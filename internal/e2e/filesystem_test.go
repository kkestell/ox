package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestDelegatedFilesystemRunsThroughClientAndRefusesBeforeDispatch(t *testing.T) {
	model := startModel(t,
		toolResponse("read", "read_file", `{"path":"notes.txt"}`),
		toolResponse("write", "write_file", `{"path":"notes.txt","content":"three\nfour\n"}`),
		toolResponse("edit", "edit_file", `{"path":"notes.txt","old_string":"three","new_string":"THREE"}`),
		toolResponse("missing", "read_file", `{"path":"missing.txt"}`),
		sse(
			evToolCall(0, "escape", "function", "read_file", `{"path":"../outside.txt"}`),
			evToolCall(1, "unread", "function", "write_file", `{"path":"unread.txt","content":"changed"}`),
			evToolCall(2, "rejected", "function", "edit_file", `{"path":"reject.txt","old_string":"before","new_string":"after"}`),
			evFinishReason("tool_calls"),
		),
		sse(evText("done"), evFinishReason("stop")),
	)
	child := start(t,
		withModel(model),
		withFile("notes.txt", "local notes\n"),
		withFile("unread.txt", "unread local\n"),
		withFile("reject.txt", "before\n"),
	)
	initializeWithCapabilities(t, child, &acp.ClientCapabilities{
		FS: &acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
	})
	session := newSession(t, child, child.cwd)
	notesPath, err := filepath.EvalSymlinks(filepath.Join(child.cwd, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	clientContent := "one\r\ntwo"
	promptCall := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("delegate filesystem work"),
	})

	read := child.serverRequest()
	assertReadRequest(t, read, session, notesPath)
	child.respond(read, acp.ReadTextFileResponse{Content: clientContent})

	allowPermission(t, child, "write", true)
	read = child.serverRequest()
	assertReadRequest(t, read, session, notesPath)
	child.respond(read, acp.ReadTextFileResponse{Content: clientContent})
	write := child.serverRequest()
	clientContent = assertWriteRequest(t, write, session, notesPath, "three\r\nfour")
	child.respond(write, acp.WriteTextFileResponse{})

	allowPermission(t, child, "edit", true)
	read = child.serverRequest()
	assertReadRequest(t, read, session, notesPath)
	child.respond(read, acp.ReadTextFileResponse{Content: clientContent})
	write = child.serverRequest()
	clientContent = assertWriteRequest(t, write, session, notesPath, "THREE\r\nfour")
	child.respond(write, acp.WriteTextFileResponse{})

	missing := child.serverRequest()
	if missing.Method != acp.MethodFSReadTextFile {
		t.Fatalf("missing-file callback method = %q", missing.Method)
	}
	child.respondError(missing, -32602, "client cannot read missing file")

	allowPermission(t, child, "unread", true)
	allowPermission(t, child, "rejected", false)
	response := child.result(child.await(promptCall))
	var promptResponse acp.PromptResponse
	if err := json.Unmarshal(response, &promptResponse); err != nil {
		t.Fatal(err)
	}
	if promptResponse.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", promptResponse.StopReason)
	}
	if clientContent != "THREE\r\nfour" {
		t.Fatalf("client content = %q", clientContent)
	}
	for name, want := range map[string]string{
		"notes.txt":  "local notes\n",
		"unread.txt": "unread local\n",
		"reject.txt": "before\n",
	} {
		data, err := os.ReadFile(filepath.Join(child.cwd, name))
		if err != nil || string(data) != want {
			t.Fatalf("local %s = %q, %v", name, data, err)
		}
	}

	failed := map[string]bool{}
	for _, notification := range updates(t, child, session) {
		if notification.Update.Status == acp.ToolCallStatusFailed {
			failed[notification.Update.ToolCallID] = true
		}
	}
	for _, callID := range []string{"missing", "escape", "unread", "rejected"} {
		if !failed[callID] {
			t.Errorf("tool call %q was not reported failed", callID)
		}
	}
}

func toolResponse(callID, name, arguments string) string {
	return sse(
		evToolCall(0, callID, "function", name, arguments),
		evFinishReason("tool_calls"),
	)
}

func assertReadRequest(t *testing.T, request message, session, path string) {
	t.Helper()
	if request.Method != acp.MethodFSReadTextFile {
		t.Fatalf("callback method = %q, want %q", request.Method, acp.MethodFSReadTextFile)
	}
	var params acp.ReadTextFileRequest
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.SessionID != session || params.Path != path ||
		params.Line != nil || params.Limit != nil {
		t.Fatalf("read request = %#v", params)
	}
}

func assertWriteRequest(
	t *testing.T,
	request message,
	session string,
	path string,
	wantContent string,
) string {
	t.Helper()
	if request.Method != acp.MethodFSWriteTextFile {
		t.Fatalf("callback method = %q, want %q", request.Method, acp.MethodFSWriteTextFile)
	}
	var params acp.WriteTextFileRequest
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.SessionID != session || params.Path != path || params.Content != wantContent {
		t.Fatalf("write request = %#v", params)
	}
	return params.Content
}

func allowPermission(t *testing.T, child *process, callID string, allow bool) {
	t.Helper()
	request := child.serverRequest()
	if request.Method != acp.MethodSessionRequestPermission {
		t.Fatalf("callback method = %q, want permission", request.Method)
	}
	var params acp.RequestPermissionRequest
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.ToolCall.ToolCallID != callID {
		t.Fatalf("permission call = %q, want %q", params.ToolCall.ToolCallID, callID)
	}
	option := "reject_once"
	if allow {
		option = "allow_once"
	}
	child.respond(request, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: option},
	})
}
