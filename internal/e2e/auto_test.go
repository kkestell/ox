package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/acp"
)

// TestAutoModeRunsGatedToolsAndCodeRestoresPrompts covers the shipped binary's
// auto mode: an approval-gated call reaches its real effect with no permission
// request, the selection survives a reload, and returning to code asks again
// rather than reusing the authorization auto gave.
func TestAutoModeRunsGatedToolsAndCodeRestoresPrompts(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		toolResponse("auto-write", "write_file", `{"path":"auto.txt","content":"auto\n"}`),
		sse(evText("auto done"), evFinishReason("stop")),
		toolResponse("code-write", "write_file", `{"path":"code.txt","content":"code\n"}`),
		sse(evText("code done"), evFinishReason("stop")),
	)
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	first, session := startSession(t, options...)
	cwd := first.cwd
	setMode(t, first, session, "auto")

	prompt(t, first, session, "write in auto mode")
	_ = updates(t, first, session)
	if content, err := os.ReadFile(filepath.Join(cwd, "auto.txt")); err != nil ||
		string(content) != "auto\n" {
		t.Fatalf("auto-mode write = %q, %v", content, err)
	}
	assertNoPermissionRequest(t, first)

	setMode(t, first, session, "code")
	turn := first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("write in code mode"),
	})
	request := first.serverRequest()
	permission := permissionRequest(t, request, "code-write")
	if permission.ToolCall.Name != "write_file" {
		t.Fatalf("permission tool = %#v", permission.ToolCall)
	}
	first.respond(request, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: "allow_once"},
	})
	if response := promptResponse(t, first.result(first.await(turn))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("code-mode stop reason = %q", response.StopReason)
	}
	_ = updates(t, first, session)

	setMode(t, first, session, "auto")
	first.request("session/close", acp.CloseSessionRequest{SessionID: session})
	first.stop()

	second := start(t, options...)
	initialize(t, second)
	loaded := second.request("session/load", acp.LoadSessionRequest{
		SessionID: session, CWD: cwd, MCPServers: []acp.MCPServer{},
	})
	var loadResponse acp.LoadSessionResponse
	if err := json.Unmarshal(loaded, &loadResponse); err != nil {
		t.Fatal(err)
	}
	if optionValue(loadResponse.ConfigOptions, "mode") != "auto" {
		t.Fatalf("reloaded options = %#v", loadResponse.ConfigOptions)
	}
	_ = updates(t, second, session)
	second.request("session/close", acp.CloseSessionRequest{SessionID: session})
}

// TestAutoModeRunsChildAgentToolsWithoutPermission covers a child agent under
// the session mode its parent is running in. The parent is held at its next
// model request until the child's gated write lands, so releasing it cannot end
// the turn first.
func TestAutoModeRunsChildAgentToolsWithoutPermission(t *testing.T) {
	model := startModel(t)
	model.queueFor("delegate", toolResponse(
		"start-child", "subagent_start", `{"name":"worker","task":"write child.txt"}`,
	))
	childHeld := model.holdFor("write child.txt", "")
	parentHeld := model.hold("")
	model.queue(sse(evText("child done"), evFinishReason("stop")))

	child, session := startSession(t, withModel(model))
	setMode(t, child, session, "auto")
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("delegate"),
	})
	childHeld.await(t)
	parentHeld.await(t)
	childHeld.finish(sse(
		evToolCall(0, "child-write", "function", "write_file", `{"path":"child.txt","content":"child\n"}`),
		evFinishReason("tool_calls"),
	))
	awaitFile(t, filepath.Join(child.cwd, "child.txt"), "child\n")
	parentHeld.finish(sse(evText("parent done"), evFinishReason("stop")))

	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	assertNoPermissionRequest(t, child)
}

// TestAutoModeDoesNotApproveARecoveredCodePermission covers the turn boundary
// at its sharpest point: a durable permission wait belongs to the code-mode
// turn that opened it, so selecting auto while that wait is recovered neither
// answers it nor runs its effect.
func TestAutoModeDoesNotApproveARecoveredCodePermission(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		shellRecoveryResponse(),
		sse(evText("recovered"), evFinishReason("stop")),
	)
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	first, session := startSession(t, options...)
	cwd := first.cwd
	_ = first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("recover a permission wait"),
	})
	_ = permissionRequest(t, first.serverRequest(), "call-recovery")
	first.kill()

	second := start(t, options...)
	initialize(t, second)
	load := second.begin("session/load", acp.LoadSessionRequest{
		SessionID: session, CWD: cwd, MCPServers: []acp.MCPServer{},
	})
	reissued := second.serverRequest()
	_ = permissionRequest(t, reissued, "call-recovery")
	setMode(t, second, session, "auto")
	second.respond(reissued, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: "reject_once"},
	})
	second.result(second.await(load))
	_ = updates(t, second, session)
	if _, err := os.Stat(filepath.Join(cwd, "recovered.txt")); !os.IsNotExist(err) {
		t.Fatalf("rejected recovery ran its effect: %v", err)
	}
	second.request("session/close", acp.CloseSessionRequest{SessionID: session})
}

func setMode(t *testing.T, child *process, session, mode string) {
	t.Helper()
	result := child.request(acp.MethodSessionSetConfigOption, acp.SetSessionConfigOptionRequest{
		SessionID: session, ConfigID: "mode", Value: mode,
	})
	var response acp.SetSessionConfigOptionResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatal(err)
	}
	if optionValue(response.ConfigOptions, "mode") != mode {
		t.Fatalf("mode after setting %q = %#v", mode, response.ConfigOptions)
	}
	_ = updates(t, child, session)
}

// assertNoPermissionRequest reports a permission request ox sent. A turn that
// ran to completion cannot be waiting on one, so this catches a request that
// was somehow made and answered rather than one still outstanding.
func assertNoPermissionRequest(t *testing.T, child *process) {
	t.Helper()
	for _, received := range child.received {
		if received.Method == acp.MethodSessionRequestPermission {
			t.Fatalf("auto mode asked for permission: %s", received.raw)
		}
	}
}

func awaitFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(readTimeout)
	for {
		content, err := os.ReadFile(path)
		if err == nil && string(content) == want {
			return
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %q, %v; want %q", path, content, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestModeChangeReachesTheRunningTurn covers the point of a live mode: the turn
// starts in code and is already waiting on a permission request when auto is
// selected, and every later call in that same turn runs unasked.
func TestModeChangeReachesTheRunningTurn(t *testing.T) {
	model := startModel(t,
		toolResponse("write-one", "write_file", `{"path":"one.txt","content":"one\n"}`),
		toolResponse("write-two", "write_file", `{"path":"two.txt","content":"two\n"}`),
		sse(evText("both written"), evFinishReason("stop")),
	)
	child, session := startSession(t, withModel(model))
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("write twice"),
	})

	asked := child.serverRequest()
	_ = permissionRequest(t, asked, "write-one")
	setMode(t, child, session, "auto")
	child.respond(asked, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: "allow_once"},
	})

	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	for _, name := range []string{"one.txt", "two.txt"} {
		if _, err := os.Stat(filepath.Join(child.cwd, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if asks := permissionRequestCount(child); asks != 1 {
		t.Fatalf("permission requests = %d, want 1", asks)
	}
}

// TestPlanSelectedMidTurnWithdrawsMutatingTools covers the other half of a live
// mode: plan chosen while a code turn is running narrows the tools the next
// provider request declares, and a mutating call the model already asked for is
// refused rather than reported as a tool that does not exist.
func TestPlanSelectedMidTurnWithdrawsMutatingTools(t *testing.T) {
	model := startModel(t)
	held := model.hold("")
	model.queue(sse(evText("planning instead"), evFinishReason("stop")))

	child, session := startSession(t, withModel(model))
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("write a file"),
	})
	held.await(t)
	setMode(t, child, session, "plan")
	held.finish(sse(
		evToolCall(0, "blocked-write", "function", "write_file", `{"path":"blocked.txt","content":"no\n"}`),
		evFinishReason("tool_calls"),
	))

	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	if _, err := os.Stat(filepath.Join(child.cwd, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("plan mode ran a withdrawn write: %v", err)
	}
	if asks := permissionRequestCount(child); asks != 0 {
		t.Fatalf("permission requests = %d, want 0", asks)
	}
	requests := model.requests()
	if len(requests) < 2 {
		t.Fatalf("model requests = %d, want at least 2", len(requests))
	}
	first, last := declaredTools(requests[0]), declaredTools(requests[len(requests)-1])
	if !first["write_file"] {
		t.Fatalf("code-mode request declared %v", first)
	}
	if last["write_file"] || !last["read_file"] {
		t.Fatalf("plan-mode request declared %v", last)
	}
	if !containsMessage(requests[len(requests)-1], "not available in plan mode") {
		t.Fatalf("withdrawn call result = %#v", requests[len(requests)-1].Messages)
	}
}

func permissionRequestCount(child *process) int {
	count := 0
	for _, received := range child.received {
		if received.Method == acp.MethodSessionRequestPermission {
			count++
		}
	}
	return count
}

func containsMessage(request modelRequest, text string) bool {
	for _, message := range request.Messages {
		if strings.Contains(message.text(), text) {
			return true
		}
	}
	return false
}

func declaredTools(request modelRequest) map[string]bool {
	names := make(map[string]bool, len(request.Tools))
	for _, declaration := range request.Tools {
		var tool struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if err := json.Unmarshal(declaration, &tool); err == nil {
			names[tool.Function.Name] = true
		}
	}
	return names
}
