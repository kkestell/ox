package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/fake"
	"github.com/kkestell/ox/internal/server"
	"github.com/kkestell/ox/internal/store"
)

// TestMain runs the forwarder itself when a test starts this binary as ox-mcp.
func TestMain(m *testing.M) {
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
	if len(list.Tools) != 5 {
		t.Fatalf("tools/list returned %s", r.Result)
	}
	for i, want := range []struct {
		name     string
		readOnly bool
		required string
	}{{"list_models", true, ""}, {"list_sessions", true, ""}, {"send_message", false, "to,text"}, {"read_session", true, "to"}, {"create_session", false, "name,title,message"}} {
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
