package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

// startSession starts ox, completes the handshake, and creates a session, which
// is where every prompt test begins.
func startSession(t *testing.T, options ...startOption) (*process, string) {
	t.Helper()
	child := start(t, options...)
	initialize(t, child)
	return child, newSession(t, child, child.cwd)
}

func initialize(t *testing.T, child *process) {
	t.Helper()
	child.request("initialize", acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersion})
}

func newSessionRequest(cwd string) acp.NewSessionRequest {
	return acp.NewSessionRequest{CWD: cwd, MCPServers: []json.RawMessage{}}
}

func newSession(t *testing.T, child *process, cwd string) string {
	t.Helper()
	result := child.request("session/new", newSessionRequest(cwd))
	var response acp.NewSessionResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("decode session/new result: %v", err)
	}
	if response.SessionID == "" {
		t.Fatalf("session/new returned an empty session ID: %s", result)
	}
	return response.SessionID
}

func TestNewSessionMintsDistinctSessions(t *testing.T) {
	child, first := startSession(t)
	if second := newSession(t, child, child.cwd); second == first {
		t.Fatalf("session/new returned %s twice", first)
	}
}

func TestNewSessionRejectsInvalidRequests(t *testing.T) {
	child, _ := startSession(t)
	file := filepath.Join(child.cwd, "file.txt")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		request acp.NewSessionRequest
	}{
		{
			name:    "relative cwd",
			request: acp.NewSessionRequest{CWD: "workspace", MCPServers: []json.RawMessage{}},
		},
		{
			name: "unreachable cwd",
			request: acp.NewSessionRequest{
				CWD:        filepath.Join(child.cwd, "missing"),
				MCPServers: []json.RawMessage{},
			},
		},
		{
			name:    "cwd is a file",
			request: acp.NewSessionRequest{CWD: file, MCPServers: []json.RawMessage{}},
		},
		{
			name:    "missing mcpServers",
			request: acp.NewSessionRequest{CWD: child.cwd},
		},
		{
			name: "mcp server requested",
			request: acp.NewSessionRequest{
				CWD:        child.cwd,
				MCPServers: []json.RawMessage{json.RawMessage(`{"name":"files","command":"/bin/true","args":[]}`)},
			},
		},
		{
			name: "additional directory requested",
			request: acp.NewSessionRequest{
				CWD:                   child.cwd,
				MCPServers:            []json.RawMessage{},
				AdditionalDirectories: []string{child.cwd},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			responseError := child.requestError("session/new", test.request)
			if responseError.Code != -32602 {
				t.Fatalf("error code = %d (%s), want -32602",
					responseError.Code, responseError.Message)
			}
		})
	}
}

func TestNewSessionRejectsAnUnlistableWorkingDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can list directories regardless of their mode")
	}
	child, _ := startSession(t)
	dir := filepath.Join(child.cwd, "unlistable")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore working directory mode: %v", err)
		}
	})

	responseError := child.requestError("session/new", newSessionRequest(dir))
	if responseError.Code != -32602 {
		t.Fatalf("error code = %d (%s), want -32602",
			responseError.Code, responseError.Message)
	}
	if !strings.Contains(responseError.Message, dir) {
		t.Errorf("error message = %q, want it to name %s", responseError.Message, dir)
	}
}

func TestNewSessionRequiresAModel(t *testing.T) {
	child := start(t, withEnvironment("OX_MODEL", ""))
	initialize(t, child)

	responseError := child.requestError("session/new", newSessionRequest(child.cwd))
	if responseError.Code != -32603 {
		t.Errorf("error code = %d, want -32603", responseError.Code)
	}
	if !strings.Contains(responseError.Message, "OX_MODEL") {
		t.Errorf("error message = %q, want it to name OX_MODEL", responseError.Message)
	}
	for _, path := range []string{
		filepath.Join(child.cwd, "config", "ox", "config.json"),
		filepath.Join(child.cwd, ".ox", "config.json"),
	} {
		if !strings.Contains(responseError.Message, path) {
			t.Errorf("error message = %q, want it to name %s", responseError.Message, path)
		}
	}
}

func TestCancelWithoutARunningTurnIsANoOp(t *testing.T) {
	child, session := startSession(t)
	child.notify("session/cancel", acp.CancelNotification{SessionID: session})
	child.notify("session/cancel", acp.CancelNotification{SessionID: "missing"})
	child.notify("session/cancel", acp.CancelNotification{})

	// A round trip after the notifications proves they were handled and that ox
	// is still answering.
	if next := newSession(t, child, child.cwd); next == session {
		t.Fatalf("session/new returned %s twice", session)
	}
}
