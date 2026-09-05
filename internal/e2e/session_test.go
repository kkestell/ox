package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	return acp.NewSessionRequest{CWD: cwd, MCPServers: []acp.MCPServer{}}
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
		MCPServers: []acp.MCPServer{},
	})
}

func TestSessionConfigurationOptionsPersistAndReplay(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t)
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}

	first := start(t, options...)
	initialize(t, first)
	cwd := first.cwd
	created := first.request("session/new", newSessionRequest(cwd))
	var newResponse acp.NewSessionResponse
	if err := json.Unmarshal(created, &newResponse); err != nil {
		t.Fatal(err)
	}
	if optionValue(newResponse.ConfigOptions, "mode") != "code" ||
		optionValue(newResponse.ConfigOptions, "model") != "test/model" {
		t.Fatalf("new options = %#v", newResponse.ConfigOptions)
	}
	if err := first.requestError(acp.MethodSessionSetConfigOption, acp.SetSessionConfigOptionRequest{
		SessionID: newResponse.SessionID, ConfigID: "mode", Value: "unknown",
	}); err.Code != -32602 {
		t.Fatalf("invalid mode error = %#v", err)
	}
	unchanged := first.request(acp.MethodSessionSetConfigOption, acp.SetSessionConfigOptionRequest{
		SessionID: newResponse.SessionID, ConfigID: "mode", Value: "code",
	})
	var unchangedResponse acp.SetSessionConfigOptionResponse
	if err := json.Unmarshal(unchanged, &unchangedResponse); err != nil {
		t.Fatal(err)
	}
	if got := updates(t, first, newResponse.SessionID); len(got) != 0 {
		t.Fatalf("invalid/no-op updates = %#v", got)
	}
	set := first.request(acp.MethodSessionSetConfigOption, acp.SetSessionConfigOptionRequest{
		SessionID: newResponse.SessionID, ConfigID: "mode", Value: "plan",
	})
	var setResponse acp.SetSessionConfigOptionResponse
	if err := json.Unmarshal(set, &setResponse); err != nil {
		t.Fatal(err)
	}
	if optionValue(setResponse.ConfigOptions, "mode") != "plan" {
		t.Fatalf("set options = %#v", setResponse.ConfigOptions)
	}
	setUpdates := updates(t, first, newResponse.SessionID)
	if len(setUpdates) != 1 || setUpdates[0].Update.SessionUpdate != acp.SessionUpdateConfigOptionUpdate ||
		optionValue(setUpdates[0].Update.ConfigOptions, "mode") != "plan" {
		t.Fatalf("set updates = %#v", setUpdates)
	}
	first.request("session/close", acp.CloseSessionRequest{SessionID: newResponse.SessionID})
	first.stop()

	second := start(t, options...)
	initialize(t, second)
	loaded := second.request("session/load", acp.LoadSessionRequest{
		SessionID: newResponse.SessionID, CWD: cwd, MCPServers: []acp.MCPServer{},
	})
	var loadResponse acp.LoadSessionResponse
	if err := json.Unmarshal(loaded, &loadResponse); err != nil {
		t.Fatal(err)
	}
	if optionValue(loadResponse.ConfigOptions, "mode") != "plan" {
		t.Fatalf("load options = %#v", loadResponse.ConfigOptions)
	}
	replay := updates(t, second, newResponse.SessionID)
	if len(replay) != 1 || replay[0].Update.SessionUpdate != acp.SessionUpdateConfigOptionUpdate ||
		optionValue(replay[0].Update.ConfigOptions, "mode") != "plan" {
		t.Fatalf("replayed options = %#v", replay)
	}
	second.request("session/close", acp.CloseSessionRequest{SessionID: newResponse.SessionID})
}

func TestTodoPlanPersistsAndReplaysThroughRealProcess(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		toolResponse("todo-call", "todo", `{"todos":[{"content":"Keep going","status":"in_progress"}]}`),
		sse(evText("working"), evFinishReason("stop")),
	)
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	first, session := startSession(t, options...)
	cwd := first.cwd
	first.request(acp.MethodSessionSetConfigOption, acp.SetSessionConfigOptionRequest{
		SessionID: session, ConfigID: "mode", Value: "plan",
	})
	_ = updates(t, first, session)
	prompt(t, first, session, "track work")
	var live bool
	for _, notification := range updates(t, first, session) {
		if notification.Update.SessionUpdate == acp.SessionUpdatePlan &&
			len(notification.Update.Entries) == 1 &&
			notification.Update.Entries[0].Content == "Keep going" {
			live = true
		}
	}
	if !live {
		t.Fatal("prompt emitted no matching plan update")
	}
	requests := model.requests()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	var contextSeen bool
	for _, message := range requests[1].Messages {
		if message.Role == "system" && strings.Contains(message.text(), `"content":"Keep going"`) {
			contextSeen = true
		}
	}
	if !contextSeen {
		t.Fatalf("continuation request omitted todo context: %#v", requests[1].Messages)
	}
	first.request("session/close", acp.CloseSessionRequest{SessionID: session})
	first.stop()

	second := start(t, options...)
	initialize(t, second)
	loadSession(t, second, session, cwd)
	var replayed bool
	for _, notification := range updates(t, second, session) {
		if notification.Update.SessionUpdate == acp.SessionUpdatePlan &&
			len(notification.Update.Entries) == 1 &&
			notification.Update.Entries[0].Content == "Keep going" {
			replayed = true
		}
	}
	if !replayed {
		t.Fatal("session/load did not replay the plan update")
	}
	second.request("session/close", acp.CloseSessionRequest{SessionID: session})
}

func optionValue(options []acp.SessionConfigOption, id string) string {
	for _, option := range options {
		if option.ID == id {
			return option.CurrentValue
		}
	}
	return ""
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

func TestSessionCompactionSurvivesRestartWithoutChangingReplay(t *testing.T) {
	dataDir := t.TempDir()
	oldAnswer := strings.Repeat("old detail ", 240)
	recentAnswer := strings.Repeat("recent answer ", 100)
	model := startModel(t,
		sse(evText(oldAnswer), evFinishReason("stop"), evUsage(100, 600, 700)),
		sse(evText(recentAnswer), evFinishReason("stop"), evUsage(800, 5, 805)),
		sse(evText("preserved old details"), evFinishReason("stop"), evUsage(650, 20, 670)),
		sse(evText("third answer"), evFinishReason("stop"), evUsage(100, 5, 105)),
		sse(evText("fourth answer"), evFinishReason("stop"), evUsage(120, 5, 125)),
	)
	options := []startOption{
		withModel(model),
		withModelContextWindow(model, 14000),
		withEnvironment("XDG_DATA_HOME", dataDir),
	}

	first := start(t, options...)
	initialize(t, first)
	cwd := first.cwd
	session := newSession(t, first, cwd)
	prompt(t, first, session, "first prompt")
	_ = updates(t, first, session)
	prompt(t, first, session, "second prompt")
	_ = updates(t, first, session)
	prompt(t, first, session, "third prompt")
	thirdUpdates := updates(t, first, session)
	var occupancies []uint64
	for _, notification := range thirdUpdates {
		if notification.Update.SessionUpdate == "usage_update" {
			occupancies = append(occupancies, notification.Update.Used)
		}
	}
	if len(occupancies) != 2 || occupancies[0] == 0 || occupancies[1] != 100 {
		t.Fatalf("third prompt occupancies = %#v", occupancies)
	}
	first.request("session/close", acp.CloseSessionRequest{SessionID: session})
	first.stop()

	second := start(t, options...)
	initialize(t, second)
	replayStart := len(second.received)
	loadSession(t, second, session, cwd)
	replayed := receivedSessionUpdates(t, second, replayStart, session)
	_ = updates(t, second, session)
	var replayedText []string
	for _, notification := range replayed {
		switch notification.Update.SessionUpdate {
		case "user_message_chunk", "agent_message_chunk":
			replayedText = append(replayedText, notification.Update.Content.Text)
		}
	}
	if !reflect.DeepEqual(replayedText, []string{
		"first prompt", oldAnswer,
		"second prompt", recentAnswer,
		"third prompt", "third answer",
	}) {
		t.Fatalf("full replay text = %#v", replayedText)
	}
	prompt(t, second, session, "fourth prompt")
	_ = updates(t, second, session)

	requests := model.requests()
	if len(requests) != 5 {
		t.Fatalf("model received %d requests, want 5", len(requests))
	}
	summaryRequest := requests[2]
	if len(summaryRequest.Tools) != 0 || len(summaryRequest.Messages) != 2 ||
		summaryRequest.Messages[0].text() == requests[0].Messages[0].text() ||
		!strings.Contains(summaryRequest.Messages[1].text(), oldAnswer) {
		t.Fatalf("summary request = %#v", summaryRequest)
	}
	wantCompacted := []exchange{
		{role: "user", text: "first prompt"},
		{role: "user", text: "Summary of earlier conversation:\n\npreserved old details"},
		{role: "user", text: "second prompt"},
		{role: "assistant", text: recentAnswer},
		{role: "user", text: "third prompt"},
		{role: "assistant", text: "third answer"},
		{role: "user", text: "fourth prompt"},
	}
	assertConversation(t, requests[4].Messages, wantCompacted)
}

func TestOpenParentCompactionCheckpointSurvivesCrash(t *testing.T) {
	dataDir := t.TempDir()
	oldAnswer := strings.Repeat("old checkpoint detail ", 240)
	recentAnswer := strings.Repeat("recent checkpoint answer ", 80)
	held := (*modelResponse)(nil)
	model := startModel(t,
		sse(evText(oldAnswer), evFinishReason("stop"), evUsageCost(100, 600, 700, 0.1)),
		sse(evText(recentAnswer), evFinishReason("stop"), evUsageCost(800, 5, 805, 0.2)),
		sse(evText("folded parent facts"), evFinishReason("stop"), evUsageCost(650, 20, 670, 0.3)),
	)
	held = model.holdFor("third checkpoint prompt", "")
	model.queue(sse(evText("continued"), evFinishReason("stop"), evUsageCost(90, 3, 93, 0.4)))
	options := []startOption{
		withModel(model),
		withModelContextWindow(model, 17000),
		withEnvironment("XDG_DATA_HOME", dataDir),
	}

	first, session := startSession(t, options...)
	cwd := first.cwd
	prompt(t, first, session, "first checkpoint prompt")
	_ = updates(t, first, session)
	prompt(t, first, session, "second checkpoint prompt")
	_ = updates(t, first, session)
	_ = first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("third checkpoint prompt"),
	})
	held.await(t)
	first.kill()

	second := start(t, options...)
	initialize(t, second)
	replayStart := len(second.received)
	loadSession(t, second, session, cwd)
	firstReplay := receivedSessionUpdates(t, second, replayStart, session)
	_ = updates(t, second, session)
	second.request("session/close", acp.CloseSessionRequest{SessionID: session})
	second.stop()

	third := start(t, options...)
	initialize(t, third)
	replayStart = len(third.received)
	loadSession(t, third, session, cwd)
	secondReplay := receivedSessionUpdates(t, third, replayStart, session)
	_ = updates(t, third, session)
	if !reflect.DeepEqual(secondReplay, firstReplay) {
		t.Fatalf("parent checkpoint replay changed across reloads:\nsecond = %#v\nfirst = %#v", secondReplay, firstReplay)
	}
	assertReplayUsage(t, secondReplay, 17000, 0.59)
	prompt(t, third, session, "fourth checkpoint prompt")
	_ = updates(t, third, session)

	requests := model.requests()
	if len(requests) != 5 {
		t.Fatalf("model requests = %d, want 5", len(requests))
	}
	assertConversation(t, requests[4].Messages, []exchange{
		{role: "user", text: "first checkpoint prompt"},
		{role: "user", text: "Summary of earlier conversation:\n\nfolded parent facts"},
		{role: "user", text: "second checkpoint prompt"},
		{role: "assistant", text: recentAnswer},
		{role: "user", text: "third checkpoint prompt"},
		{role: "user", text: "fourth checkpoint prompt"},
	})
}

func TestOpenChildCompactionCheckpointSurvivesCrash(t *testing.T) {
	dataDir := t.TempDir()
	oldContents := strings.Repeat("old child detail ", 240)
	held := (*modelResponse)(nil)
	model := startModel(t,
		sse(
			evToolCall(0, "task-checkpoint", "function", "task", `{"description":"inspect","prompt":"inspect checkpoint files"}`),
			evFinishReason("tool_calls"), evUsageCost(20, 4, 24, 0.1),
		),
		sse(
			evToolCall(0, "provider-old", "function", "read_file", `{"path":"old.txt"}`),
			evFinishReason("tool_calls"), evUsageCost(40, 4, 44, 0.2),
		),
		sse(
			evToolCall(0, "provider-recent", "function", "read_file", `{"path":"recent.txt"}`),
			evFinishReason("tool_calls"), evUsageCost(5000, 4, 5004, 0.3),
		),
		sse(evText("folded child facts"), evFinishReason("stop"), evUsageCost(5200, 20, 5220, 0.4)),
	)
	held = model.holdFor("recent child detail\n", "")
	options := []startOption{
		withModel(model),
		withModelContextWindow(model, 12000),
		withEnvironment("XDG_DATA_HOME", dataDir),
		withFile("old.txt", oldContents),
		withFile("recent.txt", "recent child detail\n"),
	}

	first, session := startSession(t, options...)
	cwd := first.cwd
	_ = first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("delegate checkpoint work"),
	})
	held.await(t)
	first.kill()

	second := start(t, options...)
	initialize(t, second)
	replayStart := len(second.received)
	loadSession(t, second, session, cwd)
	firstReplay := receivedSessionUpdates(t, second, replayStart, session)
	_ = updates(t, second, session)
	second.request("session/close", acp.CloseSessionRequest{SessionID: session})
	second.stop()

	third := start(t, options...)
	initialize(t, third)
	replayStart = len(third.received)
	loadSession(t, third, session, cwd)
	secondReplay := receivedSessionUpdates(t, third, replayStart, session)
	_ = updates(t, third, session)
	if !reflect.DeepEqual(secondReplay, firstReplay) {
		t.Fatalf("child checkpoint replay changed across reloads:\nsecond = %#v\nfirst = %#v", secondReplay, firstReplay)
	}
	assertReplayUsage(t, secondReplay, 12000, 0.99)
	assertUnknownToolReplay(t, secondReplay, "task-checkpoint")

	requests := model.requests()
	if len(requests) != 5 {
		t.Fatalf("model requests = %d, want 5", len(requests))
	}
	compacted := requests[4].Messages
	assertProviderToolPairs(t, compacted)
	assertConversation(t, compacted, []exchange{
		{role: "user", text: "inspect checkpoint files"},
		{role: "user", text: "Summary of earlier conversation:\n\nfolded child facts"},
		{role: "assistant", text: ""},
		{role: "tool", text: "recent child detail\n"},
	})
}

func assertProviderToolPairs(t *testing.T, messages []modelMessage) {
	t.Helper()
	known := make(map[string]struct{})
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			known[call.ID] = struct{}{}
		}
		if message.Role == "tool" {
			if _, ok := known[message.ToolCallID]; !ok {
				t.Fatalf("tool result %q has no preceding provider call", message.ToolCallID)
			}
		}
	}
}

func assertReplayUsage(t *testing.T, replay []sessionNotification, size uint64, minimumCost float64) {
	t.Helper()
	var seen bool
	var maximumCost float64
	for _, notification := range replay {
		if notification.Update.SessionUpdate != "usage_update" {
			continue
		}
		seen = true
		if notification.Update.Size != size || notification.Update.Used == 0 ||
			notification.Update.Cost == nil {
			t.Fatalf("replayed usage = %#v", notification.Update)
		}
		maximumCost = max(maximumCost, notification.Update.Cost.Amount)
	}
	if !seen {
		t.Fatal("replay omitted usage")
	}
	if maximumCost < minimumCost {
		t.Fatalf("maximum replayed cost = %g, want at least %g", maximumCost, minimumCost)
	}
}

func TestChangedFilesAndToolLocationsSurviveProcessRestart(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		toolResponse("create-b", "write_file", `{"path":"nested/b.txt","content":"before"}`),
		toolResponse("edit-b", "edit_file", `{"path":"nested/b.txt","old_string":"before","new_string":"after"}`),
		toolResponse("create-a", "write_file", `{"path":"a.txt","content":"a"}`),
		toolResponse("failed", "edit_file", `{"path":"missing.txt","old_string":"x","new_string":"y"}`),
		toolResponse("escape", "edit_file", `{"path":"../outside.txt","old_string":"x","new_string":"y"}`),
		sse(evText("done"), evFinishReason("stop")),
	)
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	first := start(t, options...)
	initialize(t, first)
	root, err := filepath.EvalSymlinks(first.cwd)
	if err != nil {
		t.Fatal(err)
	}
	session := newSession(t, first, first.cwd)
	promptCall := first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("change files"),
	})
	allowFilePermission(t, first, "create-b", filepath.Join(root, "nested", "b.txt"), true)
	allowFilePermission(t, first, "edit-b", filepath.Join(root, "nested", "b.txt"), true)
	allowFilePermission(t, first, "create-a", filepath.Join(root, "a.txt"), true)
	allowFilePermission(t, first, "failed", filepath.Join(root, "missing.txt"), true)
	allowPermissionWithoutLocation(t, first, "escape")
	if response := promptResponse(t, first.result(first.await(promptCall))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	live := updates(t, first, session)
	wantLocations := map[string]string{
		"create-b": filepath.Join(root, "nested", "b.txt"),
		"edit-b":   filepath.Join(root, "nested", "b.txt"),
		"create-a": filepath.Join(root, "a.txt"),
		"failed":   filepath.Join(root, "missing.txt"),
	}
	assertE2EToolLocations(t, live, wantLocations, "escape")
	first.request("session/close", acp.CloseSessionRequest{SessionID: session})
	first.stop()

	assertChangedFilesCheckpoint(t, dataDir, session, []string{"a.txt", "nested/b.txt"})
	second := start(t, options...)
	initialize(t, second)
	loadSession(t, second, session, first.cwd)
	replay := updates(t, second, session)
	assertE2EToolLocations(t, replay, wantLocations, "escape")
	second.request("session/close", acp.CloseSessionRequest{SessionID: session})
	second.stop()
}

func assertE2EToolLocations(
	t *testing.T,
	updates []sessionNotification,
	want map[string]string,
	wantOmitted string,
) {
	t.Helper()
	seen := make(map[string]bool)
	for _, notification := range updates {
		update := notification.Update
		if update.SessionUpdate != "tool_call" {
			continue
		}
		if path, exists := want[update.ToolCallID]; exists {
			if len(update.Locations) != 1 || update.Locations[0].Path != path {
				t.Fatalf("tool %q locations = %#v", update.ToolCallID, update.Locations)
			}
			seen[update.ToolCallID] = true
		}
		if update.ToolCallID == wantOmitted {
			if len(update.Locations) != 0 {
				t.Fatalf("tool %q locations = %#v, want omitted", wantOmitted, update.Locations)
			}
			seen[wantOmitted] = true
		}
	}
	if len(seen) != len(want)+1 {
		t.Fatalf("tool locations seen = %v", seen)
	}
}

func assertChangedFilesCheckpoint(t *testing.T, dataDir, session string, want []string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dataDir, "ox", "sessions", session+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		var record struct {
			Type string `json:"type"`
			Data struct {
				State struct {
					ChangedFiles []string `json:"changedFiles"`
				} `json:"state"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record.Type == "checkpoint" {
			got = record.Data.State.ChangedFiles
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("checkpoint changed files = %v, want %v", got, want)
	}
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

func TestPendingPermissionRecoversAcrossProcessRestart(t *testing.T) {
	tests := []struct {
		name          string
		outcome       acp.RequestPermissionOutcome
		wantContent   string
		wantRequests  int
		wantToolState acp.ToolCallStatus
		priorDecision bool
	}{
		{
			name: "allow",
			outcome: acp.RequestPermissionOutcome{
				Outcome: "selected", OptionID: "allow_once",
			},
			wantContent: "earlier\nrecovered\n", wantRequests: 2,
			wantToolState: acp.ToolCallStatusCompleted,
			priorDecision: true,
		},
		{
			name: "reject",
			outcome: acp.RequestPermissionOutcome{
				Outcome: "selected", OptionID: "reject_once",
			},
			wantRequests: 2, wantToolState: acp.ToolCallStatusFailed,
		},
		{
			name:         "cancel",
			outcome:      acp.RequestPermissionOutcome{Outcome: "cancelled"},
			wantRequests: 1, wantToolState: acp.ToolCallStatusFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			response := shellRecoveryResponse()
			if test.priorDecision {
				response = shellBatchRecoveryResponse()
			}
			responses := []string{response}
			if test.wantRequests == 2 {
				responses = append(responses, sse(evText("done"), evFinishReason("stop")))
			}
			model := startModel(t, responses...)
			options := []startOption{
				withModel(model),
				withEnvironment("XDG_DATA_HOME", dataDir),
			}

			first, session := startSession(t, options...)
			cwd := first.cwd
			marker := filepath.Join(cwd, "recovered.txt")
			_ = first.begin("session/prompt", acp.PromptRequest{
				SessionID: session,
				Prompt:    textPrompt("recover this permission"),
			})
			originalMessage := first.serverRequest()
			if test.priorDecision {
				_ = permissionRequest(t, originalMessage, "call-earlier")
				first.respond(originalMessage, acp.RequestPermissionResponse{
					Outcome: acp.RequestPermissionOutcome{
						Outcome: "selected", OptionID: "allow_once",
					},
				})
				originalMessage = first.serverRequest()
			}
			original := permissionRequest(t, originalMessage, "call-recovery")
			first.kill()

			second := start(t, options...)
			initialize(t, second)
			load := second.begin("session/load", acp.LoadSessionRequest{
				SessionID: session, CWD: cwd, MCPServers: []acp.MCPServer{},
			})
			reissuedMessage := second.serverRequest()
			reissued := permissionRequest(t, reissuedMessage, "call-recovery")
			if !reflect.DeepEqual(reissued, original) {
				t.Fatalf("reissued permission changed:\nreissued = %#v\noriginal = %#v", reissued, original)
			}
			second.respond(reissuedMessage, acp.RequestPermissionResponse{
				Outcome: test.outcome,
			})
			_ = second.result(second.await(load))
			_ = updates(t, second, session)

			content, err := os.ReadFile(marker)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if string(content) != test.wantContent {
				t.Fatalf("recovered tool content = %q, want %q", content, test.wantContent)
			}
			if requests := model.requests(); len(requests) != test.wantRequests {
				t.Fatalf("model requests = %d, want %d", len(requests), test.wantRequests)
			}
			assertPermissionGenerations(t, dataDir, session)

			second.request("session/close", acp.CloseSessionRequest{SessionID: session})
			second.stop()
			third := start(t, options...)
			initialize(t, third)
			replayStart := len(third.received)
			loadSession(t, third, session, cwd)
			replay := receivedSessionUpdates(t, third, replayStart, session)
			_ = updates(t, third, session)
			assertRecoveredReplay(t, replay, test.wantToolState, test.priorDecision)
		})
	}
}

func TestRecoveredTurnKeepsItsFrozenConfiguration(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		shellToolCallResponse("true"),
		toolResponse("call-skill", "skill", `{"name":"recovery"}`),
		sse(evText("recovered"), evFinishReason("stop")),
		sse(evText("next"), evFinishReason("stop")),
	)
	first := start(t,
		withModel(model),
		withEnvironment("XDG_DATA_HOME", dataDir),
		withModelOverride("old/model"),
	)
	initialize(t, first)
	cwd := first.cwd
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("old instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(cwd, ".agents", "skills", "recovery", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: recovery\ndescription: old skill\n---\nold body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := newSession(t, first, cwd)
	_ = first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("recover with old settings"),
	})
	_ = permissionRequest(t, first.serverRequest(), "call-shell")
	first.kill()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("new instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: recovery\ndescription: new skill\n---\nnew body\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := start(t,
		withModel(model),
		withEnvironment("XDG_DATA_HOME", dataDir),
		withModelOverride("new/model"),
	)
	initialize(t, second)
	load := second.begin("session/load", acp.LoadSessionRequest{
		SessionID: session, CWD: cwd, MCPServers: []acp.MCPServer{},
	})
	permission := second.serverRequest()
	_ = permissionRequest(t, permission, "call-shell")
	second.respond(permission, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Outcome: "selected", OptionID: "allow_once",
		},
	})
	_ = second.result(second.await(load))
	_ = updates(t, second, session)
	prompt(t, second, session, "use new settings")
	_ = updates(t, second, session)

	requests := model.requests()
	if len(requests) != 4 {
		t.Fatalf("model requests = %d, want 4", len(requests))
	}
	models := []string{requests[0].Model, requests[1].Model, requests[2].Model, requests[3].Model}
	if !reflect.DeepEqual(models, []string{"old/model", "old/model", "old/model", "new/model"}) {
		t.Fatalf("model sequence = %v", models)
	}
	instructions := make([]string, len(requests))
	for index, request := range requests {
		instructions[index] = request.Messages[0].Content[0].Text
	}
	if !strings.Contains(instructions[0], "<workspace-instructions>\nold instructions\n\n</workspace-instructions>") ||
		!strings.Contains(instructions[0], `"description":"old skill"`) ||
		instructions[1] != instructions[0] || instructions[2] != instructions[0] ||
		!strings.Contains(instructions[3], "<workspace-instructions>\nnew instructions\n\n</workspace-instructions>") ||
		!strings.Contains(instructions[3], `"description":"new skill"`) {
		t.Fatalf("instruction prompts did not remain frozen through recovery")
	}
	if !requestContainsText(requests[2], "changed since activation") ||
		!requestContainsText(requests[2], "reactivate the session") {
		t.Fatal("recovered stale skill load did not fail with reactivation guidance")
	}
}

func TestRestartDoesNotRepeatStartedLocalTool(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t, shellToolCallResponse("printf 'once\\n' >> effect.txt; sleep 1"))
	options := []startOption{
		withModel(model), withEnvironment("XDG_DATA_HOME", dataDir),
	}
	first, session := startSession(t, options...)
	cwd := first.cwd
	_ = first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("run one effect"),
	})
	permission := first.serverRequest()
	_ = permissionRequest(t, permission, "call-shell")
	first.respond(permission, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: "allow_once"},
	})
	marker := filepath.Join(cwd, "effect.txt")
	deadline := time.Now().Add(3 * time.Second)
	for {
		content, err := os.ReadFile(marker)
		if err == nil && string(content) == "once\n" {
			break
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("local tool effect did not occur")
		}
		time.Sleep(10 * time.Millisecond)
	}
	first.kill()

	second := start(t, options...)
	initialize(t, second)
	replayStart := len(second.received)
	loadSession(t, second, session, cwd)
	replay := receivedSessionUpdates(t, second, replayStart, session)
	_ = updates(t, second, session)
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "once\n" {
		t.Fatalf("effect after recovery = %q, %v", content, err)
	}
	if requests := model.requests(); len(requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(requests))
	}
	assertUnknownToolReplay(t, replay, "call-shell")
	second.stop()
}

func TestRestartDoesNotRepeatStartedChildTool(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		toolResponse(
			"task-effect", "task",
			`{"description":"Child effect","prompt":"run the shell effect"}`,
		),
		shellToolCallResponse("printf 'once\\n' >> child-effect.txt; sleep 1"),
	)
	options := []startOption{
		withModel(model), withEnvironment("XDG_DATA_HOME", dataDir),
	}
	first, session := startSession(t, options...)
	cwd := first.cwd
	_ = first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("delegate one effect"),
	})
	permission := first.serverRequest()
	if permission.Method != acp.MethodSessionRequestPermission {
		t.Fatalf("callback method = %q, want permission", permission.Method)
	}
	var requested acp.RequestPermissionRequest
	if err := json.Unmarshal(permission.Params, &requested); err != nil {
		t.Fatal(err)
	}
	childCallID := requested.ToolCall.ToolCallID
	if childCallID == "" || requested.ToolCall.Name != "shell" {
		t.Fatalf("child permission = %#v", requested)
	}
	first.respond(permission, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: "allow_once"},
	})
	marker := filepath.Join(cwd, "child-effect.txt")
	deadline := time.Now().Add(3 * time.Second)
	for {
		content, err := os.ReadFile(marker)
		if err == nil && string(content) == "once\n" {
			break
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("child tool effect did not occur")
		}
		time.Sleep(10 * time.Millisecond)
	}
	first.kill()

	second := start(t, options...)
	initialize(t, second)
	replayStart := len(second.received)
	loadSession(t, second, session, cwd)
	replay := receivedSessionUpdates(t, second, replayStart, session)
	_ = updates(t, second, session)
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "once\n" {
		t.Fatalf("child effect after recovery = %q, %v", content, err)
	}
	if requests := model.requests(); len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	assertUnknownToolReplay(t, replay, "task-effect")
	assertUnknownToolReplay(t, replay, childCallID)
	second.stop()
}

func assertUnknownToolReplay(t *testing.T, replay []sessionNotification, callID string) {
	t.Helper()
	var pending, unknown int
	for _, notification := range replay {
		update := notification.Update
		if update.ToolCallID != callID {
			continue
		}
		switch update.SessionUpdate {
		case "tool_call":
			pending++
		case "tool_call_update":
			if update.Status == acp.ToolCallStatusFailed &&
				strings.Contains(update.Content.Text, "outcome is unknown") {
				unknown++
			}
		}
	}
	if pending != 1 || unknown != 1 {
		t.Fatalf("unknown replay = pending %d terminal %d", pending, unknown)
	}
}

func TestRecoveryRequiresTheFrozenClientExecutor(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		shellToolCallResponse("true"),
		sse(evText("recovered"), evFinishReason("stop")),
	)
	options := []startOption{
		withModel(model), withEnvironment("XDG_DATA_HOME", dataDir),
	}
	first := start(t, options...)
	initializeWithCapabilities(t, first, &acp.ClientCapabilities{Terminal: true})
	session := newSession(t, first, first.cwd)
	cwd := first.cwd
	_ = first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("recover delegated execution"),
	})
	_ = permissionRequest(t, first.serverRequest(), "call-shell")
	first.kill()

	unsupported := start(t, options...)
	initialize(t, unsupported)
	failure := unsupported.requestError("session/load", acp.LoadSessionRequest{
		SessionID: session, CWD: cwd, MCPServers: []acp.MCPServer{},
	})
	if failure.Code != -32602 || !strings.Contains(failure.Message, "terminal capability") {
		t.Fatalf("unsupported recovery error = %#v", failure)
	}
	unsupported.stop()

	recovered := start(t, options...)
	initializeWithCapabilities(t, recovered, &acp.ClientCapabilities{Terminal: true})
	load := recovered.begin("session/load", acp.LoadSessionRequest{
		SessionID: session, CWD: cwd, MCPServers: []acp.MCPServer{},
	})
	permission := recovered.serverRequest()
	_ = permissionRequest(t, permission, "call-shell")
	recovered.respond(permission, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Outcome: "selected", OptionID: "reject_once",
		},
	})
	_ = recovered.result(recovered.await(load))
	_ = updates(t, recovered, session)
	if requests := model.requests(); len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
}

func assertPermissionGenerations(t *testing.T, dataDir, session string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dataDir, "ox", "sessions", session+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var generations []uint64
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		var record struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		switch record.Type {
		case "permission_requested":
			var value struct {
				Pending struct {
					CallID     string `json:"callId"`
					Generation uint64 `json:"generation"`
				} `json:"pending"`
			}
			if err := json.Unmarshal(record.Data, &value); err != nil {
				t.Fatal(err)
			}
			if value.Pending.CallID == "call-recovery" {
				generations = append(generations, value.Pending.Generation)
			}
		case "permission_reissued":
			var value struct {
				CallID     string `json:"callId"`
				Generation uint64 `json:"generation"`
			}
			if err := json.Unmarshal(record.Data, &value); err != nil {
				t.Fatal(err)
			}
			if value.CallID == "call-recovery" {
				generations = append(generations, value.Generation)
			}
		}
	}
	if !reflect.DeepEqual(generations, []uint64{1, 2}) {
		t.Fatalf("permission generations = %v, want [1 2]", generations)
	}
}

func shellRecoveryResponse() string {
	arguments, _ := json.Marshal(map[string]string{
		"command": "printf 'recovered\\n' >> recovered.txt",
	})
	return sse(
		evText("working"),
		evToolCall(0, "call-recovery", "function", "shell", string(arguments)),
		evFinishReason("tool_calls"),
	)
}

func shellBatchRecoveryResponse() string {
	earlier, _ := json.Marshal(map[string]string{
		"command": "printf 'earlier\\n' >> recovered.txt",
	})
	recovered, _ := json.Marshal(map[string]string{
		"command": "printf 'recovered\\n' >> recovered.txt",
	})
	return sse(
		evText("working"),
		evToolCall(0, "call-earlier", "function", "shell", string(earlier)),
		evToolCall(1, "call-recovery", "function", "shell", string(recovered)),
		evFinishReason("tool_calls"),
	)
}

func permissionRequest(
	t *testing.T,
	request message,
	callID string,
) acp.RequestPermissionRequest {
	t.Helper()
	if request.Method != acp.MethodSessionRequestPermission {
		t.Fatalf("callback method = %q, want permission", request.Method)
	}
	var permission acp.RequestPermissionRequest
	if err := json.Unmarshal(request.Params, &permission); err != nil {
		t.Fatal(err)
	}
	if permission.ToolCall.ToolCallID != callID {
		t.Fatalf("permission call = %q, want %q", permission.ToolCall.ToolCallID, callID)
	}
	return permission
}

func assertRecoveredReplay(
	t *testing.T,
	updates []sessionNotification,
	wantStatus acp.ToolCallStatus,
	wantPrior bool,
) {
	t.Helper()
	var messages, pending, outcomes, priorPending, priorOutcome int
	for _, notification := range updates {
		update := notification.Update
		switch update.SessionUpdate {
		case acp.SessionUpdateAgentMessageChunk:
			if update.Content.Text == "working" {
				messages++
			}
		case "tool_call":
			if update.ToolCallID == "call-recovery" {
				pending++
			}
			if update.ToolCallID == "call-earlier" {
				priorPending++
			}
		case "tool_call_update":
			if update.ToolCallID == "call-recovery" && update.Status == wantStatus {
				outcomes++
			}
			if update.ToolCallID == "call-earlier" && update.Status == acp.ToolCallStatusCompleted {
				priorOutcome++
			}
		}
	}
	if messages != 1 || pending != 1 || outcomes != 1 {
		t.Fatalf(
			"recovered replay counts = message %d, pending %d, outcome %d; want 1 each",
			messages, pending, outcomes,
		)
	}
	wantPriorCount := 0
	if wantPrior {
		wantPriorCount = 1
	}
	if priorPending != wantPriorCount || priorOutcome != wantPriorCount {
		t.Fatalf(
			"prior replay counts = pending %d, outcome %d; want %d each",
			priorPending, priorOutcome, wantPriorCount,
		)
	}
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
			request: acp.NewSessionRequest{CWD: "workspace", MCPServers: []acp.MCPServer{}},
		},
		{
			name: "unreachable cwd",
			request: acp.NewSessionRequest{
				CWD:        filepath.Join(child.cwd, "missing"),
				MCPServers: []acp.MCPServer{},
			},
		},
		{
			name:    "cwd is a file",
			request: acp.NewSessionRequest{CWD: file, MCPServers: []acp.MCPServer{}},
		},
		{
			name:    "missing mcpServers",
			request: acp.NewSessionRequest{CWD: child.cwd},
		},
		{
			name: "mcp server requested",
			request: acp.NewSessionRequest{
				CWD:        child.cwd,
				MCPServers: []acp.MCPServer{{Stdio: &acp.MCPStdioServer{Name: "files", Command: "/bin/true", Args: []string{}, Env: []acp.EnvVariable{}}}},
			},
		},
		{
			name: "additional directory requested",
			request: acp.NewSessionRequest{
				CWD:                   child.cwd,
				MCPServers:            []acp.MCPServer{},
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
	child := start(t, withModelOverride(""))
	initialize(t, child)

	responseError := child.requestError("session/new", newSessionRequest(child.cwd))
	if responseError.Code != -32603 {
		t.Errorf("error code = %d, want -32603", responseError.Code)
	}
	if !strings.Contains(responseError.Message, "--model") {
		t.Errorf("error message = %q, want it to name --model", responseError.Message)
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
