package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testModelCatalog = `{"data":[
{"id":"test/model","context_length":128000},
{"id":"canonical/model","context_length":128000},
{"id":"environment/model","context_length":128000},
{"id":"first/model","context_length":128000},
{"id":"fixed/model","context_length":128000},
{"id":"global/model","context_length":128000},
{"id":"good/model","context_length":128000},
{"id":"home/model","context_length":128000},
{"id":"new/model","context_length":128000},
{"id":"old/model","context_length":128000},
{"id":"parent/model","context_length":128000},
{"id":"second/model","context_length":128000},
{"id":"workspace/model","context_length":128000}
]}`

type modelRequest struct {
	Model         string            `json:"model"`
	Messages      []modelMessage    `json:"messages"`
	Tools         []json.RawMessage `json:"tools"`
	Stream        bool              `json:"stream"`
	Authorization string            `json:"-"`
}

type modelMessage struct {
	Role       string             `json:"role"`
	Content    []modelContentPart `json:"content"`
	ToolCalls  []modelToolCall    `json:"tool_calls"`
	ToolCallID string             `json:"tool_call_id"`
}

type modelToolCall struct {
	ID string `json:"id"`
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
	prompt   string
	status   int
	body     string
	started  chan struct{}
	rest     chan string
	consumed bool
}

type mockModel struct {
	t       *testing.T
	server  *httptest.Server
	catalog string

	mu               sync.Mutex
	responses        []*modelResponse
	received         []modelRequest
	credentialStatus int
	credentialBody   string
	credentialChecks []string
}

func startModel(t *testing.T, bodies ...string) *mockModel {
	t.Helper()
	model := &mockModel{t: t, catalog: testModelCatalog}
	for _, body := range bodies {
		model.queue(body)
	}
	model.server = httptest.NewServer(http.HandlerFunc(model.serveHTTP))
	t.Cleanup(func() {
		model.server.Close()
		model.mu.Lock()
		defer model.mu.Unlock()
		remaining := 0
		for _, response := range model.responses {
			if !response.consumed {
				remaining++
			}
		}
		if remaining != 0 {
			t.Errorf("mock model has %d unmatched responses", remaining)
		}
	})
	return model
}

func withModel(model *mockModel) startOption {
	return func(config *startConfig) {
		setStringFlag(config, "--openrouter-base-url", model.server.URL+"/api/v1")
	}
}

func flagValue(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}

func withModelContextWindow(model *mockModel, contextWindow int) startOption {
	return func(config *startConfig) {
		catalog := strings.Replace(
			testModelCatalog,
			`{"id":"test/model","context_length":128000}`,
			fmt.Sprintf(`{"id":"test/model","context_length":%d}`, contextWindow),
			1,
		)
		model.catalog = catalog
		config.files[filepath.Join("cache", "ox", "models.json")] = catalog
	}
}

func (m *mockModel) queue(body string) {
	m.queueFor("", body)
}

// queueFor queues a response for the request whose final message has prompt.
func (m *mockModel) queueFor(prompt, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append(m.responses, &modelResponse{prompt: prompt, body: body})
}

// fail queues a non-2xx response, which is how a provider outage is scripted.
func (m *mockModel) fail(status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append(m.responses, &modelResponse{status: status, body: body})
}

func (m *mockModel) rejectCredential(message string) {
	m.failCredential(
		http.StatusUnauthorized,
		fmt.Sprintf(`{"error":{"code":401,"message":%s}}`, jsonString(message)),
	)
}

func (m *mockModel) failCredential(status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentialStatus = status
	m.credentialBody = body
}

// hold queues a response that writes opening and then blocks until finish or
// the request context ends.
func (m *mockModel) hold(opening string) *modelResponse {
	return m.holdFor("", opening)
}

// holdFor queues a held response for the request whose final message has
// prompt.
func (m *mockModel) holdFor(prompt, opening string) *modelResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	held := &modelResponse{
		prompt:  prompt,
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
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")) == "" {
		m.t.Errorf("mock model Authorization = %q, want a bearer credential", authorization)
		http.Error(writer, "invalid authorization", http.StatusUnauthorized)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/api/v1/auth/key" {
		m.serveCredentialCheck(writer, authorization)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/api/v1/models" {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, m.catalog)
		return
	}
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

	var decoded modelRequest
	if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
		m.t.Errorf("decode mock model request: %v", err)
		http.Error(writer, "invalid JSON", http.StatusBadRequest)
		return
	}
	decoded.Authorization = authorization

	m.mu.Lock()
	m.received = append(m.received, decoded)
	prompt := ""
	if len(decoded.Messages) != 0 {
		prompt = decoded.Messages[len(decoded.Messages)-1].text()
	}
	var response *modelResponse
	for _, candidate := range m.responses {
		if candidate.consumed || candidate.prompt != "" && candidate.prompt != prompt {
			continue
		}
		candidate.consumed = true
		response = candidate
		break
	}
	if response == nil {
		m.mu.Unlock()
		m.t.Errorf("mock model has no unmatched response for prompt %q", prompt)
		http.Error(writer, "no response queued", http.StatusInternalServerError)
		return
	}
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

func (m *mockModel) serveCredentialCheck(writer http.ResponseWriter, authorization string) {
	m.mu.Lock()
	m.credentialChecks = append(m.credentialChecks, authorization)
	status, body := m.credentialStatus, m.credentialBody
	m.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
		body = `{"data":{}}`
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, body)
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

func (m *mockModel) checks() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.credentialChecks...)
}

// requestFor returns the recorded request whose final message has prompt.
func (m *mockModel) requestFor(prompt string) modelRequest {
	m.t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, request := range m.received {
		if len(request.Messages) != 0 && request.Messages[len(request.Messages)-1].text() == prompt {
			return request
		}
	}
	m.t.Fatalf("mock model received no request for prompt %q", prompt)
	return modelRequest{}
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

func evUsageCost(promptTokens, completionTokens, totalTokens int, cost float64) string {
	return fmt.Sprintf(
		`{"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,"cost":%g}}`,
		promptTokens, completionTokens, totalTokens, cost,
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
		flagValue(config.arguments, "--openrouter-base-url")+"/chat/completions",
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

func TestMockModelRoutesConcurrentRequestsByFinalMessage(t *testing.T) {
	model := startModel(t)
	model.queueFor("alpha", sse(evText("alpha answer")))
	model.queueFor("beta", sse(evText("beta answer")))
	config := startConfig{environment: make(map[string]string)}
	withModel(model)(&config)

	type result struct {
		prompt string
		body   string
		status int
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, prompt := range []string{"alpha", "beta"} {
		go func() {
			<-start
			body, err := json.Marshal(modelRequest{
				Model: "test/model",
				Messages: []modelMessage{{
					Role: "user",
					Content: []modelContentPart{{
						Type: "text",
						Text: prompt,
					}},
				}},
				Stream: true,
			})
			if err != nil {
				results <- result{prompt: prompt, err: err}
				return
			}
			request, err := http.NewRequest(
				http.MethodPost,
				flagValue(config.arguments, "--openrouter-base-url")+"/chat/completions",
				bytes.NewReader(body),
			)
			if err != nil {
				results <- result{prompt: prompt, err: err}
				return
			}
			request.Header.Set("Authorization", "Bearer test-key")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				results <- result{prompt: prompt, err: err}
				return
			}
			responseBody, readErr := io.ReadAll(response.Body)
			results <- result{
				prompt: prompt,
				body:   string(responseBody),
				status: response.StatusCode,
				err:    errors.Join(readErr, response.Body.Close()),
			}
		}()
	}
	close(start)

	want := map[string]string{
		"alpha": sse(evText("alpha answer")),
		"beta":  sse(evText("beta answer")),
	}
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.status != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", got.prompt, got.status, http.StatusOK)
		}
		if got.body != want[got.prompt] {
			t.Fatalf("%s body = %q, want %q", got.prompt, got.body, want[got.prompt])
		}
	}

	for _, prompt := range []string{"alpha", "beta"} {
		request := model.requestFor(prompt)
		if got := request.Messages[len(request.Messages)-1].text(); got != prompt {
			t.Fatalf("requestFor(%q) final message = %q", prompt, got)
		}
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
			flagValue(config.arguments, "--openrouter-base-url")+"/chat/completions",
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
