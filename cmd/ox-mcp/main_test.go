package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/fake"
	"github.com/kkestell/ox/internal/server"
	"github.com/kkestell/ox/internal/store"
	"github.com/kkestell/ox/internal/ur"
	"github.com/kkestell/ox/internal/ur/engine"
)

// TestMain runs the forwarder itself when a test starts this binary as
// ox-mcp, and ur when it starts it as ur. A link's name tells them apart,
// since ur passes its environment to the forwarder.
func TestMain(m *testing.M) {
	switch filepath.Base(os.Args[0]) {
	case "ur":
		os.Exit(engine.Main(os.Args[1:]))
	case "ox-mcp":
		main()
		return
	}
	if os.Getenv("OX_MCP_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

type reply struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// lines reads JSON lines from r, delivering them in order.
type lines struct {
	t  *testing.T
	ch chan reply
}

func readLines(t *testing.T, r io.Reader) *lines {
	t.Helper()
	l := &lines{t: t, ch: make(chan reply, 100)}
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			var m reply
			if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
				t.Errorf("not JSON: %q", scanner.Text())
			}
			l.ch <- m
		}
		close(l.ch)
	}()
	return l
}

func (l *lines) next() reply {
	l.t.Helper()
	select {
	case m, ok := <-l.ch:
		if !ok {
			l.t.Fatal("stream ended")
		}
		return m
	case <-time.After(5 * time.Second):
		l.t.Fatal("no line within 5s")
	}
	return reply{}
}

// ends reports that nothing more arrives before the stream closes.
func (l *lines) ends() {
	l.t.Helper()
	select {
	case m, ok := <-l.ch:
		if ok {
			l.t.Fatalf("unexpected line %+v", m)
		}
	case <-time.After(5 * time.Second):
		l.t.Fatal("stream did not end")
	}
}

type page struct {
	conn  net.Conn
	lines *lines
	id    int
}

func (p *page) send(method string, params any) int {
	p.id++
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": p.id, "method": method, "params": params})
	if _, err := p.conn.Write(append(line, '\n')); err != nil {
		p.lines.t.Fatal(err)
	}
	return p.id
}

// call sends a request and returns its response, skipping notifications.
func (p *page) call(method string, params any) reply {
	id := p.send(method, params)
	for {
		m := p.lines.next()
		if string(m.ID) == itoa(id) {
			return m
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// startOxd serves a real socket in-process and returns its path and a page
// connected to it, with a parked session.
func startOxd(t *testing.T) (s *server.Server, socket string, p *page, sessionID string) {
	t.Helper()
	// t.TempDir is too long for a socket path on macOS.
	dir, err := os.MkdirTemp("", "oxmcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	st, err := store.Open(filepath.Join(dir, "ox.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The built-in configuration titles with Claude; this one echoes.
	harnesses := map[string]agent.Harness{cfg.Titling.Harness: {Ask: fake.Ask}}
	s, err = server.New(st, cfg, 0, harnesses, server.MCP{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	socket = filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	t.Cleanup(func() { l.Close(); s.Close(); st.Close() })

	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	p = &page{conn: conn, lines: readLines(t, conn)}
	var w struct {
		ID string `json:"workspace_id"`
	}
	json.Unmarshal(p.call("workspace/create", map[string]string{"path": t.TempDir()}).Result, &w)
	var created struct {
		ID string `json:"session_id"`
	}
	r := p.call("session/create", map[string]string{
		"workspace_id": w.ID, "harness": cfg.Defaults.Harness, "model": cfg.Defaults.Model,
		"effort": cfg.Defaults.Effort, "permission_mode": cfg.Defaults.PermissionMode,
	})
	if r.Error != nil {
		t.Fatalf("session/create: %+v", r.Error)
	}
	json.Unmarshal(r.Result, &created)
	return s, socket, p, created.ID
}

type forwarder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *lines
	stderr *bytes.Buffer
	id     int
}

func startForwarder(t *testing.T, env ...string) *forwarder {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), append([]string{"OX_MCP_TEST_MAIN=1"}, env...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	f := &forwarder{cmd: cmd, stdin: stdin, stdout: readLines(t, stdout), stderr: &bytes.Buffer{}}
	cmd.Stderr = f.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
	return f
}

func (f *forwarder) send(method string, params any) {
	f.id++
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": f.id, "method": method, "params": params})
	f.stdin.Write(append(line, '\n'))
}

func (f *forwarder) notify(method string) {
	f.stdin.Write([]byte(`{"jsonrpc":"2.0","method":"` + method + `"}` + "\n"))
}

func (f *forwarder) call(method string, params any) reply {
	f.send(method, params)
	return f.stdout.next()
}

// exit waits for the forwarder to end and returns its exit code.
func (f *forwarder) exit(t *testing.T) int {
	t.Helper()
	err := f.cmd.Wait()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		return exitErr.ExitCode()
	}
	t.Fatal(err)
	return -1
}

func TestForwarder(t *testing.T) {
	s, socket, p, sessionID := startOxd(t)
	f := startForwarder(t, "OX_SOCKET="+socket, "OX_SESSION_ID="+sessionID)

	if r := f.call("initialize", map[string]any{}); r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("initialize without protocolVersion answered %s, %+v", r.Result, r.Error)
	}
	r := f.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	var init struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	json.Unmarshal(r.Result, &init)
	if init.ProtocolVersion != "2025-06-18" || init.Capabilities["tools"] == nil || init.ServerInfo.Name != "ox" {
		t.Fatalf("initialize returned %s, %+v", r.Result, r.Error)
	}
	f.notify("notifications/initialized")
	if r := f.call("ping", map[string]any{}); string(r.Result) != "{}" {
		t.Fatalf("ping returned %s, %+v", r.Result, r.Error)
	}

	// The page sees this notification; the forwarder must not.
	dir := t.TempDir()
	p.call("workspace/create", map[string]string{"path": dir})
	r = f.call("tools/list", map[string]any{})
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Type     string   `json:"type"`
				Required []string `json:"required"`
			} `json:"inputSchema"`
			Annotations struct {
				ReadOnlyHint bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	json.Unmarshal(r.Result, &list)
	if len(list.Tools) != 6 {
		t.Fatalf("tools/list returned %s", r.Result)
	}
	for i, want := range []struct {
		name     string
		readOnly bool
		required string
	}{{"list_models", true, ""}, {"list_sessions", true, ""}, {"send_message", false, "to,text"}, {"read_session", true, "to"}, {"create_session", false, "name,title,message"},
		{"create_top_level_session", false, "title,message"}} {
		got := list.Tools[i]
		if got.Name != want.name || got.InputSchema.Type != "object" || got.Annotations.ReadOnlyHint != want.readOnly ||
			strings.Join(got.InputSchema.Required, ",") != want.required {
			t.Fatalf("tools/list returned %s", r.Result)
		}
	}

	r = f.call("tools/call", map[string]any{"name": "list_models", "arguments": map[string]any{}})
	var called struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	json.Unmarshal(r.Result, &called)
	if len(called.Content) != 1 || called.Content[0].Type != "text" {
		t.Fatalf("tools/call returned %s, %+v", r.Result, r.Error)
	}
	catalog := p.call("harness/list", map[string]any{})
	var got, want any
	json.Unmarshal([]byte(called.Content[0].Text), &got)
	json.Unmarshal(catalog.Result, &want)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("list_models returned %s, harness/list returned %s", gotJSON, wantJSON)
	}

	if r := f.call("tools/call", map[string]any{"name": "nope"}); r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("unknown tool answered %s, %+v", r.Result, r.Error)
	}
	if r := f.call("harness/list", map[string]any{}); r.Error == nil || r.Error.Code != -32601 {
		t.Fatalf("harness/list answered %s, %+v", r.Result, r.Error)
	}
	if r := p.call("mcp/attach", map[string]string{"session_id": sessionID}); r.Error == nil || r.Error.Code != -32601 {
		t.Fatalf("a page's late mcp/attach answered %s, %+v", r.Result, r.Error)
	}

	s.Close()
	if r := f.call("tools/call", map[string]any{"name": "list_models"}); r.Error == nil || r.Error.Code != -32000 || r.Error.Message != "daemon is shutting down" {
		t.Fatalf("tools/call during shutdown answered %s, %+v", r.Result, r.Error)
	}

	f.stdin.Close()
	f.stdout.ends()
	if code := f.exit(t); code != 0 || f.stderr.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, f.stderr)
	}
}

func TestForwarderRefusesUnknownSession(t *testing.T) {
	_, socket, _, _ := startOxd(t)
	f := startForwarder(t, "OX_SOCKET="+socket, "OX_SESSION_ID=missing")
	if r := f.call("tools/call", map[string]any{"name": "list_models"}); r.Error == nil || r.Error.Code != -32001 || r.Error.Message != "session not found" {
		t.Fatalf("answered %s, %+v", r.Result, r.Error)
	}

	f = startForwarder(t, "OX_SOCKET="+socket, "OX_SESSION_ID=")
	f.stdout.ends()
	if code := f.exit(t); code != 1 || !bytes.Contains(f.stderr.Bytes(), []byte("session_id is required")) {
		t.Fatalf("exit %d, stderr %q", code, f.stderr)
	}
}

func TestForwarderWithoutOxd(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "none.sock")
	f := startForwarder(t, "OX_SOCKET="+socket, "OX_SESSION_ID=x")
	f.stdout.ends()
	if code := f.exit(t); code != 1 || !bytes.Contains(f.stderr.Bytes(), []byte("session x:")) || !bytes.Contains(f.stderr.Bytes(), []byte(socket)) {
		t.Fatalf("exit %d, stderr %q", code, f.stderr)
	}
}

func TestForwarderDropsLinesBeforeAttach(t *testing.T) {
	dir, err := os.MkdirTemp("", "oxmcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		bufio.NewReader(c).ReadBytes('\n')
		c.Write([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{}}` + "\n"))
		c.Write([]byte(`{"jsonrpc":"2.0","id":"attach","result":{}}` + "\n"))
	}()
	f := startForwarder(t, "OX_SOCKET="+socket, "OX_SESSION_ID=x")
	f.stdout.ends()
	if code := f.exit(t); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, f.stderr)
	}
}

// runTool marks a prompt that has Ur's model call a tool: the marker, the
// tool's name, and its arguments, to the end of the line.
const runTool = "RUN-TOOL "

// openRouter answers a request whose last message holds runTool with that
// call, and any other with the reply "done". It keeps every request's body.
type openRouter struct {
	mu       sync.Mutex
	requests []map[string]any
	calls    atomic.Int32
}

func (o *openRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var body map[string]any
	json.NewDecoder(req.Body).Decode(&body)
	o.mu.Lock()
	o.requests = append(o.requests, body)
	o.mu.Unlock()
	write := func(chunk map[string]any) {
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	messages := body["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	content, _ := last["content"].(string)
	if _, call, ok := strings.Cut(content, runTool); ok && last["role"] == "user" {
		call, _, _ = strings.Cut(call, "\n")
		name, arguments, _ := strings.Cut(call, " ")
		write(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": fmt.Sprintf("call_%d", o.calls.Add(1)), "type": "function",
			"function": map[string]any{"name": name, "arguments": arguments},
		}}}}}})
		write(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": ""}, "finish_reason": "tool_calls"}}})
	} else {
		write(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "done"}, "finish_reason": "stop"}}})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// requestsOf is the requests of the Ox session sessionID's Ur, which its
// system message names.
func (o *openRouter) requestsOf(sessionID string) []map[string]any {
	o.mu.Lock()
	defer o.mu.Unlock()
	var mine []map[string]any
	for _, body := range o.requests {
		system := body["messages"].([]any)[0].(map[string]any)["content"].(string)
		if strings.Contains(system, "You are Ox session `"+sessionID+"`.") {
			mine = append(mine, body)
		}
	}
	return mine
}

// notice is a notification a page receives.
type notice struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// sessionUpdate is a session/update notification's params.
type sessionUpdate struct {
	SessionID string `json:"session_id"`
	agent.Update
}

// watcher is a page connection that keeps every notification.
type watcher struct {
	t       *testing.T
	notices chan notice
	backlog []notice // read but not yet matched, oldest first
}

func watch(t *testing.T, socket string) *watcher {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	w := &watcher{t: t, notices: make(chan notice, 4096)}
	go func() {
		decoder := json.NewDecoder(conn)
		for {
			var n notice
			if decoder.Decode(&n) != nil {
				close(w.notices)
				return
			}
			w.notices <- n
		}
	}()
	return w
}

// next returns the oldest notification that matches, keeping the others for
// later calls.
func (w *watcher) next(matches func(notice) bool) notice {
	w.t.Helper()
	for i, n := range w.backlog {
		if matches(n) {
			w.backlog = slices.Delete(w.backlog, i, i+1)
			return n
		}
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case n, ok := <-w.notices:
			if !ok {
				w.t.Fatal("connection closed")
			}
			if matches(n) {
				return n
			}
			w.backlog = append(w.backlog, n)
		case <-timeout:
			w.t.Fatal("no matching notification within 10s")
		}
	}
}

// update returns the session's next update that matches.
func (w *watcher) update(sessionID string, matches func(agent.Update) bool) agent.Update {
	w.t.Helper()
	var u sessionUpdate
	w.next(func(n notice) bool {
		u = sessionUpdate{}
		return n.Method == "session/update" && json.Unmarshal(n.Params, &u) == nil && u.SessionID == sessionID && matches(u.Update)
	})
	return u.Update
}

// turn returns the session's updates until its next result.
func (w *watcher) turn(sessionID string) []agent.Update {
	w.t.Helper()
	var updates []agent.Update
	for {
		u := w.update(sessionID, func(agent.Update) bool { return true })
		updates = append(updates, u)
		if u.Type == agent.UpdateResult {
			return updates
		}
	}
}

// toolResult is the result of the turn's one tool call, which ran without
// asking.
func toolResult(t *testing.T, updates []agent.Update) agent.Result {
	t.Helper()
	var results []agent.Result
	for _, u := range updates {
		switch u.Type {
		case agent.UpdateApproval:
			t.Fatalf("an Ox tool call asked for approval: %s", u.Request)
		case agent.UpdateToolResult:
			results = append(results, *u.Result)
		}
	}
	if len(results) != 1 {
		t.Fatalf("tool results %+v in %+v", results, updates)
	}
	return results[0]
}

// Ur runs Ox's tools through ox-mcp, as the session oxd started it for.
func TestUr(t *testing.T) {
	dir, err := os.MkdirTemp("", "oxmcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	self, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	urCommand, forwarderCommand := filepath.Join(dir, "ur"), filepath.Join(dir, "ox-mcp")
	for _, link := range []string{urCommand, forwarderCommand} {
		if err := os.Symlink(self, link); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	router := &openRouter{}
	endpoint := httptest.NewServer(router)
	t.Cleanup(endpoint.Close)

	st, err := store.Open(filepath.Join(dir, "ox.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Harnesses: map[string]config.Harness{
			"fake": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
			"ur":   {Models: []config.Model{{ID: "u"}}, PermissionModes: []config.Option{{ID: "manual"}, {ID: "auto"}}},
		},
		Titling:       config.Titling{Harness: "fake", Model: "m"},
		MaxChildDepth: 1,
	}
	harnesses := map[string]agent.Harness{
		"fake": {Start: fake.Start, Ask: fake.Ask},
		"ur":   ur.Harness(urCommand, endpoint.URL, filepath.Join(dir, "conversations"), nil),
	}
	socket := filepath.Join(dir, "s.sock")
	s, err := server.New(st, cfg, 0, harnesses, server.MCP{Forwarder: forwarderCommand, Socket: socket}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	t.Cleanup(func() { l.Close(); s.Close(); st.Close() })
	w := watch(t, socket)
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	p := &page{conn: conn, lines: readLines(t, conn)}

	var workspace struct {
		ID string `json:"workspace_id"`
	}
	json.Unmarshal(p.call("workspace/create", map[string]string{"path": t.TempDir()}).Result, &workspace)
	create := func(harness, model string) string {
		t.Helper()
		r := p.call("session/create", map[string]string{"workspace_id": workspace.ID, "harness": harness, "model": model, "permission_mode": "manual"})
		var created struct {
			ID string `json:"session_id"`
		}
		if r.Error != nil || json.Unmarshal(r.Result, &created) != nil {
			t.Fatalf("session/create: %s, %+v", r.Result, r.Error)
		}
		return created.ID
	}
	other, urSession := create("fake", "m"), create("ur", "u")
	send := func(sessionID, text string) {
		t.Helper()
		if r := p.call("session/send", map[string]string{"session_id": sessionID, "text": text}); r.Error != nil {
			t.Fatalf("session/send: %+v", r.Error)
		}
	}

	t.Log("a manual Ur session messages another session without asking")
	send(urSession, runTool+`send_message {"to":"`+other+`","text":"hello"}`)
	if result := toolResult(t, w.turn(urSession)); result.IsError || result.Text != "Message queued for "+other+"." {
		t.Fatalf("send_message returned %+v", result)
	}
	if u := w.update(other, func(u agent.Update) bool { return u.Type == agent.UpdatePrompt }); u.From != urSession || u.Text != "hello" {
		t.Fatalf("the other session took %+v", u)
	}
	requests := router.requestsOf(urSession)
	if len(requests) != 2 {
		t.Fatalf("%d requests name the Ur session", len(requests))
	}
	toolMessage := requests[1]["messages"].([]any)[3].(map[string]any)
	if toolMessage["role"] != "tool" || toolMessage["content"] != "Message queued for "+other+"." {
		t.Fatalf("the model read %v", toolMessage)
	}

	t.Log("a manual Ur session starts a manual child of its own")
	send(urSession, runTool+`create_session {"name":"kid","title":"Work: kid","message":"hi"}`)
	result := toolResult(t, w.turn(urSession))
	var kid struct {
		SessionID string `json:"session_id"`
		Harness   string `json:"harness"`
		agent.Settings
	}
	if result.IsError || json.Unmarshal([]byte(result.Text), &kid) != nil || kid.Harness != "ur" || kid.PermissionMode != "manual" {
		t.Fatalf("create_session returned %+v", result)
	}
	created := w.next(func(n notice) bool {
		return n.Method == "session/created" && strings.Contains(string(n.Params), kid.SessionID)
	})
	var published struct {
		Child *store.Child `json:"child"`
	}
	if json.Unmarshal(created.Params, &published) != nil || published.Child == nil || published.Child.ParentID != urSession {
		t.Fatalf("the child was published as %s", created.Params)
	}

	t.Log("an Ur child whose last step messages its parent sends the parent no report")
	f := startForwarder(t, "OX_SOCKET="+socket, "OX_SESSION_ID="+other)
	createChild := func(arguments map[string]string) string {
		t.Helper()
		var called struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		r := f.call("tools/call", map[string]any{"name": "create_session", "arguments": arguments})
		var child struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(r.Result, &called) != nil || called.IsError || len(called.Content) != 1 ||
			json.Unmarshal([]byte(called.Content[0].Text), &child) != nil {
			t.Fatalf("create_session returned %s, %+v", r.Result, r.Error)
		}
		return child.SessionID
	}
	urKid := createChild(map[string]string{
		"name": "ur-kid", "title": "Work: ur-kid", "harness": "ur", "model": "u",
		"message": runTool + `send_message {"to":"` + other + `","text":"status"}`,
	})
	if result := toolResult(t, w.turn(urKid)); result.IsError {
		t.Fatalf("the child's send_message returned %+v", result)
	}
	// The probe's report follows any report the Ur child sent.
	probe := createChild(map[string]string{"name": "probe", "title": "Work: probe", "message": "hi"})
	told := false
	for {
		u := w.update(other, func(u agent.Update) bool { return u.Type == agent.UpdatePrompt })
		if u.From == urKid && u.Text == "status" {
			told = true
		}
		if slices.ContainsFunc(u.Reports, func(r agent.Report) bool { return r.From == urKid }) {
			t.Fatalf("the parent took a report from the Ur child that told it: %+v", u.Reports)
		}
		if slices.ContainsFunc(u.Reports, func(r agent.Report) bool { return r.From == probe }) {
			break
		}
	}
	if !told {
		t.Fatal("the parent did not take the Ur child's message")
	}
}
