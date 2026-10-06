// Package openroutertest is a scripted OpenRouter for tests: an HTTP server
// that answers each model request with the next reply and records the request
// bodies.
package openroutertest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ox/internal/catalog"
	"ox/internal/openrouter"
)

// DefaultModel is a qualified model ID in Catalog.
const DefaultModel = "openrouter:deepseek/deepseek-v4.1-flash"

// Now is the time Catalog is filtered at: 2026-09-23.
const Now = 1_790_121_600

// Catalog is an OpenRouter `GET /models` response, out of name order. The
// last five models fail the catalog filter at Now. The first four are dated
// 2100, so they pass it at any time before then.
const Catalog = `{"data": [
  {"id": "acme/plain", "name": "Plain", "context_length": 8001, "created": 4102444800,
   "pricing": {"prompt": "0", "completion": "0"},
   "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
   "supported_parameters": ["tools"], "reasoning": {"mandatory": false}},
  {"id": "z-ai/glm-5.3-flash", "name": "GLM 5.3 Flash", "context_length": 1310720, "created": 4102444800,
   "pricing": {"prompt": "0.00000004", "completion": "0.00000014"},
   "architecture": {"input_modalities": ["text", "image"], "output_modalities": ["text"]},
   "supported_parameters": ["tools"],
   "reasoning": {"supported_efforts": ["future", "max", "xhigh", "high", "medium", "low"]}},
  {"id": "deepseek/deepseek-v4.1-flash", "name": "DeepSeek V4.1 Flash", "context_length": 1048576, "created": 4102444800,
   "pricing": {"prompt": "0.00000003", "completion": "0.0000006"},
   "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
   "supported_parameters": ["reasoning", "tools"],
   "reasoning": {"supported_efforts": ["max", "high", "medium", "low"], "default_effort": "high"}},
  {"id": "meta/muse-spark-1.3-contributor", "name": "Muse Spark 1.3 Contributor", "context_length": 1048576, "created": 4102444800,
   "pricing": {"prompt": "0.000001", "completion": "0.000004"},
   "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
   "supported_parameters": ["tools"],
   "reasoning": {"supported_efforts": ["xhigh", "high", "medium", "none"]}},
  {"id": "acme/no-tools", "name": "No Tools", "context_length": 1048576, "created": 1789689600,
   "pricing": {"prompt": "0.000001", "completion": "0.000002"},
   "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
   "supported_parameters": ["reasoning"]},
  {"id": "acme/image-out", "name": "Image Out", "context_length": 1048576, "created": 1789689600,
   "pricing": {"prompt": "0.000001", "completion": "0.000002"},
   "architecture": {"input_modalities": ["text"], "output_modalities": ["image"]},
   "supported_parameters": ["tools"]},
  {"id": "deepseek/deepseek-v4.1-flash:batch", "name": "DeepSeek V4.1 Flash (batch)", "context_length": 1048576, "created": 1789689600,
   "pricing": {"prompt": "0.000001", "completion": "0.000002"},
   "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
   "supported_parameters": ["tools"]},
  {"id": "acme/old", "name": "Old", "context_length": 1048576, "created": 1774310399,
   "pricing": {"prompt": "0.000001", "completion": "0.000002"},
   "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
   "supported_parameters": ["tools"]},
  {"id": "openrouter/auto-beta", "name": "Auto Beta", "context_length": 2000000, "created": 1789689600,
   "pricing": {"prompt": "-1", "completion": "-1"},
   "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
   "supported_parameters": ["tools"]}
]}`

// ParsedCatalog returns Catalog filtered at Now: DeepSeek V4.1 Flash, GLM 5.3
// Flash, Muse Spark 1.3 Contributor, and Plain.
func ParsedCatalog() catalog.Catalog {
	models, err := openrouter.ParseCatalog([]byte(Catalog), Now)
	if err != nil {
		panic(err)
	}
	return models
}

// Reply answers one model request, given its decoded body.
type Reply func(w http.ResponseWriter, r *http.Request, body map[string]any)

// Server is a scripted OpenRouter.
type Server struct {
	*httptest.Server
	mu      sync.Mutex
	replies []Reply
	bodies  []map[string]any
}

// Start serves replies in request order. Catalog and key requests are
// answered by the next reply too.
func Start(t testing.TB, replies ...Reply) *Server {
	s := &Server{replies: replies}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	var body map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &body); err != nil {
			panic(err)
		}
	}
	s.mu.Lock()
	if body != nil {
		s.bodies = append(s.bodies, body)
	}
	if len(s.replies) == 0 {
		s.mu.Unlock()
		http.Error(w, `{"error":"no scripted reply"}`, http.StatusTeapot)
		return
	}
	reply := s.replies[0]
	s.replies = s.replies[1:]
	s.mu.Unlock()
	reply(w, r, body)
}

// Client returns a client for this server.
func (s *Server) Client() *openrouter.Client {
	return openrouter.NewForEndpoint("test-key", s.URL)
}

// Bodies returns the request bodies received so far.
func (s *Server) Bodies() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any{}, s.bodies...)
}

// Delta is one stream chunk with a delta and an optional finish reason.
func Delta(delta map[string]any, finish string) map[string]any {
	choice := map[string]any{"index": 0, "delta": delta, "finish_reason": nil}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	return map[string]any{"id": "gen-1", "object": "chat.completion.chunk", "choices": []any{choice}}
}

// Usage is the usage chunk OpenRouter sends after the finish chunk.
func Usage(input, output int, cost float64) map[string]any {
	chunk := Delta(map[string]any{"content": ""}, "stop")
	chunk["usage"] = map[string]any{"prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output, "cost": cost}
	return chunk
}

// SSE is a stream body: a keep-alive comment, one data event per chunk, and
// `[DONE]`.
func SSE(chunks ...map[string]any) string {
	body := ": OPENROUTER PROCESSING\n\n"
	for _, chunk := range chunks {
		data, _ := json.Marshal(chunk)
		body += "data: " + string(data) + "\n\n"
	}
	return body + "data: [DONE]\n\n"
}

// Stream sends an SSE body, one event per flush, the way OpenRouter sends a
// stream it is still generating.
func Stream(body string) Reply {
	return func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range strings.SplitAfter(body, "\n\n") {
			io.WriteString(w, event)
			w.(http.Flusher).Flush()
		}
	}
}

// Chunks streams the chunks.
func Chunks(chunks ...map[string]any) Reply { return Stream(SSE(chunks...)) }

// Text answers with text and finishes.
func Text(text string) Reply {
	return Chunks(Delta(map[string]any{"role": "assistant", "content": text}, ""), Delta(map[string]any{}, "stop"))
}

// Status answers with a status code and JSON body.
func Status(code int, body string) Reply {
	return func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

// Hang sends the start of a stream, then holds the request open until the
// client gives up.
func Hang(prefix string) Reply {
	return func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, prefix)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
}

// Gated sends reply only once gate closes, so its model request stays in
// flight until the test releases it.
func Gated(gate <-chan struct{}, reply Reply) Reply {
	return func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		select {
		case <-gate:
			reply(w, r, body)
		case <-r.Context().Done():
		}
	}
}

// Call is one tool call of a scripted assistant message.
type Call struct {
	ID        string
	Name      string
	Arguments any
}

// Calls answers with one assistant message making the calls.
func Calls(calls ...Call) Reply {
	var toolCalls []any
	for i, call := range calls {
		arguments, _ := json.Marshal(call.Arguments)
		toolCalls = append(toolCalls, map[string]any{
			"index": i, "id": call.ID, "type": "function",
			"function": map[string]any{"name": call.Name, "arguments": string(arguments)},
		})
	}
	return Chunks(Delta(map[string]any{"role": "assistant", "tool_calls": toolCalls}, "tool_calls"))
}

// Shell answers with one shell call per command.
func Shell(commands ...string) Reply {
	var calls []Call
	for i, command := range commands {
		calls = append(calls, Call{fmt.Sprintf("shell-%d", i), "shell", map[string]any{"command": command}})
	}
	return Calls(calls...)
}

// Echo answers with the latest user message's text.
func Echo() Reply {
	return func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		messages := body["messages"].([]any)
		for i := len(messages) - 1; i >= 0; i-- {
			message := messages[i].(map[string]any)
			if message["role"] == "user" {
				Text(fmt.Sprintf("you said: %v", message["content"]))(w, r, body)
				return
			}
		}
		panic("an echo request has a user message")
	}
}
