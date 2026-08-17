package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type modelRequest struct {
	Model    string            `json:"model"`
	Messages []json.RawMessage `json:"messages"`
	Stream   bool              `json:"stream"`
}

type mockModel struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	bodies   []string
	nextBody int
	received []modelRequest
}

func startModel(t *testing.T, bodies ...string) *mockModel {
	t.Helper()
	model := &mockModel{t: t, bodies: append([]string(nil), bodies...)}
	model.server = httptest.NewServer(http.HandlerFunc(model.serveHTTP))
	t.Cleanup(func() {
		model.server.Close()
		model.mu.Lock()
		defer model.mu.Unlock()
		if remaining := len(model.bodies) - model.nextBody; remaining != 0 {
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
	if m.nextBody >= len(m.bodies) {
		m.mu.Unlock()
		m.t.Errorf("mock model received more requests than queued responses")
		http.Error(writer, "no response queued", http.StatusInternalServerError)
		return
	}
	body := m.bodies[m.nextBody]
	m.nextBody++
	m.mu.Unlock()

	writer.Header().Set("Content-Type", "text/event-stream")
	flusher := writer.(http.Flusher)
	for _, frame := range strings.SplitAfter(body, "\n\n") {
		if frame == "" {
			continue
		}
		if _, err := io.WriteString(writer, frame); err != nil {
			return
		}
		flusher.Flush()
	}
}

func (m *mockModel) requests() []modelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]modelRequest(nil), m.received...)
}

func sse(chunks ...string) string {
	var body strings.Builder
	for _, chunk := range chunks {
		body.WriteString("data: ")
		body.WriteString(chunk)
		body.WriteString("\n\n")
	}
	body.WriteString("data: [DONE]\n\n")
	return body.String()
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
		bytes.NewBufferString(`{"model":"test/model","messages":[],"stream":true}`),
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
}
