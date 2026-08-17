package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type modelRequest struct {
	Model    string         `json:"model"`
	Messages []modelMessage `json:"messages"`
	Stream   bool           `json:"stream"`
}

type modelMessage struct {
	Role    string             `json:"role"`
	Content []modelContentPart `json:"content"`
}

type modelContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
	InputAudio *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio,omitempty"`
}

func (m modelMessage) text() string {
	var text strings.Builder
	for _, part := range m.Content {
		text.WriteString(part.Text)
	}
	return text.String()
}

// modelResponse is one queued response. A held response writes its opening
// frames, signals that it started, then waits for the test to release the rest
// of the body or for the request context to end, which is how a mid-turn
// cancellation is scripted.
type modelResponse struct {
	status  int
	body    string
	started chan struct{}
	rest    chan string
}

type mockModel struct {
	t      *testing.T
	server *httptest.Server

	mu        sync.Mutex
	responses []*modelResponse
	next      int
	received  []modelRequest
}

func startModel(t *testing.T, bodies ...string) *mockModel {
	t.Helper()
	model := &mockModel{t: t}
	for _, body := range bodies {
		model.queue(body)
	}
	model.server = httptest.NewServer(http.HandlerFunc(model.serveHTTP))
	t.Cleanup(func() {
		model.server.Close()
		model.mu.Lock()
		defer model.mu.Unlock()
		if remaining := len(model.responses) - model.next; remaining != 0 {
			t.Errorf("mock model has %d unused response bodies", remaining)
		}
	})
	return model
}

func withModel(model *mockModel) startOption {
	return func(config *startConfig) {
		config.environment["OX_OPENROUTER_BASE_URL"] = model.server.URL + "/api/v1"
	}
}

func (m *mockModel) queue(body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append(m.responses, &modelResponse{body: body})
}

// fail queues a non-2xx response, which is how a provider outage is scripted.
func (m *mockModel) fail(status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append(m.responses, &modelResponse{status: status, body: body})
}

// hold queues a response that writes opening and then blocks until finish or
// the request context ends.
func (m *mockModel) hold(opening string) *modelResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	held := &modelResponse{
		body:    opening,
		started: make(chan struct{}),
		rest:    make(chan string, 1),
	}
	m.responses = append(m.responses, held)
	return held
}

// await blocks until the mock model has written the held response's opening
// frames.
func (r *modelResponse) await(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(readTimeout):
		t.Fatal("mock model did not start the held response")
	}
}

// finish releases a held response with the remainder of its body.
func (r *modelResponse) finish(rest string) {
	r.rest <- rest
}

func (m *mockModel) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		m.t.Errorf("mock model method = %s, want POST", request.Method)
		http.Error(writer, "method must be POST", http.StatusMethodNotAllowed)
		return
	}
	if request.URL.Path != "/api/v1/chat/completions" {
		m.t.Errorf("mock model path = %s, want /api/v1/chat/completions", request.URL.Path)
		http.NotFound(writer, request)
		return
	}
	if authorization := request.Header.Get("Authorization"); authorization != "Bearer test-key" {
		m.t.Errorf("mock model Authorization = %q, want %q", authorization, "Bearer test-key")
		http.Error(writer, "invalid authorization", http.StatusUnauthorized)
		return
	}

	var decoded modelRequest
	if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
		m.t.Errorf("decode mock model request: %v", err)
		http.Error(writer, "invalid JSON", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.received = append(m.received, decoded)
	if m.next >= len(m.responses) {
		m.mu.Unlock()
		m.t.Errorf("mock model received more requests than queued responses")
		http.Error(writer, "no response queued", http.StatusInternalServerError)
		return
	}
	response := m.responses[m.next]
	m.next++
	m.mu.Unlock()

	if response.status != 0 {
		http.Error(writer, response.body, response.status)
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	flusher := writer.(http.Flusher)
	if !writeFrames(writer, flusher, response.body) || response.started == nil {
		return
	}
	close(response.started)
	select {
	case rest := <-response.rest:
		writeFrames(writer, flusher, rest)
	case <-request.Context().Done():
	}
}

func writeFrames(writer io.Writer, flusher http.Flusher, body string) bool {
	for _, frame := range strings.SplitAfter(body, "\n\n") {
		if frame == "" {
			continue
		}
		if _, err := io.WriteString(writer, frame); err != nil {
			return false
		}
		flusher.Flush()
	}
	return true
}

func (m *mockModel) requests() []modelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]modelRequest(nil), m.received...)
}

// frames renders chunks as server-sent events without terminating the stream.
func frames(chunks ...string) string {
	var body strings.Builder
	for _, chunk := range chunks {
		body.WriteString("data: ")
		body.WriteString(chunk)
		body.WriteString("\n\n")
	}
	return body.String()
}

func sse(chunks ...string) string {
	return frames(chunks...) + "data: [DONE]\n\n"
}

func evText(text string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%s}}]}`, jsonString(text))
}

func evReasoning(text string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"reasoning":%s}}]}`, jsonString(text))
}

func evToolCall(index int, id, kind, name, arguments string) string {
	return fmt.Sprintf(
		`{"choices":[{"delta":{"tool_calls":[{"index":%d,"id":%s,"type":%s,"function":{"name":%s,"arguments":%s}}]}}]}`,
		index, jsonString(id), jsonString(kind), jsonString(name), jsonString(arguments),
	)
}

func evFinishReason(reason string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{},"finish_reason":%s}]}`, jsonString(reason))
}

func evUsage(promptTokens, completionTokens, totalTokens int) string {
	return fmt.Sprintf(
		`{"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
		promptTokens, completionTokens, totalTokens,
	)
}

func evError(code int, message string) string {
	return fmt.Sprintf(`{"error":{"code":%d,"message":%s}}`, code, jsonString(message))
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestSSEFrames(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "stream",
			got:  sse(`{"first":1}`, `{"second":2}`),
			want: "data: {\"first\":1}\n\ndata: {\"second\":2}\n\ndata: [DONE]\n\n",
		},
		{
			name: "unterminated stream",
			got:  frames(`{"first":1}`),
			want: "data: {\"first\":1}\n\n",
		},
		{
			name: "text",
			got:  evText("hello\nworld"),
			want: `{"choices":[{"delta":{"content":"hello\nworld"}}]}`,
		},
		{
			name: "reasoning",
			got:  evReasoning("think"),
			want: `{"choices":[{"delta":{"reasoning":"think"}}]}`,
		},
		{
			name: "tool call",
			got:  evToolCall(1, "call_1", "function", "shell", `{"cmd":"go test`),
			want: `{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_1","type":"function","function":{"name":"shell","arguments":"{\"cmd\":\"go test"}}]}}]}`,
		},
		{
			name: "finish reason",
			got:  evFinishReason("tool_calls"),
			want: `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		},
		{
			name: "usage",
			got:  evUsage(4, 2, 6),
			want: `{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
		},
		{
			name: "error",
			got:  evError(502, "provider unavailable"),
			want: `{"error":{"code":502,"message":"provider unavailable"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("frame = %q, want %q", test.got, test.want)
			}
		})
	}
}

func TestMockModelQueuesResponsesAndRecordsRequests(t *testing.T) {
	wantBody := sse(evText("answer"), evFinishReason("stop"))
	model := startModel(t, wantBody)
	config := startConfig{environment: make(map[string]string)}
	withModel(model)(&config)

	request, err := http.NewRequest(
		http.MethodPost,
		config.environment["OX_OPENROUTER_BASE_URL"]+"/chat/completions",
		bytes.NewBufferString(
			`{"model":"test/model","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"stream":true}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-key")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	gotBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %s, body = %s", response.Status, gotBody)
	}
	if string(gotBody) != wantBody {
		t.Fatalf("body = %q, want %q", gotBody, wantBody)
	}

	requests := model.requests()
	if len(requests) != 1 || requests[0].Model != "test/model" || !requests[0].Stream {
		t.Fatalf("requests = %#v", requests)
	}
	if len(requests[0].Messages) != 1 ||
		requests[0].Messages[0].Role != "user" ||
		requests[0].Messages[0].text() != "hi" {
		t.Fatalf("messages = %#v", requests[0].Messages)
	}
}

func TestMockModelHoldsAResponseUntilReleased(t *testing.T) {
	model := startModel(t)
	held := model.hold(frames(evText("partial")))
	config := startConfig{environment: make(map[string]string)}
	withModel(model)(&config)

	type result struct {
		body string
		err  error
	}
	results := make(chan result, 1)
	go func() {
		request, err := http.NewRequest(
			http.MethodPost,
			config.environment["OX_OPENROUTER_BASE_URL"]+"/chat/completions",
			bytes.NewBufferString(`{"model":"test/model","messages":[],"stream":true}`),
		)
		if err != nil {
			results <- result{err: err}
			return
		}
		request.Header.Set("Authorization", "Bearer test-key")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			results <- result{err: err}
			return
		}
		body, err := io.ReadAll(response.Body)
		results <- result{body: string(body), err: errors.Join(err, response.Body.Close())}
	}()

	held.await(t)
	held.finish(sse(evFinishReason("stop")))

	select {
	case got := <-results:
		if got.err != nil {
			t.Fatal(got.err)
		}
		want := frames(evText("partial")) + sse(evFinishReason("stop"))
		if got.body != want {
			t.Fatalf("body = %q, want %q", got.body, want)
		}
	case <-time.After(readTimeout):
		t.Fatal("held response never completed")
	}
}
