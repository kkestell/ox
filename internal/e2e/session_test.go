package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	initializeWithCapabilities(t, child, nil)
}

func initializeWithCapabilities(
	t *testing.T,
	child *process,
	capabilities *acp.ClientCapabilities,
) {
	t.Helper()
	child.request("initialize", acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion,
		ClientCapabilities: capabilities,
	})
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

func loadSession(t *testing.T, child *process, session, cwd string) {
	t.Helper()
	child.request("session/load", acp.LoadSessionRequest{
		SessionID:  session,
		CWD:        cwd,
		MCPServers: []json.RawMessage{},
	})
}

func receivedSessionUpdates(
	t *testing.T,
	child *process,
	start int,
	session string,
) []sessionNotification {
	t.Helper()
	var updates []sessionNotification
	for _, message := range child.received[start:] {
		if message.Method != "session/update" || len(message.ID) != 0 {
			continue
		}
		var notification sessionNotification
		if err := json.Unmarshal(message.Params, &notification); err != nil {
			t.Fatal(err)
		}
		if notification.SessionID == session {
			updates = append(updates, notification)
		}
	}
	return updates
}

func TestSessionCanContinueInANewProcess(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		sse(evText("first answer"), evFinishReason("stop")),
		sse(evText("second answer"), evFinishReason("stop")),
		sse(evText("third answer"), evFinishReason("stop")),
	)
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}

	first := start(t, options...)
	initialize(t, first)
	cwd := first.cwd
	session := newSession(t, first, cwd)
	prompt(t, first, session, "first prompt")
	_ = updates(t, first, session)
	prompt(t, first, session, "second prompt")
	_ = updates(t, first, session)
	first.request("session/close", acp.CloseSessionRequest{SessionID: session})
	first.stop()

	second := start(t, options...)
	initialize(t, second)
	firstReplayStart := len(second.received)
	loadSession(t, second, session, cwd)
	firstReplay := receivedSessionUpdates(t, second, firstReplayStart, session)
	_ = updates(t, second, session)
	if len(firstReplay) != 4 {
		t.Fatalf("session/load replayed %d updates, want 4", len(firstReplay))
	}
	wantKinds := []string{
		"user_message_chunk",
		"agent_message_chunk",
		"user_message_chunk",
		"agent_message_chunk",
	}
	identities := make(map[string]struct{}, len(firstReplay))
	for index, notification := range firstReplay {
		if notification.Update.SessionUpdate != wantKinds[index] {
			t.Fatalf(
				"replay update %d kind = %q, want %q",
				index,
				notification.Update.SessionUpdate,
				wantKinds[index],
			)
		}
		if notification.Update.MessageID == "" {
			t.Fatalf("replay update %d has no message ID", index)
		}
		if _, exists := identities[notification.Update.MessageID]; exists {
			t.Fatalf("replay reused message ID %q", notification.Update.MessageID)
		}
		identities[notification.Update.MessageID] = struct{}{}
	}
	second.request("session/close", acp.CloseSessionRequest{SessionID: session})
	second.stop()

	third := start(t, options...)
	initialize(t, third)
	secondReplayStart := len(third.received)
	loadSession(t, third, session, cwd)
	secondReplay := receivedSessionUpdates(t, third, secondReplayStart, session)
	_ = updates(t, third, session)
	if !reflect.DeepEqual(secondReplay, firstReplay) {
		t.Fatalf("replay changed across restarts:\nsecond = %#v\nfirst = %#v", secondReplay, firstReplay)
	}
	prompt(t, third, session, "third prompt")
	_ = updates(t, third, session)

	requests := model.requests()
	if len(requests) != 3 {
		t.Fatalf("model received %d requests, want 3", len(requests))
	}
	assertConversation(t, requests[2].Messages, []exchange{
		{role: "user", text: "first prompt"},
		{role: "assistant", text: "first answer"},
		{role: "user", text: "second prompt"},
		{role: "assistant", text: "second answer"},
		{role: "user", text: "third prompt"},
	})
}

func TestCancellingPermissionWaitLeavesReplayableSession(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t, shellToolCallResponse("sleep 30"))
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	child, session := startSession(t, options...)
	cwd := child.cwd

	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("run a command"),
	})
	permission := child.serverRequest()
	if permission.Method != acp.MethodSessionRequestPermission {
		t.Fatalf("callback method = %q", permission.Method)
	}
	child.notify("session/cancel", acp.CancelNotification{SessionID: session})
	response := promptResponse(t, child.result(child.await(turn)))
	if response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stopReason = %q, want %q", response.StopReason, acp.StopReasonCancelled)
	}
	_ = updates(t, child, session)
	child.request("session/close", acp.CloseSessionRequest{SessionID: session})
	child.stop()

	assertSessionLoadsWithToolHistory(t, options, session, cwd)
}

func TestCancellingShellLeavesReplayableSession(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t, shellToolCallResponse("sleep 30"))
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	child, session := startSession(t, options...)
	cwd := child.cwd

	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("run a command"),
	})
	permission := child.serverRequest()
	child.respond(permission, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: "allow_once"},
	})
	waitForToolStatus(t, child, acp.ToolCallStatusInProgress)
	child.notify("session/cancel", acp.CancelNotification{SessionID: session})
	response := promptResponse(t, child.result(child.await(turn)))
	if response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stopReason = %q, want %q", response.StopReason, acp.StopReasonCancelled)
	}
	_ = updates(t, child, session)
	child.request("session/close", acp.CloseSessionRequest{SessionID: session})
	child.stop()

	assertSessionLoadsWithToolHistory(t, options, session, cwd)
}

func shellToolCallResponse(command string) string {
	arguments, _ := json.Marshal(map[string]string{"command": command})
	return sse(
		evToolCall(0, "call-shell", "function", "shell", string(arguments)),
		evFinishReason("tool_calls"),
	)
}

func waitForToolStatus(t *testing.T, child *process, want acp.ToolCallStatus) {
	t.Helper()
	for {
		message := child.notification("session/update")
		var notification struct {
			Update acp.ToolCallUpdate `json:"update"`
		}
		if err := json.Unmarshal(message.Params, &notification); err != nil {
			t.Fatal(err)
		}
		if notification.Update.Status == want {
			return
		}
	}
}

func assertSessionLoadsWithToolHistory(
	t *testing.T,
	options []startOption,
	session string,
	cwd string,
) {
	t.Helper()
	child := start(t, options...)
	initialize(t, child)
	loadSession(t, child, session, cwd)
	if replayed := updates(t, child, session); len(replayed) == 0 {
		t.Fatal("session/load replayed no tool history")
	}
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
		filepath.Join(child.cwd, "config", "ox", "settings.json"),
		filepath.Join(child.cwd, ".ox", "settings.json"),
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
