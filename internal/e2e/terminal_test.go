package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/workspace"
)

func TestDelegatedTerminalRunsThroughClientAndContinues(t *testing.T) {
	firstArguments := `{"command":"printf first > delegated-marker"}`
	secondArguments := `{"command":"printf first"}`
	model := startModel(t,
		toolResponse("shell-1", "shell", firstArguments),
		sse(evText("first done"), evFinishReason("stop")),
		toolResponse("shell-2", "shell", secondArguments),
		sse(evText("second done"), evFinishReason("stop")),
	)
	child := start(t, withModel(model))
	initializeWithCapabilities(t, child, &acp.ClientCapabilities{Terminal: true})
	session := newSession(t, child, child.cwd)

	first := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("run first"),
	})
	permission := child.serverRequest()
	if permission.Method != acp.MethodSessionRequestPermission {
		t.Fatalf("callback method = %q, want permission", permission.Method)
	}
	child.respond(permission, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "allow_always",
		},
	})
	terminalRoundTrip(
		t,
		child,
		session,
		"printf first > delegated-marker",
		acp.WaitForTerminalExitResponse{ExitCode: intPointer(7)},
		acp.TerminalOutputResponse{Output: "delegated first\n", Truncated: true},
	)
	if response := promptResponse(t, child.result(child.await(first))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	if _, err := os.Stat(filepath.Join(child.cwd, "delegated-marker")); !os.IsNotExist(err) {
		t.Fatalf("local command ran: %v", err)
	}
	requests := model.requests()
	if len(requests) < 2 {
		t.Fatalf("model requests = %d", len(requests))
	}
	firstResult := requests[1].Messages[len(requests[1].Messages)-1].text()
	if !strings.Contains(firstResult, "exit code: 7") ||
		!strings.Contains(firstResult, "client output truncated") ||
		!strings.Contains(firstResult, "delegated first") {
		t.Fatalf("tool result sent to model = %q", firstResult)
	}

	second := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("run second"),
	})
	terminalRoundTrip(
		t,
		child,
		session,
		"printf first",
		acp.WaitForTerminalExitResponse{Signal: stringPointer("SIGTERM")},
		acp.TerminalOutputResponse{Output: "delegated second\n"},
	)
	if response := promptResponse(t, child.result(child.await(second))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	requests = model.requests()
	if len(requests) != 4 {
		t.Fatalf("model requests = %d, want 4", len(requests))
	}
	secondResult := requests[3].Messages[len(requests[3].Messages)-1].text()
	if !strings.Contains(secondResult, "signal: SIGTERM") ||
		!strings.Contains(secondResult, "delegated second") {
		t.Fatalf("tool result sent to model = %q", secondResult)
	}
}

func TestCancellingDelegatedTerminalKillsReleasesAndReplays(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t, shellToolCallResponse("sleep 30"))
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	child := start(t, options...)
	initializeWithCapabilities(t, child, &acp.ClientCapabilities{Terminal: true})
	session := newSession(t, child, child.cwd)
	cwd := child.cwd

	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("run a command"),
	})
	permission := child.serverRequest()
	if permission.Method != acp.MethodSessionRequestPermission {
		t.Fatalf("callback method = %q, want permission", permission.Method)
	}
	child.respond(permission, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: "allow_once"},
	})
	create := child.serverRequest()
	assertTerminalCreate(t, create, session, child.cwd, "sleep 30")
	child.respond(create, acp.CreateTerminalResponse{TerminalID: "terminal-cancel"})
	wait := child.serverRequest()
	assertTerminalRequest(t, wait, acp.MethodTerminalWaitForExit, session, "terminal-cancel")

	child.notify("session/cancel", acp.CancelNotification{SessionID: session})
	kill := child.serverRequest()
	assertTerminalRequest(t, kill, acp.MethodTerminalKill, session, "terminal-cancel")
	child.respond(kill, acp.KillTerminalResponse{})
	output := child.serverRequest()
	assertTerminalRequest(t, output, acp.MethodTerminalOutput, session, "terminal-cancel")
	child.respond(output, acp.TerminalOutputResponse{Output: "partial\n"})
	release := child.serverRequest()
	assertTerminalRequest(t, release, acp.MethodTerminalRelease, session, "terminal-cancel")
	child.respond(release, acp.ReleaseTerminalResponse{})

	response := promptResponse(t, child.result(child.await(turn)))
	if response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stop reason = %q, want %q", response.StopReason, acp.StopReasonCancelled)
	}
	failed := false
	for _, notification := range updates(t, child, session) {
		if notification.Update.ToolCallID == "call-shell" &&
			notification.Update.Status == acp.ToolCallStatusFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatal("cancelled terminal tool result was not reported failed")
	}
	discardPendingMethod(child, "$/cancel_request")
	child.request("session/close", acp.CloseSessionRequest{SessionID: session})
	child.stop()

	assertSessionLoadsWithToolHistory(t, options, session, cwd)
}

func TestDelegatedTerminalClientErrorFailsToolAndContinues(t *testing.T) {
	model := startModel(t,
		shellToolCallResponse("false"),
		sse(evText("continued"), evFinishReason("stop")),
	)
	child := start(t, withModel(model))
	initializeWithCapabilities(t, child, &acp.ClientCapabilities{Terminal: true})
	session := newSession(t, child, child.cwd)
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("run a command"),
	})
	allowPermission(t, child, "call-shell", true)
	create := child.serverRequest()
	assertTerminalCreate(t, create, session, child.cwd, "false")
	child.respond(create, acp.CreateTerminalResponse{TerminalID: "terminal-error"})
	wait := child.serverRequest()
	assertTerminalRequest(t, wait, acp.MethodTerminalWaitForExit, session, "terminal-error")
	child.respondError(wait, -32603, "terminal wait failed")
	release := child.serverRequest()
	assertTerminalRequest(t, release, acp.MethodTerminalRelease, session, "terminal-error")
	child.respond(release, acp.ReleaseTerminalResponse{})

	response := promptResponse(t, child.result(child.await(turn)))
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want %q", response.StopReason, acp.StopReasonEndTurn)
	}
	failed := false
	for _, notification := range updates(t, child, session) {
		if notification.Update.ToolCallID == "call-shell" &&
			notification.Update.Status == acp.ToolCallStatusFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatal("terminal callback error was not reported as a failed tool")
	}
	requests := model.requests()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	result := requests[1].Messages[len(requests[1].Messages)-1].text()
	if !strings.Contains(result, "terminal wait failed") {
		t.Fatalf("tool result sent to model = %q", result)
	}
}

func terminalRoundTrip(
	t *testing.T,
	child *process,
	session string,
	command string,
	exit acp.WaitForTerminalExitResponse,
	output acp.TerminalOutputResponse,
) {
	t.Helper()
	create := child.serverRequest()
	assertTerminalCreate(t, create, session, child.cwd, command)
	child.respond(create, acp.CreateTerminalResponse{TerminalID: "terminal-1"})
	wait := child.serverRequest()
	assertTerminalRequest(t, wait, acp.MethodTerminalWaitForExit, session, "terminal-1")
	child.respond(wait, exit)
	currentOutput := child.serverRequest()
	assertTerminalRequest(t, currentOutput, acp.MethodTerminalOutput, session, "terminal-1")
	child.respond(currentOutput, output)
	release := child.serverRequest()
	assertTerminalRequest(t, release, acp.MethodTerminalRelease, session, "terminal-1")
	child.respond(release, acp.ReleaseTerminalResponse{})
}

func assertTerminalCreate(
	t *testing.T,
	message message,
	session string,
	cwd string,
	command string,
) {
	t.Helper()
	if message.Method != acp.MethodTerminalCreate {
		t.Fatalf("callback method = %q, want %q", message.Method, acp.MethodTerminalCreate)
	}
	var request acp.CreateTerminalRequest
	if err := json.Unmarshal(message.Params, &request); err != nil {
		t.Fatal(err)
	}
	canonicalCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if request.SessionID != session || request.Command != "/bin/sh" ||
		len(request.Args) != 2 || request.Args[0] != "-c" || request.Args[1] != command ||
		request.CWD == nil || *request.CWD != canonicalCWD ||
		request.OutputByteLimit == nil || *request.OutputByteLimit != workspace.CollectionLimitBytes {
		t.Fatalf("create request = %#v", request)
	}
	environment := make(map[string]string, len(request.Env))
	for _, variable := range request.Env {
		environment[variable.Name] = variable.Value
	}
	if environment["PAGER"] != "cat" || environment["GIT_TERMINAL_PROMPT"] != "0" ||
		environment["TERM"] != "dumb" || environment["NO_COLOR"] != "1" {
		t.Fatalf("terminal environment = %#v", environment)
	}
	if _, present := environment["OPENROUTER_API_KEY"]; present {
		t.Fatal("terminal environment exposed OPENROUTER_API_KEY")
	}
}

func assertTerminalRequest(
	t *testing.T,
	message message,
	method string,
	session string,
	terminalID string,
) {
	t.Helper()
	if message.Method != method {
		t.Fatalf("callback method = %q, want %q", message.Method, method)
	}
	var request struct {
		SessionID  string `json:"sessionId"`
		TerminalID string `json:"terminalId"`
	}
	if err := json.Unmarshal(message.Params, &request); err != nil {
		t.Fatal(err)
	}
	if request.SessionID != session || request.TerminalID != terminalID {
		t.Fatalf("terminal request = %#v", request)
	}
}

func discardPendingMethod(child *process, method string) {
	kept := child.pending[:0]
	for _, message := range child.pending {
		if message.Method != method {
			kept = append(kept, message)
		}
	}
	child.pending = kept
}

func intPointer(value int) *int          { return &value }
func stringPointer(value string) *string { return &value }
