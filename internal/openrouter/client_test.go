package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sse(chunks ...string) string {
	var body strings.Builder
	body.WriteString(": OPENROUTER PROCESSING\n\n")
	for _, chunk := range chunks {
		body.WriteString("data: ")
		body.WriteString(chunk)
		body.WriteString("\n\n")
	}
	body.WriteString("data: [DONE]\n\n")
	return body.String()
}

func testClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestClientStreamsAndAssemblesCompletion(t *testing.T) {
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var err error
		received, err = io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		if request.Method != http.MethodPost ||
			request.URL.Path != "/api/v1/chat/completions" ||
			request.Header.Get("Accept") != "text/event-stream" ||
			request.Header.Get("Content-Type") != "application/json" ||
			request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request = %s %s, headers = %#v", request.Method, request.URL, request.Header)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(
			`{"choices":[{"delta":{"reasoning":"think"}}]}`,
			`{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6,"cost":0.002}}`,
		))
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL + "/api/v1")
	client.APIKey = func() string { return "secret" }
	var deltas []Delta
	completion, err := client.Stream(t.Context(), Request{
		Model: "author/model",
		Messages: []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: "text", Text: "hi"}},
		}},
	}, func(delta Delta) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "answer" ||
		completion.Reasoning != "think" ||
		completion.FinishReason != "stop" ||
		completion.Usage == nil ||
		completion.Usage.TotalTokens != 6 {
		t.Fatalf("completion = %#v", completion)
	}
	if len(deltas) != 2 ||
		deltas[0].Kind != DeltaReasoning ||
		deltas[1].Kind != DeltaText {
		t.Fatalf("deltas = %#v", deltas)
	}

	var encoded map[string]any
	if err := json.Unmarshal(received, &encoded); err != nil {
		t.Fatal(err)
	}
	if encoded["stream"] != true {
		t.Fatalf("request body = %s", received)
	}
}

func TestClientReturnsPartialCompletionForMidStreamErrorWithoutRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(
			`{"choices":[{"delta":{"content":"partial ","reasoning":"thinking"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"x\":1}"}}]}}]}`,
			`{"error":{"code":502,"message":"upstream failed"},"choices":[{"finish_reason":"error"}]}`,
		))
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.retryWait = noWait
	completion, err := client.Stream(t.Context(), Request{Model: "author/model"}, nil)
	if err == nil || !strings.Contains(err.Error(), "upstream failed") {
		t.Fatalf("error = %v", err)
	}
	if completion.Text != "partial " ||
		completion.Reasoning != "thinking" ||
		len(completion.ToolCalls) != 1 {
		t.Fatalf("partial completion = %#v", completion)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestClientDoesNotRetryMalformedSuccessfulStream(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: not-json\n\n")
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.retryWait = noWait
	_, err := client.Stream(t.Context(), Request{Model: "author/model"}, nil)
	if err == nil || !strings.Contains(err.Error(), "decode OpenRouter stream event") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestClientRetriesRetryAfterAndSucceeds(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(`{"choices":[{"delta":{"content":"ok"}}]}`))
	}))
	t.Cleanup(server.Close)

	started := time.Now()
	completion, err := testClient(server.URL).Stream(
		t.Context(),
		Request{Model: "author/model"},
		nil,
	)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "ok" || requests.Load() != 2 {
		t.Fatalf("completion = %#v, requests = %d", completion, requests.Load())
	}
	if elapsed < 1800*time.Millisecond || elapsed > 3500*time.Millisecond {
		t.Fatalf("Retry-After elapsed = %v", elapsed)
	}
}

func TestClientRetriesTransientStatusesWithBoundedJitter(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) == 1 {
					http.Error(w, "transient", status)
					return
				}
				_, _ = io.WriteString(w, sse(`{"choices":[{"delta":{"content":"ok"}}]}`))
			}))
			t.Cleanup(server.Close)

			var delays []time.Duration
			client := testClient(server.URL)
			client.retryWait = func(_ context.Context, delay time.Duration) error {
				delays = append(delays, delay)
				return nil
			}
			if _, err := client.Stream(t.Context(), Request{Model: "author/model"}, nil); err != nil {
				t.Fatal(err)
			}
			if len(delays) != 1 ||
				delays[0] < baseBackoff/2 ||
				delays[0] > baseBackoff {
				t.Fatalf("delays = %#v", delays)
			}
		})
	}
}

func TestClientDoesNotRetryFatalStatuses(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
		http.StatusForbidden,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				http.Error(w, "fatal", status)
			}))
			t.Cleanup(server.Close)

			client := testClient(server.URL)
			client.retryWait = noWait
			if _, err := client.Stream(
				t.Context(),
				Request{Model: "author/model"},
				nil,
			); err == nil {
				t.Fatal("fatal status succeeded")
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d", requests.Load())
			}
		})
	}
}

func TestClientRetryBudgetBoundsAttempts(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "transient", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client := testClient(server.URL)
	client.retryBudgetOverride = 100 * time.Millisecond
	client.retryWait = noWait
	if _, err := client.Stream(
		t.Context(),
		Request{Model: "author/model"},
		nil,
	); err == nil {
		t.Fatal("transient failure succeeded")
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestClientCancellationAbortsRequestAndReturnsPartialOutput(t *testing.T) {
	requestAborted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-request.Context().Done()
		close(requestAborted)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	client := testClient(server.URL)
	completion, err := client.Stream(ctx, Request{Model: "author/model"}, func(Delta) {
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if completion.Text != "partial" {
		t.Fatalf("partial completion = %#v", completion)
	}
	select {
	case <-requestAborted:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request was not aborted")
	}
}

func TestClientReturnsPartialOutputWhenBodyEndsWithoutDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	t.Cleanup(server.Close)

	completion, err := testClient(server.URL).Stream(
		t.Context(),
		Request{Model: "author/model"},
		nil,
	)
	if !errors.Is(err, errStreamEnded) {
		t.Fatalf("error = %v", err)
	}
	if completion.Text != "partial" {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestClientStopsAtDoneWithoutWaitingForBodyClose(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, sse(`{"choices":[{"delta":{"content":"ok"}}]}`))
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	done := make(chan error, 1)
	go func() {
		_, err := testClient(server.URL).Stream(
			context.Background(),
			Request{Model: "author/model"},
			nil,
		)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("[DONE] did not end the completion")
	}
}

func TestClientRequestProviderFalseIsOmitted(t *testing.T) {
	var (
		mu       sync.Mutex
		received []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		mu.Lock()
		received = raw
		mu.Unlock()
		_, _ = io.WriteString(w, sse(`{"choices":[{"delta":{"content":"ok"}}]}`))
	}))
	t.Cleanup(server.Close)

	disabled := false
	client := testClient(server.URL)
	_, err := client.Stream(t.Context(), Request{
		Model:    "author/model",
		Tools:    []Tool{{Type: "function"}},
		Provider: &Provider{RequireParameters: &disabled},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var encoded map[string]any
	if err := json.Unmarshal(received, &encoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := encoded["provider"]; ok {
		t.Fatalf("provider was not omitted: %s", received)
	}
	if encoded["tool_choice"] != "auto" {
		t.Fatalf("tool choice = %#v", encoded["tool_choice"])
	}
}

func TestClientAndCatalogNeverWriteToStdout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failure", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	defer func() {
		os.Stdout = original
	}()

	client := testClient(server.URL)
	client.cachePathOverride = filepath.Join(t.TempDir(), "models.json")
	_, streamErr := client.Stream(t.Context(), Request{Model: "author/model"}, nil)
	_, catalogErr := client.Catalog(t.Context())

	os.Stdout = original
	closeErr := writer.Close()
	raw, readErr := io.ReadAll(reader)
	readerCloseErr := reader.Close()
	if closeErr != nil || readErr != nil || readerCloseErr != nil {
		t.Fatalf(
			"capture stdout: close writer=%v, read=%v, close reader=%v",
			closeErr,
			readErr,
			readerCloseErr,
		)
	}
	if streamErr == nil || catalogErr == nil {
		t.Fatalf("expected failures: stream=%v, catalog=%v", streamErr, catalogErr)
	}
	if len(raw) != 0 {
		t.Fatalf("stdout = %q", raw)
	}
}

func noWait(context.Context, time.Duration) error {
	return nil
}
