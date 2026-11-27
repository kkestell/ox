package ur

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/children"
	"github.com/kkestell/ox/internal/ur/engine"
)

// TestMain runs a stand-in MCP forwarder, or ur itself, when a test starts
// this binary as one. ur passes its environment to the forwarder, so the
// forwarder's variable is checked first.
func TestMain(m *testing.M) {
	if os.Getenv("OX_UR_FORWARDER") == "1" {
		standInForwarder()
		os.Exit(0)
	}
	if os.Getenv("OX_UR_MAIN") == "1" {
		os.Exit(engine.Main(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// standInForwarder serves one tool, echo, which answers with its text
// argument. It writes its PID to the file OX_UR_FORWARDER_PID names. A call
// with the text "hold" is answered only when the next call arrives, just
// before it, and one with the text "exit" ends the forwarder unanswered.
func standInForwarder() {
	os.WriteFile(os.Getenv("OX_UR_FORWARDER_PID"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	out := json.NewEncoder(os.Stdout)
	reply := func(id json.RawMessage, result any) {
		out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	text := func(text string) map[string]any {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": false}
	}
	var held json.RawMessage
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Arguments struct {
					Text string `json:"text"`
				} `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.ID == nil {
			continue
		}
		switch request.Method {
		case "initialize":
			reply(request.ID, map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}})
		case "tools/list":
			reply(request.ID, map[string]any{"tools": []any{map[string]any{
				"name": "echo", "description": "Echo the text.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
			}}})
		case "tools/call":
			if held != nil {
				reply(held, text("hold"))
				held = nil
			}
			switch request.Params.Arguments.Text {
			case "hold":
				held = request.ID
				continue
			case "exit":
				return
			}
			reply(request.ID, text(request.Params.Arguments.Text))
		}
	}
}

// urHarness is the harness with this test binary as ur.
func urHarness(t *testing.T, endpoint, dir string, providers map[string][]string) agent.Harness {
	t.Helper()
	t.Setenv("OX_UR_MAIN", "1")
	return Harness(os.Args[0], endpoint, dir, providers)
}

const model = "deepseek/test"

// sse writes a streamed completion of words, ending with data: [DONE].
func sse(w http.ResponseWriter, words ...string) {
	fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n")
	for _, word := range words {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", word)
	}
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"total_tokens\":3}}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// sseReasoning streams each reasoning detail fragment as its own chunk.
func sseReasoning(w http.ResponseWriter, fragments ...map[string]any) {
	for _, f := range fragments {
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "", "reasoning_details": []any{f}}}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
}

// reasoningText is a fragment of readable reasoning, as DeepSeek streams it.
func reasoningText(text string) map[string]any {
	return map[string]any{"type": "reasoning.text", "text": text, "format": "unknown", "index": 0}
}

// recorded is a request the server received.
type recorded struct {
	auth string
	body map[string]any
}

// run is an agent driven from the test goroutine against a canned server.
type run struct {
	t          *testing.T
	harness    agent.Harness
	dir        string
	workspace  string
	mcp        agent.MCPServer
	settings   agent.Settings // the settings the next launch starts with
	agent      agent.Agent
	updates    []agent.Update
	configured []agent.Settings
	tokens     []string // every token OnResumable received
	requests   chan recorded
}

// handler answers the nth request, counting from 0.
type handler func(w http.ResponseWriter, req *http.Request, n int)

func start(t *testing.T, settings agent.Settings, providers map[string][]string, handle handler) *run {
	t.Helper()
	return startMCP(t, settings, providers, agent.MCPServer{}, handle)
}

// startMCP starts a run whose agent has mcp as Ox's MCP server.
func startMCP(t *testing.T, settings agent.Settings, providers map[string][]string, mcp agent.MCPServer, handle handler) *run {
	t.Helper()
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	r := &run{t: t, dir: filepath.Join(t.TempDir(), "ur"), workspace: t.TempDir(), mcp: mcp, settings: settings, requests: make(chan recorded, 16)}
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, _ := io.ReadAll(req.Body)
		var body map[string]any
		json.Unmarshal(data, &body)
		r.requests <- recorded{req.Header.Get("Authorization"), body}
		handle(w, req, int(count.Add(1))-1)
	}))
	t.Cleanup(server.Close)
	r.harness = urHarness(t, server.URL, r.dir, providers)
	if err := r.launch(""); err != nil {
		t.Fatal(err)
	}
	return r
}

// launch starts the run's agent with the latest settings, resuming the
// conversation token names, as the server does after parking.
func (r *run) launch(token string) error {
	a, err := r.harness.Start(agent.Options{
		SessionID:   "ur-session",
		MCP:         r.mcp,
		Resume:      token,
		Workspace:   r.workspace,
		Settings:    r.settings,
		OnUpdate:    func(u agent.Update) { r.updates = append(r.updates, u) },
		OnResumable: func(token string) { r.tokens = append(r.tokens, token) },
		OnConfigured: func(s agent.Settings) {
			r.configured = append(r.configured, s)
			r.settings = s
		},
	})
	if err != nil {
		return err
	}
	r.agent = a
	r.t.Cleanup(func() {
		if !a.Exited() {
			a.Stop()
		}
	})
	return nil
}

// file is the conversation file of the run's only conversation.
func (r *run) file() string {
	r.t.Helper()
	if len(r.tokens) != 1 {
		r.t.Fatalf("OnResumable got %q, not one token", r.tokens)
	}
	return filepath.Join(r.dir, r.tokens[0]+".jsonl")
}

func (r *run) send(id, content string) {
	r.t.Helper()
	if err := r.agent.Send(agent.Input{MessageID: id, Text: content, Content: content}); err != nil {
		r.t.Fatal(err)
	}
}

// until handles events until an update satisfies done, and returns it.
func (r *run) until(done func(agent.Update) bool) agent.Update {
	r.t.Helper()
	timeout := time.After(5 * time.Second)
	for seen := 0; ; {
		for ; seen < len(r.updates); seen++ {
			if done(r.updates[seen]) {
				u := r.updates[seen]
				r.updates = r.updates[seen+1:]
				return u
			}
		}
		select {
		case e := <-r.agent.Events():
			r.agent.Handle(e)
		case <-timeout:
			r.t.Fatalf("no matching update; got %+v", r.updates)
		}
	}
}

// turn handles events until the agent is idle, and returns every update
// published until then.
func (r *run) turn() []agent.Update {
	r.t.Helper()
	var seen []agent.Update
	r.until(func(u agent.Update) bool {
		seen = append(seen, u)
		return u.Type == agent.UpdateState && u.State == agent.StateIdle
	})
	return seen
}

func (r *run) idle() {
	r.t.Helper()
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateState && u.State == agent.StateIdle })
}

// result handles events until the turn's result, and returns it.
func (r *run) result() agent.Result {
	r.t.Helper()
	return *r.until(func(u agent.Update) bool { return u.Type == agent.UpdateResult }).Result
}

func (r *run) request() recorded {
	r.t.Helper()
	select {
	case req := <-r.requests:
		return req
	case <-time.After(5 * time.Second):
		r.t.Fatal("no request")
		return recorded{}
	}
}

func roles(body map[string]any) []string {
	return field(body, "role")
}

func contents(body map[string]any) []string {
	return field(body, "content")
}

// field is the given field of each of a request's conversation messages that
// has it; the system message is not part of the conversation.
func field(body map[string]any, name string) []string {
	var values []string
	for _, m := range body["messages"].([]any) {
		if m.(map[string]any)["role"] == "system" {
			continue
		}
		if value, ok := m.(map[string]any)[name].(string); ok {
			values = append(values, value)
		}
	}
	return values
}

func TestTwoTurns(t *testing.T) {
	settings := agent.Settings{Model: model, Effort: "low", PermissionMode: "manual"}
	r := start(t, settings, map[string][]string{model: {"a/x", "b/y"}}, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			sseReasoning(w, reasoningText("Greet"), reasoningText(" them."),
				map[string]any{"type": "reasoning.encrypted", "data": "opaque", "index": 1})
		}
		sse(w, "Hello", ", ", "world")
	})
	r.send("p1", "hi")
	var kinds, reasoning []string
	for {
		u := r.until(func(agent.Update) bool { return true })
		if u.MessageID != "p1" {
			t.Errorf("%s update carries message ID %q, not p1", u.Type, u.MessageID)
		}
		kind := u.Type
		if u.State != "" {
			kind += " " + u.State
		}
		if u.Type == agent.UpdateTextDelta && u.Text == "" {
			t.Error("an empty text delta was published")
		}
		if u.Type == agent.UpdateReasoningDelta {
			reasoning = append(reasoning, u.Text)
		}
		if u.Type == agent.UpdateResult {
			if u.Result.IsError || u.Result.Text != "Hello, world" {
				t.Errorf("result %+v, not the joined reply", u.Result)
			}
		}
		if len(kinds) == 0 || kinds[len(kinds)-1] != kind {
			kinds = append(kinds, kind)
		}
		if kind == "state idle" {
			break
		}
	}
	want := []string{"prompt_queued", "state running", "prompt", "reasoning_delta", "text_delta", "result", "state idle"}
	if !slices.Equal(kinds, want) {
		t.Errorf("updates %q, not %q", kinds, want)
	}
	if want := []string{"Greet", " them."}; !slices.Equal(reasoning, want) {
		t.Errorf("reasoning %q, not %q", reasoning, want)
	}
	r.request()

	r.send("p2", "again")
	r.idle()
	second := r.request()
	if second.auth != "Bearer test-key" {
		t.Errorf("Authorization %q", second.auth)
	}
	if second.body["model"] != model || second.body["stream"] != true {
		t.Errorf("model or stream wrong: %v", second.body)
	}
	if effort := second.body["reasoning"].(map[string]any)["effort"]; effort != "low" {
		t.Errorf("reasoning effort %v", effort)
	}
	p := second.body["provider"].(map[string]any)
	if fmt.Sprint(p["order"]) != "[a/x b/y]" || p["allow_fallbacks"] != false {
		t.Errorf("provider %v", p)
	}
	if got := roles(second.body); !slices.Equal(got, []string{"user", "assistant", "user"}) {
		t.Errorf("messages %q", got)
	}
	for _, m := range messages(second.body) {
		if details, ok := m["reasoning_details"]; ok {
			t.Errorf("the first turn's reasoning was sent again: %v", details)
		}
	}
}

func TestUnpinnedModelWithoutEffort(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "manual"}, map[string][]string{"other": {"a/x"}}, func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w, "ok")
	})
	r.send("p1", "hi")
	r.idle()
	body := r.request().body
	if _, ok := body["provider"]; ok {
		t.Errorf("a model without providers sent provider: %v", body["provider"])
	}
	if _, ok := body["reasoning"]; ok {
		t.Errorf("an empty effort sent reasoning: %v", body["reasoning"])
	}
}

// The stream's last line counts even without a trailing newline.
func TestLastLineWithoutNewline(t *testing.T) {
	for name, stream := range map[string]string{
		"[DONE]":       "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]",
		"finish chunk": "data: {\"choices\":[{\"delta\":{\"content\":\"o\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"k\"},\"finish_reason\":\"stop\"}]}",
	} {
		t.Run(name, func(t *testing.T) {
			r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
				fmt.Fprint(w, stream)
			})
			r.send("p1", "hi")
			if result := r.result(); result.IsError || result.Text != "ok" {
				t.Errorf("result %+v", result)
			}
		})
	}
}

// A reply that finishes without text ends the turn with an empty result.
func TestEmptyReply(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w)
	})
	r.send("p1", "hi")
	if result := r.result(); result.IsError || result.Text != "" {
		t.Errorf("result %+v, not an empty reply", result)
	}
}

// A failed turn ends with its error. The next request carries the reply the
// user saw, and no empty reply when there was none.
func TestStreamErrors(t *testing.T) {
	for name, c := range map[string]struct {
		handle handler
		want   string
		roles  []string // of the next request
	}{
		"mid-stream error chunk": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"error\":{\"code\":502,\"message\":\"provider went away\"},\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"error\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}, "OpenRouter: provider went away", []string{"user", "assistant", "user"}},
		"refused request": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":401,"message":"No auth credentials found"}}`)
		}, "OpenRouter 401 Unauthorized: No auth credentials found", []string{"user", "user"}},
		"refused request without a message": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "upstream down")
		}, "OpenRouter 502 Bad Gateway", []string{"user", "user"}},
		"refused request with an error without a message": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"code":502}}`)
		}, "OpenRouter 502 Bad Gateway", []string{"user", "user"}},
		"unfinished stream": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n")
		}, "OpenRouter stream ended before the response finished", []string{"user", "assistant", "user"}},
		"cut connection": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}, "unexpected EOF", []string{"user", "assistant", "user"}},
		"tool call without an id or name": {func(w http.ResponseWriter, _ *http.Request, n int) {
			if n > 0 {
				sse(w, "ok")
				return
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{}\"}}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}, "OpenRouter sent a tool call without an id or name", []string{"user", "user"}},
		"malformed chunk": {func(w http.ResponseWriter, _ *http.Request, _ int) {
			fmt.Fprint(w, "data: {\"choices\":\n\n")
		}, "malformed OpenRouter stream chunk: unexpected end of JSON input", []string{"user", "user"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := start(t, agent.Settings{Model: model}, nil, c.handle)
			r.send("p1", "hi")
			if result := r.result(); !result.IsError || result.Text != c.want {
				t.Errorf("result %+v, not the error %q", result, c.want)
			}
			r.idle()
			r.request()
			r.send("p2", "again")
			r.idle()
			if got := roles(r.request().body); !slices.Equal(got, c.roles) {
				t.Errorf("the next request's messages %q, not %q", got, c.roles)
			}
		})
	}
}

// interrupt interrupts the agent and handles events until the answer, which
// must come without error. The updates published until then are in r.updates.
func interrupt(t *testing.T, r *run) {
	t.Helper()
	answered := false
	r.agent.Interrupt(func(err error) {
		if err != nil {
			t.Errorf("Interrupt answered %v", err)
		}
		answered = true
	})
	timeout := time.After(5 * time.Second)
	for !answered {
		select {
		case e := <-r.agent.Events():
			r.agent.Handle(e)
		case <-timeout:
			t.Fatalf("Interrupt was not answered; got %+v", r.updates)
		}
	}
}

func TestInterrupt(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, req *http.Request, n int) {
		if n > 0 {
			sse(w, "ok")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	})
	r.send("p1", "hi")
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta })
	r.send("p2", "next")
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdatePromptQueued })
	r.updates = nil
	// The answer comes only once the stopped turn's result and idle state
	// are published.
	interrupt(t, r)
	var kinds []string
	for _, u := range r.updates {
		kinds = append(kinds, u.Type+" "+u.MessageID+" "+u.State)
	}
	want := []string{"prompt_cancelled p2 ", "result p1 ", "state p1 idle"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("interrupt published %q before its answer, not %q", kinds, want)
	}
	if result := r.updates[1].Result; !result.IsError || result.Text != "Interrupted" {
		t.Errorf("interrupted result %+v", result)
	}
	r.updates = nil
	r.request()

	// The interrupted request's end arrives during the next turn.
	r.send("p3", "again")
	for {
		u := r.until(func(agent.Update) bool { return true })
		if u.Type == agent.UpdateTextDelta && u.Text != "ok" {
			t.Errorf("the interrupted turn's text %q reached the next turn", u.Text)
		}
		if u.Type == agent.UpdateResult && (u.Result.IsError || u.Result.Text != "ok") {
			t.Errorf("the turn after the interrupt ended with %+v", u.Result)
		}
		if u.Type == agent.UpdateState && u.State == agent.StateIdle {
			break
		}
	}
	if got := contents(r.request().body); !slices.Equal(got, []string{"hi", "partial", "again"}) {
		t.Errorf("history after the interrupt %q", got)
	}
	r.updates = nil
	interrupt(t, r)
	if len(r.updates) != 0 {
		t.Errorf("Interrupt while idle published %+v", r.updates)
	}
}

func TestInputDuringTurnRunsNext(t *testing.T) {
	release := make(chan struct{})
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			<-release
		}
		sse(w, fmt.Sprint("reply ", n))
	})
	if !r.agent.Parkable() {
		t.Error("a new agent is not parkable")
	}
	r.send("p1", "first")
	r.request()
	var kinds []string
	describe := func(u agent.Update) {
		if u.Type != agent.UpdateTextDelta {
			kinds = append(kinds, u.Type+" "+u.MessageID+" "+u.State)
		}
	}
	r.until(func(u agent.Update) bool {
		describe(u)
		return u.Type == agent.UpdatePrompt
	})
	if r.agent.Parkable() {
		t.Error("parkable while a turn runs")
	}
	r.send("p2", "second")
	if r.agent.Parkable() {
		t.Error("parkable while an input waits")
	}
	if snapshot := r.agent.Snapshot(); snapshot.State != agent.StateRunning || len(snapshot.Queued) != 1 || snapshot.Queued[0].MessageID != "p2" {
		t.Errorf("snapshot during the turn: %+v", snapshot)
	}
	close(release)
	r.until(func(u agent.Update) bool {
		describe(u)
		return u.Type == agent.UpdateState && u.State == agent.StateIdle && u.MessageID == "p2"
	})
	want := []string{"prompt_queued p1 ", "state p1 running", "prompt p1 ", "prompt_queued p2 ", "result p1 ", "prompt p2 ", "result p2 ", "state p2 idle"}
	if !slices.Equal(kinds, want) {
		t.Errorf("updates %q, not %q", kinds, want)
	}
	if got := roles(r.request().body); !slices.Equal(got, []string{"user", "assistant", "user"}) {
		t.Errorf("the second request's messages %q", got)
	}
	if snapshot := r.agent.Snapshot(); snapshot.State != agent.StateIdle || len(snapshot.Queued) != 0 {
		t.Errorf("snapshot after the turns: %+v", snapshot)
	}
	if !r.agent.Parkable() {
		t.Error("not parkable once idle")
	}
}

func TestConfigure(t *testing.T) {
	release := make(chan struct{})
	r := start(t, agent.Settings{Model: model, Effort: "low"}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
		<-release
		sse(w, "ok")
	})
	r.send("p1", "hi")
	other := "other/model"
	high := "high"
	var got error
	r.agent.Configure(agent.SettingsPatch{Model: &other}, func(err error) { got = err })
	if !errors.Is(got, agent.ErrBusy) {
		t.Errorf("Configure while running: %v", got)
	}
	close(release)
	r.idle()
	r.request()

	low := "low"
	r.agent.Configure(agent.SettingsPatch{Effort: &low}, func(err error) { got = err })
	if got != nil || len(r.configured) != 0 {
		t.Errorf("Configure without a change: %v, OnConfigured got %+v", got, r.configured)
	}
	got = errors.New("not called")
	r.agent.Configure(agent.SettingsPatch{Model: &other, Effort: &high}, func(err error) { got = err })
	if got != nil {
		t.Fatalf("Configure while idle: %v", got)
	}
	want := agent.Settings{Model: other, Effort: high}
	if !slices.Equal(r.configured, []agent.Settings{want}) {
		t.Errorf("OnConfigured got %+v, not %+v", r.configured, want)
	}
	r.send("p2", "again")
	r.idle()
	body := r.request().body
	if body["model"] != other || body["reasoning"].(map[string]any)["effort"] != high {
		t.Errorf("the next request did not use the new settings: %v", body)
	}
}

// Ask posts one request without tools, with the instructions as the system
// message, and returns the whole streamed reply.
func TestAsk(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, _ := io.ReadAll(req.Body)
		var body map[string]any
		json.Unmarshal(data, &body)
		requests <- body
		if body["model"] == "unknown" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"code":400,"message":"model not found"}}`)
			return
		}
		sse(w, "A ", "title")
	}))
	t.Cleanup(server.Close)
	h := urHarness(t, server.URL, t.TempDir(), map[string][]string{model: {"a/x", "b/y"}})

	reply, err := h.Ask(t.Context(), model, "low", "Write a title.", "the prompt")
	if err != nil || reply != "A title" {
		t.Fatalf("Ask = %q, %v", reply, err)
	}
	body := <-requests
	if body["model"] != model {
		t.Errorf("model %v", body["model"])
	}
	if _, ok := body["tools"]; ok {
		t.Errorf("tools sent: %v", body["tools"])
	}
	want := []any{
		map[string]any{"role": "system", "content": "Write a title."},
		map[string]any{"role": "user", "content": "the prompt"},
	}
	if !reflect.DeepEqual(body["messages"], want) {
		t.Errorf("messages %v", body["messages"])
	}
	if !reflect.DeepEqual(body["reasoning"], map[string]any{"effort": "low"}) {
		t.Errorf("reasoning %v", body["reasoning"])
	}
	if !reflect.DeepEqual(body["provider"], map[string]any{"order": []any{"a/x", "b/y"}, "allow_fallbacks": false}) {
		t.Errorf("provider %v", body["provider"])
	}

	if _, err := h.Ask(t.Context(), "unknown", "", "i", "t"); err == nil || err.Error() != "OpenRouter 400 Bad Request: model not found" {
		t.Errorf("refused Ask: %v", err)
	}
	<-requests

	t.Setenv("OPENROUTER_API_KEY", "")
	if _, err := h.Ask(t.Context(), model, "", "i", "t"); err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Errorf("Ask without a key: %v", err)
	}
}

func TestStartWithoutKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	_, err := urHarness(t, "http://unused", t.TempDir(), nil).Start(agent.Options{})
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("Start without a key: %v", err)
	}
}

// Stop ends the running turn and drops waiting inputs. Its reply has already
// been saved as far as it streamed.
func TestStopDuringTurn(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, req *http.Request, n int) {
		if n > 0 {
			sse(w, "ok")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	})
	r.send("p1", "hi")
	r.request()
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta })
	r.send("p2", "waiting")
	r.updates = nil
	r.agent.Stop()
	if !r.agent.Exited() {
		t.Error("Exited is false after Stop")
	}
	var got []string
	for _, u := range r.updates {
		got = append(got, u.Type+" "+u.MessageID+" "+u.Error)
	}
	want := []string{"prompt_cancelled p2 ", "exited p1 stopped"}
	if !slices.Equal(got, want) {
		t.Errorf("Stop published %q, not %q", got, want)
	}
	if err := r.launch(r.tokens[0]); err != nil {
		t.Fatal(err)
	}
	r.send("p3", "next")
	r.idle()
	if got := contents(r.request().body); !slices.Equal(got, []string{"hi", "partial", "next"}) {
		t.Errorf("the resumed request's messages %q", got)
	}
}

// Stop after ur reports a turn idle, and before it reports taking the queued
// input that starts the next, still ends a running turn.
func TestStopBetweenTurns(t *testing.T) {
	release := make(chan struct{})
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, req *http.Request, n int) {
		if n > 0 {
			<-req.Context().Done()
			return
		}
		<-release
		sse(w, "one")
	})
	r.send("p1", "hi")
	r.request()
	r.send("p2", "next")
	close(release)
	r.result()
	r.agent.Handle(<-r.agent.Events())
	r.updates = nil
	r.agent.Stop()
	var got []string
	for _, u := range r.updates {
		got = append(got, u.Type+" "+u.MessageID+" "+u.Error)
	}
	want := []string{"prompt_cancelled p2 ", "exited p2 stopped"}
	if !slices.Equal(got, want) {
		t.Errorf("Stop published %q, not %q", got, want)
	}
}

// A second agent sees the streamed reply while the first request is still open.
func TestResumeDuringTurn(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, req *http.Request, n int) {
		if n > 0 {
			sse(w, "ok")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"remember\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" blue\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	})
	r.send("p1", "hi")
	r.request()
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta && u.Text == " blue" })

	if err := r.launch(r.tokens[0]); err != nil {
		t.Fatal(err)
	}
	r.send("p2", "next")
	r.idle()
	body := r.request().body
	if got := contents(body); !slices.Equal(got, []string{"hi", "remember blue", "next"}) {
		t.Errorf("the resumed request's messages %q", got)
	}
	if got := roles(body); !slices.Equal(got, []string{"user", "assistant", "user"}) {
		t.Errorf("the resumed request's roles %q", got)
	}
}

// A conversation survives a new agent, which sends each reply as one message.
func TestResume(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		sse(w, [][]string{{"o", "ne"}, {"two"}, {"three"}}[n]...)
	})
	r.send("p1", "hi")
	r.idle()
	if len(r.tokens) != 1 || r.tokens[0] == "" {
		t.Fatalf("OnResumable got %q in the first turn", r.tokens)
	}
	r.request()
	r.send("p2", "again")
	r.idle()
	r.request()
	r.agent.Stop()

	if err := r.launch(r.tokens[0]); err != nil {
		t.Fatal(err)
	}
	r.send("p3", "third")
	r.idle()
	body := r.request().body
	if got := contents(body); !slices.Equal(got, []string{"hi", "one", "again", "two", "third"}) {
		t.Errorf("the resumed request's messages %q", got)
	}
	if got := roles(body); !slices.Equal(got, []string{"user", "assistant", "user", "assistant", "user"}) {
		t.Errorf("the resumed request's roles %q", got)
	}
	if len(r.tokens) != 1 {
		t.Errorf("OnResumable got %q, not the one token", r.tokens)
	}
	if got := systemMessages(body); len(got) != 1 || body["messages"].([]any)[0].(map[string]any)["role"] != "system" {
		t.Errorf("the resumed request has system messages %q, not one first", got)
	}
}

// A prompt's images reach the model as image_url parts before its text, in
// every later request of the conversation and after it resumes.
func TestImages(t *testing.T) {
	r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
		sse(w, "seen")
	})
	png := agent.Image{ID: "i1", MimeType: "image/png", Data: []byte("png bytes")}
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png.Data)
	imagePart := map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
	userParts := func(body map[string]any) [][]any {
		var parts [][]any
		for _, m := range messages(body) {
			if m["role"] == "user" {
				p, _ := m["content"].([]any)
				parts = append(parts, p)
			}
		}
		return parts
	}
	withText := []any{imagePart, map[string]any{"type": "text", "text": "what is this"}}
	imageOnly := []any{imagePart}

	if err := r.agent.Send(agent.Input{MessageID: "p1", Text: "what is this", Images: []agent.Image{png}, Content: "what is this"}); err != nil {
		t.Fatal(err)
	}
	r.idle()
	if got := userParts(r.request().body); !reflect.DeepEqual(got, [][]any{withText}) {
		t.Errorf("the prompt with an image posted %v", got)
	}

	if err := r.agent.Send(agent.Input{MessageID: "p2", Images: []agent.Image{png}}); err != nil {
		t.Fatal(err)
	}
	r.idle()
	if got := userParts(r.request().body); !reflect.DeepEqual(got, [][]any{withText, imageOnly}) {
		t.Errorf("the next turn, an image alone, posted %v", got)
	}
	r.agent.Stop()

	if err := r.launch(r.tokens[0]); err != nil {
		t.Fatal(err)
	}
	r.send("p3", "and now")
	r.idle()
	if got := userParts(r.request().body); !reflect.DeepEqual(got, [][]any{withText, imageOnly, nil}) {
		t.Errorf("the resumed conversation posted %v", got)
	}
}

// systemMessages is the content of each system message in a request.
func systemMessages(body map[string]any) []string {
	var values []string
	for _, m := range body["messages"].([]any) {
		if m := m.(map[string]any); m["role"] == "system" {
			values = append(values, m["content"].(string))
		}
	}
	return values
}

// Each request starts with a system message built from the workspace, the
// style, and the workspace's AGENTS.md as it stands then.
func TestInstructions(t *testing.T) {
	answer := func(w http.ResponseWriter, _ *http.Request, _ int) { sse(w, "ok") }
	// system sends a prompt and returns the request's one system message,
	// which must lead, and the request.
	system := func(t *testing.T, r *run) (string, map[string]any) {
		t.Helper()
		r.send("p", "hi")
		r.idle()
		body := r.request().body
		got := systemMessages(body)
		if len(got) != 1 || body["messages"].([]any)[0].(map[string]any)["role"] != "system" {
			t.Fatalf("system messages %q, not one first", got)
		}
		return got[0], body
	}

	t.Run("fresh each request", func(t *testing.T) {
		r := start(t, agent.Settings{Model: model}, nil, answer)
		path := filepath.Join(r.workspace, "AGENTS.md")
		if err := os.WriteFile(path, []byte("marker-one"), 0o600); err != nil {
			t.Fatal(err)
		}
		first, body := system(t, r)
		for _, want := range []string{r.workspace, agent.Style, "marker-one", "Date: " + time.Now().Format("2006-01-02")} {
			if !strings.Contains(first, want) {
				t.Errorf("system message lacks %q:\n%s", want, first)
			}
		}
		// Without Ox's MCP server, Ur has only shell and is told of no
		// other sessions.
		if got := toolNames(body); !slices.Equal(got, []string{"shell"}) {
			t.Errorf("tools %q, not just shell", got)
		}
		if strings.Contains(first, "Ox session") || strings.Contains(first, agent.Sessions) {
			t.Errorf("system message tells of other sessions:\n%s", first)
		}
		if err := os.WriteFile(path, []byte("marker-two"), 0o600); err != nil {
			t.Fatal(err)
		}
		second, _ := system(t, r)
		if !strings.Contains(second, "marker-two") || strings.Contains(second, "marker-one") {
			t.Errorf("system message after the edit:\n%s", second)
		}
	})

	t.Run("no AGENTS.md", func(t *testing.T) {
		r := start(t, agent.Settings{Model: model}, nil, answer)
		got, _ := system(t, r)
		if !strings.Contains(got, r.workspace) || strings.Contains(got, "Instructions from the workspace's") {
			t.Errorf("system message:\n%s", got)
		}
	})

	t.Run("unreadable AGENTS.md", func(t *testing.T) {
		r := start(t, agent.Settings{Model: model}, nil, answer)
		if err := os.Mkdir(filepath.Join(r.workspace, "AGENTS.md"), 0o700); err != nil {
			t.Fatal(err)
		}
		r.send("p", "hi")
		if result := r.result(); !result.IsError || !strings.HasPrefix(result.Text, "Ur could not read AGENTS.md: ") {
			t.Errorf("result %+v", result)
		}
		r.idle()
		select {
		case req := <-r.requests:
			t.Errorf("the server received %v", req.body)
		default:
		}
	})
}

// A conversation whose file is gone or damaged drops the prompt that would
// resume it with the lost conversation notice, and clears the token.
func TestResumeLost(t *testing.T) {
	for name, content := range map[string]*string{
		"missing file":        nil,
		"truncated last line": new("{\"role\":\"user\",\"content\":\"hi\"}\n{\"role\":\"assis"),
	} {
		t.Run(name, func(t *testing.T) {
			r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
				sse(w, "ok")
			})
			if content != nil {
				os.MkdirAll(r.dir, 0o700)
				if err := os.WriteFile(filepath.Join(r.dir, "lost.jsonl"), []byte(*content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.launch("lost"); err != nil {
				t.Fatal(err)
			}
			r.send("p1", "hi")
			var seen []agent.Update
			exited := r.until(func(u agent.Update) bool {
				seen = append(seen, u)
				return u.Type == agent.UpdateExited
			})
			want := []string{"prompt_queued p1", "state p1 running", "prompt_cancelled p1", "exited p1"}
			if got := kinds(seen); !slices.Equal(got, want) {
				t.Errorf("updates %q, not %q", got, want)
			}
			if exited.Error != lostConversation {
				t.Errorf("exited with %q, not the lost conversation", exited.Error)
			}
			if !r.agent.Exited() {
				t.Error("the agent has not ended")
			}
			if !slices.Equal(r.tokens, []string{""}) {
				t.Errorf("OnResumable got %q, not the empty token", r.tokens)
			}
		})
	}
}

// An unsaved prompt never reaches the model, and an unsaved reply delta is
// never published.
func TestSaveFailure(t *testing.T) {
	t.Run("prompt", func(t *testing.T) {
		r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
			sse(w, "ok")
		})
		if err := os.WriteFile(r.dir, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		r.send("p1", "hi")
		if result := r.result(); !result.IsError || !strings.HasPrefix(result.Text, "Ur could not save the prompt: ") {
			t.Errorf("result %+v", result)
		}
		r.idle()
		os.Remove(r.dir)
		r.send("p2", "again")
		r.idle()
		if got := contents(r.request().body); !slices.Equal(got, []string{"again"}) {
			t.Errorf("the first request's messages %q", got)
		}
	})

	t.Run("reply", func(t *testing.T) {
		release := make(chan struct{})
		r := start(t, agent.Settings{Model: model}, nil, func(w http.ResponseWriter, req *http.Request, n int) {
			if n > 0 {
				sse(w, "ok")
				return
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"one\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-release:
				sse(w, "two")
			case <-req.Context().Done():
			}
		})
		r.send("p1", "hi")
		r.request()
		r.until(func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta && u.Text == "one" })
		if err := os.Chmod(r.file(), 0o400); err != nil {
			t.Fatal(err)
		}
		close(release)
		var result agent.Result
		r.until(func(u agent.Update) bool {
			if u.Type == agent.UpdateTextDelta && u.Text == "two" {
				t.Error("published an unsaved reply delta")
			}
			if u.Type == agent.UpdateResult {
				result = *u.Result
			}
			return u.Type == agent.UpdateState && u.State == agent.StateIdle
		})
		if slices.ContainsFunc(r.updates, func(u agent.Update) bool { return u.Type == agent.UpdateTextDelta }) {
			t.Error("published a reply delta after going idle")
		}
		prefix := "Ur could not save the reply: "
		if !result.IsError || !strings.HasPrefix(result.Text, prefix) || !strings.Contains(result.Text, "permission denied") {
			t.Errorf("result %+v, expected an error starting %q that names the denied write", result, prefix)
		}
		if err := os.Chmod(r.file(), 0o600); err != nil {
			t.Fatal(err)
		}
		r.send("p2", "again")
		r.idle()
		if got := contents(r.request().body); !slices.Equal(got, []string{"hi", "one", "again"}) {
			t.Errorf("the next request's messages %q", got)
		}
	})

	// A command that ran is shown in the transcript even when its outcome
	// cannot be saved.
	t.Run("command result", func(t *testing.T) {
		r := start(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
			sseCalls(w, "", call{"call_1", "shell", `{"command":"while [ ! -e go ]; do sleep 0.05; done"}`})
		})
		r.send("p1", "go")
		r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolUse })
		if err := os.Chmod(r.file(), 0o400); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.workspace, "go"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		result := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolResult })
		if result.ToolUseID != "call_1" || result.Result.IsError || !strings.HasPrefix(result.Result.Text, "Exit code: 0") {
			t.Errorf("tool_result %+v %+v", result, result.Result)
		}
		prefix := "Ur could not save the command's result: "
		if final := r.result(); !final.IsError || !strings.HasPrefix(final.Text, prefix) {
			t.Errorf("result %+v, expected an error starting %q", final, prefix)
		}
	})
}

// call is a tool call a canned response makes.
type call struct {
	id, name, arguments string
}

// sseCalls writes a streamed completion of text and calls in the shape
// OpenRouter streams them: each call's first fragment carries its ID and name,
// later fragments carry its arguments keyed by index, and the finish chunk
// arrives twice.
func sseCalls(w http.ResponseWriter, text string, calls ...call) {
	fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n")
	write := func(delta map[string]any, finish any) {
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	if text != "" {
		write(map[string]any{"content": text}, nil)
	}
	for i, c := range calls {
		write(map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": c.id, "type": "function", "function": map[string]any{"name": c.name, "arguments": ""},
		}}}, nil)
	}
	// The arguments arrive in two halves, the calls' fragments interleaved.
	for half := range 2 {
		for i, c := range calls {
			mid := len(c.arguments) / 2
			part := []string{c.arguments[:mid], c.arguments[mid:]}[half]
			write(map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": part}}}}, nil)
		}
	}
	write(map[string]any{"content": ""}, "tool_calls")
	write(map[string]any{"content": ""}, "tool_calls")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// kinds describes updates by type, message ID, and state.
func kinds(updates []agent.Update) []string {
	var described []string
	for _, u := range updates {
		described = append(described, strings.TrimSpace(u.Type+" "+u.MessageID+" "+u.State))
	}
	return described
}

// messages is a request's conversation messages, without the system message.
func messages(body map[string]any) []map[string]any {
	var ms []map[string]any
	for _, m := range body["messages"].([]any) {
		if m.(map[string]any)["role"] != "system" {
			ms = append(ms, m.(map[string]any))
		}
	}
	return ms
}

// waitFile waits for the command to write file.
func waitFile(t *testing.T, file string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(file); err == nil && strings.HasSuffix(string(data), "\n") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never written", file)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestShellApproved(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "manual"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			sseCalls(w, "Running it.", call{"call_1", "shell", `{"command":"echo hi"}`})
			return
		}
		sse(w, "done")
	})
	// The card shows the parsed arguments, with the default timeout filled in.
	const shown = `{"command":"echo hi","timeout_seconds":120}`
	r.send("p1", "say hi")
	var seen []agent.Update
	approval := r.until(func(u agent.Update) bool {
		if u.Type != agent.UpdateTextDelta {
			seen = append(seen, u)
		}
		return u.Type == agent.UpdateApproval
	})
	want := []string{"prompt_queued p1", "state p1 running", "prompt p1", "tool_use p1", "state p1 requires_action", "approval p1"}
	if got := kinds(seen); !slices.Equal(got, want) {
		t.Errorf("updates %q, not %q", got, want)
	}
	if use := seen[3]; use.ToolUseID != "call_1" || use.ToolName != "shell" || string(use.Input) != shown {
		t.Errorf("tool_use %+v", use)
	}
	var request struct {
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	}
	if err := json.Unmarshal(approval.Request, &request); err != nil || request.ToolName != "shell" ||
		string(request.Input) != shown || request.ToolUseID != "call_1" {
		t.Errorf("approval request %s", approval.Request)
	}
	snapshot := r.agent.Snapshot()
	if snapshot.State != agent.StateRequiresAction || len(snapshot.Approvals) != 1 ||
		snapshot.Approvals[0].RequestID != approval.RequestID ||
		!bytes.Equal(snapshot.Approvals[0].Request, approval.Request) {
		t.Errorf("snapshot while asking: %+v", snapshot)
	}
	if r.agent.Parkable() {
		t.Error("parkable while asking")
	}
	var configured error
	effort := "high"
	r.agent.Configure(agent.SettingsPatch{Effort: &effort}, func(err error) { configured = err })
	if !errors.Is(configured, agent.ErrBusy) {
		t.Errorf("Configure while asking: %v", configured)
	}
	if err := r.agent.Approve(approval.RequestID, json.RawMessage(`{"behavior":"maybe"}`)); err == nil || errors.Is(err, agent.ErrApprovalNotFound) {
		t.Errorf("Approve with a malformed response: %v", err)
	}
	if err := r.agent.Approve("other", json.RawMessage(`{"behavior":"allow"}`)); !errors.Is(err, agent.ErrApprovalNotFound) {
		t.Errorf("Approve with another ID: %v", err)
	}

	r.updates = nil
	if err := r.agent.Approve(approval.RequestID, json.RawMessage(`{"behavior":"allow","updatedInput":{"command":"echo hi"}}`)); err != nil {
		t.Fatal(err)
	}
	if got := kinds(r.updates); !slices.Equal(got, []string{"approval_answered p1"}) {
		t.Errorf("Approve published %q", got)
	}
	if err := r.agent.Approve(approval.RequestID, json.RawMessage(`{"behavior":"allow"}`)); !errors.Is(err, agent.ErrApprovalNotFound) {
		t.Errorf("a second Approve: %v", err)
	}
	result := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolResult })
	if result.ToolUseID != "call_1" || result.Result.IsError || result.Result.Text != "Exit code: 0\n\nhi\n" {
		t.Errorf("tool_result %+v %+v", result, result.Result)
	}
	if final := r.result(); final.IsError || final.Text != "done" {
		t.Errorf("result %+v", final)
	}

	r.request()
	second := r.request().body
	if got := roles(second); !slices.Equal(got, []string{"user", "assistant", "tool"}) {
		t.Fatalf("the second request's roles %q", got)
	}
	ms := messages(second)
	calls := ms[1]["tool_calls"].([]any)
	first := calls[0].(map[string]any)
	if len(calls) != 1 || first["id"] != "call_1" || first["function"].(map[string]any)["arguments"] != `{"command":"echo hi"}` || ms[1]["content"] != "Running it." {
		t.Errorf("assistant message %v", ms[1])
	}
	if ms[2]["tool_call_id"] != "call_1" || ms[2]["content"] != "Exit code: 0\n\nhi\n" {
		t.Errorf("tool message %v", ms[2])
	}
	if tools := second["tools"].([]any); len(tools) != 1 || tools[0].(map[string]any)["function"].(map[string]any)["name"] != "shell" {
		t.Errorf("tools %v", second["tools"])
	}
}

func TestShellDenied(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "manual"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			sseCalls(w, "", call{"call_1", "shell", `{"command":"touch ran"}`})
			return
		}
		sse(w, "ok")
	})
	r.send("p1", "touch it")
	approval := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateApproval })
	if err := r.agent.Approve(approval.RequestID, json.RawMessage(`{"behavior":"deny","message":"not now"}`)); err != nil {
		t.Fatal(err)
	}
	denied := "The user denied this command: not now"
	if result := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolResult }).Result; !result.IsError || result.Text != denied {
		t.Errorf("tool_result %+v", result)
	}
	if result := r.result(); result.IsError || result.Text != "ok" {
		t.Errorf("result %+v", result)
	}
	r.request()
	if got := contents(r.request().body); !slices.Equal(got, []string{"touch it", denied}) {
		t.Errorf("the second request's contents %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.workspace, "ran")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a denied command ran: %v", err)
	}
}

func TestShellAuto(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			sseReasoning(w, reasoningText("List "), reasoningText("both."))
			sseCalls(w, "", call{"call_1", "shell", `{"command":"echo one"}`}, call{"call_2", "shell", `{"command":"echo two"}`})
			return
		}
		if n == 1 {
			sseCalls(w, "", call{"call_3", "shell", `{"command":"echo three"}`})
			return
		}
		sse(w, "ok")
	})
	r.send("p1", "count")
	var results []string
	for _, u := range r.turn() {
		if u.Type == agent.UpdateApproval || u.Type == agent.UpdateState && u.State == agent.StateRequiresAction {
			t.Errorf("auto mode published %s", u.Type)
		}
		if u.Type == agent.UpdateToolResult {
			results = append(results, u.ToolUseID+" "+u.Result.Text)
		}
	}
	if want := []string{"call_1 Exit code: 0\n\none\n", "call_2 Exit code: 0\n\ntwo\n", "call_3 Exit code: 0\n\nthree\n"}; !slices.Equal(results, want) {
		t.Errorf("tool results %q, not %q", results, want)
	}
	r.request()
	second := r.request().body
	if got := roles(second); !slices.Equal(got, []string{"user", "assistant", "tool", "tool"}) {
		t.Fatalf("the second request's roles %q", got)
	}
	ms := messages(second)
	if ms[2]["tool_call_id"] != "call_1" || ms[2]["content"] != "Exit code: 0\n\none\n" ||
		ms[3]["tool_call_id"] != "call_2" || ms[3]["content"] != "Exit code: 0\n\ntwo\n" {
		t.Errorf("tool messages %v %v", ms[2], ms[3])
	}
	const listBoth = `[{"format":"unknown","index":0,"text":"List both.","type":"reasoning.text"}]`
	details, _ := json.Marshal(ms[1]["reasoning_details"])
	if string(details) != listBoth {
		t.Errorf("the calling message's reasoning %s, not %s", details, listBoth)
	}
	// Building a request leaves the saved fragments as they were.
	third := r.request().body
	details, _ = json.Marshal(messages(third)[1]["reasoning_details"])
	if string(details) != listBoth {
		t.Errorf("the third request's reasoning %s, not %s", details, listBoth)
	}
}

// A command starts with the default signal handling: a writer into a closed
// pipe dies of SIGPIPE, and SIGINT stops a child.
func TestShellSignals(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			sseCalls(w, "", call{"call_1", "shell", `{"command":"yes | head -1 >/dev/null; echo \"pipe=${PIPESTATUS[0]}\"; sleep 2 & kill -INT $!; wait $!; echo \"int=$?\""}`})
			return
		}
		sse(w, "ok")
	})
	r.send("p1", "go")
	result := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolResult }).Result
	if result.Text != "Exit code: 0\n\npipe=141\nint=130\n" {
		t.Errorf("the command ran with signals ignored: %q", result.Text)
	}
}

// Calls with bad arguments or to an unknown tool are answered at once, without
// asking, and the turn goes on.
func TestShellInvalidCall(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "manual"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			sseCalls(w, "",
				call{"call_1", "shell", `{"command":"touch wrong","cwd":"/"}`},
				call{"call_2", "shell", `touch wrong`},
				call{"call_3", "python", `{"code":"1"}`},
				call{"call_4", "shell", `{"command":"touch wrong"} extra`})
			return
		}
		sse(w, "ok")
	})
	r.send("p1", "go")
	var uses, results []string
	for _, u := range r.turn() {
		switch u.Type {
		case agent.UpdateApproval:
			t.Error("an invalid call asked for approval")
		case agent.UpdateToolUse:
			uses = append(uses, u.ToolUseID+" "+u.ToolName+" "+string(u.Input))
		case agent.UpdateToolResult:
			if !u.Result.IsError {
				t.Errorf("%s answered without an error: %q", u.ToolUseID, u.Result.Text)
			}
			results = append(results, u.Result.Text)
		case agent.UpdateResult:
			if u.Result.IsError || u.Result.Text != "ok" {
				t.Errorf("result %+v", u.Result)
			}
		}
	}
	wantUses := []string{`call_1 shell {"command":"touch wrong","cwd":"/"}`, "call_2 shell {}", `call_3 python {"code":"1"}`, "call_4 shell {}"}
	if !slices.Equal(uses, wantUses) {
		t.Errorf("tool uses %q, not %q", uses, wantUses)
	}
	if len(results) != 4 || results[0] != `arguments: json: unknown field "cwd"` ||
		!strings.HasPrefix(results[1], "arguments: ") || results[2] != `Ur has no tool "python"` || !strings.HasPrefix(results[3], "arguments: ") {
		t.Errorf("tool results %q", results)
	}
	r.request()
	if got := roles(r.request().body); !slices.Equal(got, []string{"user", "assistant", "tool", "tool", "tool", "tool"}) {
		t.Errorf("the second request's roles %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.workspace, "wrong")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an invalid call ran: %v", err)
	}
}

// background is a command that leaves a child, whose PID it writes to the
// file child, and runs until stopped.
const background = `{"command":"sleep 60 & echo $! > child; sleep 60"}`

func TestShellInterrupt(t *testing.T) {
	t.Run("asking", func(t *testing.T) {
		r := start(t, agent.Settings{Model: model, PermissionMode: "manual"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
			if n == 0 {
				sseCalls(w, "", call{"call_1", "shell", `{"command":"touch ran"}`})
				return
			}
			sse(w, "ok")
		})
		r.send("p1", "go")
		approval := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateApproval })
		r.send("p2", "waiting")
		r.until(func(u agent.Update) bool { return u.Type == agent.UpdatePromptQueued })
		r.updates = nil
		interrupt(t, r)
		want := []string{"prompt_cancelled p2", "approval_cancelled p1", "result p1", "state p1 idle"}
		if got := kinds(r.updates); !slices.Equal(got, want) {
			t.Fatalf("interrupt published %q, not %q", got, want)
		}
		if u := r.updates[1]; u.RequestID != approval.RequestID {
			t.Errorf("approval_cancelled %+v", u)
		}
		if result := r.updates[2].Result; !result.IsError || result.Text != "Interrupted" {
			t.Errorf("result %+v", result)
		}
		if snapshot := r.agent.Snapshot(); len(snapshot.Approvals) != 0 {
			t.Errorf("approvals after the interrupt: %+v", snapshot.Approvals)
		}
		if err := r.agent.Approve(approval.RequestID, json.RawMessage(`{"behavior":"allow"}`)); !errors.Is(err, agent.ErrApprovalNotFound) {
			t.Errorf("Approve after the interrupt: %v", err)
		}
		r.updates = nil
		r.request()
		r.send("p3", "next")
		r.idle()
		body := r.request().body
		if got := roles(body); !slices.Equal(got, []string{"user", "assistant", "tool", "user"}) {
			t.Fatalf("the next request's roles %q", got)
		}
		if got := messages(body)[2]; got["tool_call_id"] != "call_1" || got["content"] != stopped {
			t.Errorf("the stopped call's answer %v", got)
		}
	})

	t.Run("executing", func(t *testing.T) {
		release := make(chan struct{})
		r := start(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
			if n == 0 {
				sseCalls(w, "", call{"call_1", "shell", background})
				return
			}
			<-release
			sse(w, "ok")
		})
		r.send("p1", "go")
		r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolUse })
		waitFile(t, filepath.Join(r.workspace, "child"))
		r.updates = nil
		interrupt(t, r)
		if got := kinds(r.updates); !slices.Equal(got, []string{"result p1", "state p1 idle"}) {
			t.Fatalf("interrupt published %q", got)
		}
		gone(t, filepath.Join(r.workspace, "child"))
		r.request()
		// The killed command's outcome is dropped.
		r.send("p2", "next")
		r.updates = nil
		close(release)
		for _, u := range r.turn() {
			if u.Type == agent.UpdateToolResult {
				t.Errorf("the next turn published a tool_result %+v", u.Result)
			}
		}
		body := r.request().body
		if got := roles(body); !slices.Equal(got, []string{"user", "assistant", "tool", "user"}) {
			t.Fatalf("the next request's roles %q", got)
		}
		if got := messages(body)[2]["content"]; got != stopped {
			t.Errorf("the stopped call's answer %v", got)
		}
	})
}

func TestShellStop(t *testing.T) {
	t.Run("executing", func(t *testing.T) {
		r := start(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
			if n == 0 {
				sseCalls(w, "", call{"call_1", "shell", background})
				return
			}
			sse(w, "ok")
		})
		r.send("p1", "go")
		r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolUse })
		waitFile(t, filepath.Join(r.workspace, "child"))
		// children.WaitAll, which oxd calls as it stops, waits for the command.
		waited := make(chan struct{})
		go func() {
			children.WaitAll()
			close(waited)
		}()
		select {
		case <-waited:
			t.Error("children.WaitAll returned while a command ran")
		case <-time.After(100 * time.Millisecond):
		}
		r.updates = nil
		r.agent.Stop()
		if got := kinds(r.updates); !slices.Equal(got, []string{"exited p1"}) {
			t.Errorf("Stop published %q", got)
		}
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
			t.Error("children.WaitAll did not return once Stop ended the command")
		}
		gone(t, filepath.Join(r.workspace, "child"))
		r.request()
		if err := r.launch(r.tokens[0]); err != nil {
			t.Fatal(err)
		}
		r.send("p2", "next")
		r.idle()
		body := r.request().body
		if got := roles(body); !slices.Equal(got, []string{"user", "assistant", "tool", "user"}) {
			t.Fatalf("the resumed request's roles %q", got)
		}
		if got := messages(body)[2]; got["tool_call_id"] != "call_1" || got["content"] != stopped {
			t.Errorf("the stopped call's answer %v", got)
		}
	})

	t.Run("asking", func(t *testing.T) {
		r := start(t, agent.Settings{Model: model, PermissionMode: "manual"}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
			sseCalls(w, "", call{"call_1", "shell", `{"command":"true"}`})
		})
		r.send("p1", "go")
		approval := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateApproval })
		r.updates = nil
		r.agent.Stop()
		if got := kinds(r.updates); !slices.Equal(got, []string{"approval_cancelled p1", "exited p1"}) {
			t.Errorf("Stop published %q", got)
		}
		if r.updates[0].RequestID != approval.RequestID {
			t.Errorf("approval_cancelled %+v", r.updates[0])
		}
	})
}

// Reaped stays open after an interrupt and a stop until the running
// command's wait has returned: here, until WaitDelay gives up on the output
// pipe a process outside the command's group holds.
func TestShellReaped(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			sseCalls(w, "", call{"call_1", "shell", `{"command":"set -m; sleep 30 & echo $! > escaped; set +m; sleep 60"}`})
			return
		}
		sse(w, "ok")
	})
	escaped := filepath.Join(r.workspace, "escaped")
	t.Cleanup(func() {
		if data, err := os.ReadFile(escaped); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	r.send("p1", "go")
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolUse })
	waitFile(t, escaped)
	interrupt(t, r)
	r.agent.Stop()
	reaped := r.agent.Reaped()
	select {
	case <-reaped:
		t.Fatal("Reaped closed while the command's wait was running")
	default:
	}
	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("Reaped stayed open after the command's wait returned")
	}
}

// A prompt sent while a call waits is taken after the call's result, before
// the next request.
func TestShellInputBetweenSteps(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "manual"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			sseReasoning(w, reasoningText("Say hi."))
			sseCalls(w, "", call{"call_1", "shell", `{"command":"echo hi"}`})
			return
		}
		sse(w, "ok")
	})
	r.send("p1", "go")
	approval := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateApproval })
	r.send("p2", "also this")
	if err := r.agent.Approve(approval.RequestID, json.RawMessage(`{"behavior":"allow"}`)); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, u := range r.turn() {
		if u.Type == agent.UpdateToolResult || u.Type == agent.UpdatePrompt || u.Type == agent.UpdateResult {
			order = append(order, u.Type+" "+u.MessageID)
		}
	}
	if want := []string{"tool_result p1", "prompt p2", "result p2"}; !slices.Equal(order, want) {
		t.Errorf("updates %q, not %q", order, want)
	}
	r.request()
	body := r.request().body
	if got := roles(body); !slices.Equal(got, []string{"user", "assistant", "tool", "user"}) {
		t.Errorf("the second request's roles %q", got)
	}
	if got := contents(body); got[len(got)-1] != "also this" {
		t.Errorf("the second request's contents %q", got)
	}
	// The input taken mid-turn belongs to the turn, which keeps its reasoning.
	details, _ := json.Marshal(messages(body)[1]["reasoning_details"])
	if want := `[{"format":"unknown","index":0,"text":"Say hi.","type":"reasoning.text"}]`; string(details) != want {
		t.Errorf("the calling message's reasoning %s, not %s", details, want)
	}
	if r.agent.Snapshot().State != agent.StateIdle {
		t.Errorf("snapshot after the turn: %+v", r.agent.Snapshot())
	}
}

func TestPermissionModes(t *testing.T) {
	check := urHarness(t, "http://unused", t.TempDir(), nil).CheckPermissionMode
	for _, mode := range []string{"manual", "auto"} {
		if err := check(mode); err != nil {
			t.Errorf("%s refused: %v", mode, err)
		}
	}
	for _, mode := range []string{"acceptEdits", "plan"} {
		if err := check(mode); err == nil {
			t.Errorf("%s accepted", mode)
		}
	}
}

// stopped is ur's answer to a call whose command did not finish.
const stopped = "The user stopped this command before it finished."

// gone reports whether the process whose PID the file holds has ended.
func gone(t *testing.T, file string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	// A killed child is reaped by init once its parent shell is gone; give it
	// a moment.
	deadline := time.Now().Add(2 * time.Second)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("background child %d still runs", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// KillAll, which oxd runs on a second stop signal, ends ur, which first ends
// its command's whole process group. KillAll refuses later processes for the
// rest of the process, so the test runs in its own.
func TestKillAllEndsCommands(t *testing.T) {
	if os.Getenv("OX_KILLALL_INNER") == "" {
		inner := exec.Command(os.Args[0], "-test.run=^TestKillAllEndsCommands$", "-test.v")
		inner.Env = append(os.Environ(), "OX_KILLALL_INNER=1")
		out, err := inner.CombinedOutput()
		if err != nil {
			t.Fatalf("inner run: %v\n%s", err, out)
		}
		return
	}
	r := start(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, func(w http.ResponseWriter, _ *http.Request, n int) {
		sseCalls(w, "", call{"call_1", "shell", background})
	})
	r.send("p1", "go")
	r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolUse })
	child := filepath.Join(r.workspace, "child")
	waitFile(t, child)
	children.KillAll()
	select {
	case <-r.agent.Reaped():
	case <-time.After(3 * time.Second):
		t.Fatal("KillAll left ur running")
	}
	gone(t, child)
}

// A ur that exits on its own, or breaks the protocol, ends the agent with the
// reason, dropping the prompt it had not taken.
func TestUnexpectedExit(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	for name, c := range map[string]struct{ script, want string }{
		"exit": {
			"read line\necho 'ur: something broke' >&2\nexit 3\n",
			"Ur exited: exit status 3\nur: something broke",
		},
		"broken protocol": {
			"read line\necho '{\"type\":\"taken\",\"message_id\":\"other\"}'\nexec sleep 60\n",
			`Ur broke its protocol: taken "other" is not the oldest untaken input`,
		},
		"command without a group": {
			"read line\necho '{\"type\":\"command_started\"}'\nexec sleep 60\n",
			"Ur broke its protocol: command_started for group 0",
		},
		"end of a command that did not start": {
			"read line\necho '{\"type\":\"command_ended\",\"group\":5}'\nexec sleep 60\n",
			"Ur broke its protocol: command_ended for group 5, which did not start",
		},
	} {
		t.Run(name, func(t *testing.T) {
			command := filepath.Join(t.TempDir(), "ur")
			if err := os.WriteFile(command, []byte("#!/bin/sh\n"+c.script), 0o755); err != nil {
				t.Fatal(err)
			}
			r := &run{t: t, harness: Harness(command, "", t.TempDir(), nil), workspace: t.TempDir(), settings: agent.Settings{Model: model}}
			if err := r.launch(""); err != nil {
				t.Fatal(err)
			}
			r.send("p1", "hi")
			var seen []agent.Update
			exited := r.until(func(u agent.Update) bool {
				seen = append(seen, u)
				return u.Type == agent.UpdateExited
			})
			want := []string{"prompt_queued p1", "state p1 running", "prompt_cancelled p1", "exited p1"}
			if got := kinds(seen); !slices.Equal(got, want) {
				t.Errorf("updates %q, not %q", got, want)
			}
			if exited.Error != c.want {
				t.Errorf("exited with %q, not %q", exited.Error, c.want)
			}
			if !r.agent.Exited() {
				t.Error("the agent has not ended")
			}
		})
	}
}

// An input written to a ur that has gone is refused with an error that names
// Ur.
func TestSendToGoneUr(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	command := filepath.Join(t.TempDir(), "ur")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &run{t: t, harness: Harness(command, "", t.TempDir(), nil), workspace: t.TempDir(), settings: agent.Settings{Model: model}}
	if err := r.launch(""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.agent.Reaped():
	case <-time.After(5 * time.Second):
		t.Fatal("ur was not reaped")
	}
	err := r.agent.Send(agent.Input{MessageID: "p1", Content: "hi"})
	if err == nil || !strings.HasPrefix(err.Error(), "send input to Ur: ") {
		t.Fatalf("Send to a ur that has gone returned %v", err)
	}
}

// A ur that dies without ending its command, as on SIGKILL, leaves no command
// running once it is reaped, whether the agent handles its exit or is stopped
// first.
func TestCrashEndsCommands(t *testing.T) {
	for name, stopFirst := range map[string]bool{"exit handled": false, "stopped before the exit": true} {
		t.Run(name, func(t *testing.T) {
			r := start(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
				sseCalls(w, "", call{"call_1", "shell", `{"command":"echo $PPID > ur.pid; echo $$ > pid; sleep 60"}`})
			})
			r.send("p1", "go")
			r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolUse })
			waitFile(t, filepath.Join(r.workspace, "pid"))
			data, err := os.ReadFile(filepath.Join(r.workspace, "ur.pid"))
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			if stopFirst {
				r.agent.Stop()
				select {
				case <-r.agent.Reaped():
				case <-time.After(5 * time.Second):
					t.Fatal("ur was not reaped")
				}
			} else {
				r.until(func(u agent.Update) bool { return u.Type == agent.UpdateExited })
			}
			gone(t, filepath.Join(r.workspace, "pid"))
		})
	}
}

// A relative conversation directory is oxd's, not the session's workspace.
func TestRelativeConversationDirectory(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sse(w, "ok") }))
	t.Cleanup(server.Close)
	cwd := t.TempDir()
	t.Chdir(cwd)
	r := &run{t: t, harness: urHarness(t, server.URL, "urdata", nil), workspace: t.TempDir(), settings: agent.Settings{Model: model}}
	if err := r.launch(""); err != nil {
		t.Fatal(err)
	}
	r.send("p1", "hi")
	r.idle()
	if _, err := os.Stat(filepath.Join(cwd, "urdata", r.tokens[0]+".jsonl")); err != nil {
		t.Errorf("the conversation is not under oxd's directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.workspace, "urdata")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the conversation went to the workspace: %v", err)
	}
}

// An approval answered while an interrupt is on its way to ur stays answered:
// ur's cancellation of it is not published.
func TestAnswerCrossesInterrupt(t *testing.T) {
	r := start(t, agent.Settings{Model: model, PermissionMode: "manual"}, nil, func(w http.ResponseWriter, _ *http.Request, _ int) {
		sseCalls(w, "", call{"call_1", "shell", `{"command":"true"}`})
	})
	r.send("p1", "go")
	approval := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateApproval })
	r.updates = nil
	answered := false
	r.agent.Interrupt(func(error) { answered = true })
	if err := r.agent.Approve(approval.RequestID, json.RawMessage(`{"behavior":"allow"}`)); err != nil {
		t.Fatal(err)
	}
	for timeout := time.After(5 * time.Second); !answered; {
		select {
		case e := <-r.agent.Events():
			r.agent.Handle(e)
		case <-timeout:
			t.Fatalf("Interrupt was not answered; got %+v", r.updates)
		}
	}
	if got := kinds(r.updates); !slices.Equal(got, []string{"approval_answered p1", "result p1", "state p1 idle"}) {
		t.Errorf("updates %q, not the answer and the interrupted turn's end", got)
	}
}

// toolNames is the names of a request's tools.
func toolNames(body map[string]any) []string {
	var names []string
	tools, _ := body["tools"].([]any)
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["function"].(map[string]any)["name"].(string))
	}
	return names
}

// standIn is Ox's MCP server as the stand-in forwarder, which writes its PID
// to pidFile.
func standIn(pidFile string) agent.MCPServer {
	return agent.MCPServer{Command: os.Args[0], Env: map[string]string{"OX_UR_FORWARDER": "1", "OX_UR_FORWARDER_PID": pidFile}}
}

func TestOxTools(t *testing.T) {
	t.Run("offered and run without approval", func(t *testing.T) {
		r := startMCP(t, agent.Settings{Model: model, PermissionMode: "manual"}, nil, standIn(filepath.Join(t.TempDir(), "pid")), func(w http.ResponseWriter, _ *http.Request, n int) {
			if n == 0 {
				sseCalls(w, "", call{"call_1", "echo", `{"text":"hi"}`})
				return
			}
			sse(w, "ok")
		})
		r.send("p1", "go")
		for _, u := range r.turn() {
			switch u.Type {
			case agent.UpdateApproval:
				t.Errorf("an Ox tool call asked for approval: %s", u.Request)
			case agent.UpdateToolUse:
				if u.ToolUseID != "call_1" || u.ToolName != "echo" || string(u.Input) != `{"text":"hi"}` {
					t.Errorf("tool_use %+v", u)
				}
			case agent.UpdateToolResult:
				if u.ToolUseID != "call_1" || u.Result.IsError || u.Result.Text != "hi" {
					t.Errorf("tool_result %+v", u.Result)
				}
			}
		}
		first := r.request().body
		if got := toolNames(first); !slices.Equal(got, []string{"shell", "echo"}) {
			t.Errorf("tools %q, not shell and echo", got)
		}
		system := systemMessages(first)[0]
		for _, want := range []string{"You are Ox session `ur-session`.", "`create_session`", agent.Sessions} {
			if !strings.Contains(system, want) {
				t.Errorf("system message lacks %q:\n%s", want, system)
			}
		}
		if got := messages(r.request().body)[2]; got["tool_call_id"] != "call_1" || got["content"] != "hi" {
			t.Errorf("the call's answer %v", got)
		}
	})

	t.Run("arguments not an object", func(t *testing.T) {
		r := startMCP(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, standIn(filepath.Join(t.TempDir(), "pid")), func(w http.ResponseWriter, _ *http.Request, n int) {
			if n == 0 {
				sseCalls(w, "", call{"call_1", "echo", `["hi"]`})
				return
			}
			sse(w, "ok")
		})
		r.send("p1", "go")
		result := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolResult })
		if !result.Result.IsError || result.Result.Text != "arguments: must be one JSON object" {
			t.Errorf("tool_result %+v", result.Result)
		}
		if final := r.result(); final.IsError || final.Text != "ok" {
			t.Errorf("result %+v", final)
		}
	})

	t.Run("answer after an interrupt", func(t *testing.T) {
		r := startMCP(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, standIn(filepath.Join(t.TempDir(), "pid")), func(w http.ResponseWriter, _ *http.Request, n int) {
			switch n {
			case 0:
				sseCalls(w, "", call{"call_1", "echo", `{"text":"hold"}`})
			case 1:
				sseCalls(w, "", call{"call_2", "echo", `{"text":"two"}`})
			default:
				sse(w, "ok")
			}
		})
		r.send("p1", "go")
		r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolUse })
		interrupt(t, r)
		r.request()
		r.updates = nil
		r.send("p2", "next")
		for _, u := range r.turn() {
			if u.Type == agent.UpdateToolResult && (u.ToolUseID != "call_2" || u.Result.Text != "two") {
				t.Errorf("tool_result %s %+v", u.ToolUseID, u.Result)
			}
		}
		r.request()
		body := r.request().body
		if got := roles(body); !slices.Equal(got, []string{"user", "assistant", "tool", "user", "assistant", "tool"}) {
			t.Fatalf("the last request's roles %q", got)
		}
		ms := messages(body)
		if ms[2]["tool_call_id"] != "call_1" || ms[2]["content"] != stopped {
			t.Errorf("the interrupted call's answer %v", ms[2])
		}
		if ms[5]["tool_call_id"] != "call_2" || ms[5]["content"] != "two" {
			t.Errorf("the next call's answer %v", ms[5])
		}
	})

	t.Run("ended with ur", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "pid")
		r := startMCP(t, agent.Settings{Model: model}, nil, standIn(pidFile), func(w http.ResponseWriter, _ *http.Request, _ int) {
			sse(w, "ok")
		})
		r.send("p1", "hi")
		r.idle()
		waitFile(t, pidFile)
		r.agent.Stop()
		select {
		case <-r.agent.Reaped():
		case <-time.After(5 * time.Second):
			t.Fatal("ur did not exit")
		}
		data, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatal(err)
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Errorf("the forwarder %d outlived ur: %v", pid, err)
		}
	})

	t.Run("forwarder exits", func(t *testing.T) {
		r := startMCP(t, agent.Settings{Model: model, PermissionMode: "auto"}, nil, standIn(filepath.Join(t.TempDir(), "pid")), func(w http.ResponseWriter, _ *http.Request, _ int) {
			sseCalls(w, "", call{"call_1", "echo", `{"text":"exit"}`})
		})
		r.send("p1", "go")
		r.until(func(u agent.Update) bool { return u.Type == agent.UpdateToolUse })
		exited := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateExited })
		if !strings.Contains(exited.Error, "ox-mcp exited") || strings.Contains(exited.Error, "start ox-mcp") {
			t.Errorf("exited with %q", exited.Error)
		}
	})

	t.Run("forwarder fails", func(t *testing.T) {
		r := startMCP(t, agent.Settings{Model: model}, nil, agent.MCPServer{Command: "false"}, func(w http.ResponseWriter, _ *http.Request, _ int) {
			sse(w, "ok")
		})
		r.send("p1", "hi")
		exited := r.until(func(u agent.Update) bool { return u.Type == agent.UpdateExited })
		if !strings.Contains(exited.Error, "ox-mcp exited") {
			t.Errorf("exited with %q", exited.Error)
		}
	})
}
