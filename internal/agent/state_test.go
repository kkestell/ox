package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
)

func TestFoldBuildsExactModelHistoryAndReplay(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	configuration := requestConfiguration{
		Settings:      settings.Resolved{Model: "test/model"},
		ContextWindow: 1000,
		SystemPrompt:  "system prompt",
	}
	records := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: id, CWD: "/workspace", Configuration: configuration,
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "turn", MessageID: "user", Content: []acp.ContentBlock{
				{Type: "text", Text: "inspect "},
				{Type: "resource_link", Name: "file", URI: "file:///workspace/a.go"},
			},
		}),
		mustRecord(t, 3, recordModelExchange, modelExchangeRecord{
			TurnID:    "turn",
			AnswerID:  "answer",
			ThoughtID: "thought",
			Text:      "working",
			Reasoning: "because",
			ReasoningDetails: [][]byte{
				[]byte(`{ "type": "reasoning.text", "text": "opaque" }`),
			},
			FinishReason: "tool_calls",
			Usage:        &openrouter.Usage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6, Cost: 0.25},
			ToolCalls: []openrouter.ToolCall{{
				ID: "call", Type: "function",
				Function: openrouter.ToolCallFunction{Name: "read", Arguments: `{"path":"a.go"}`},
			}},
			ToolResults: []storedToolResult{{
				CallID:           "call",
				Content:          "contents",
				ApprovalDecision: decisionAllowOnce,
			}},
		}),
		mustRecord(t, 4, recordModelExchange, modelExchangeRecord{
			TurnID: "turn", AnswerID: "final", ThoughtID: "final-thought",
			Text: "done", FinishReason: "stop",
		}),
		mustRecord(t, 5, recordTurnFinished, turnFinishedRecord{
			TurnID: "turn", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	}
	state, err := foldRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	if state.openTurn != "" || len(state.history) != 4 {
		t.Fatalf("open turn = %q, history = %#v", state.openTurn, state.history)
	}
	for _, message := range state.history {
		if message.Role == openrouter.RoleSystem {
			t.Fatalf("system prompt entered model history: %#v", state.history)
		}
	}
	if got := state.history[0].Content[1].Text; got != "[file](file:///workspace/a.go)" {
		t.Fatalf("resource link projection = %q", got)
	}
	wantOpaque := records[2]
	var exchange modelExchangeRecord
	if err := json.Unmarshal(wantOpaque.Data, &exchange); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(state.history[1].ReasoningDetails[0], exchange.ReasoningDetails[0]) {
		t.Fatalf("reasoning details changed: %q", state.history[1].ReasoningDetails[0])
	}
	if exchange.ToolResults[0].ApprovalDecision != decisionAllowOnce {
		t.Fatalf("approval decision = %q", exchange.ToolResults[0].ApprovalDecision)
	}
	instance, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	updates, err := instance.replay(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 8 {
		t.Fatalf("replay updates = %d, want 8", len(updates))
	}
	toolCall, ok := updates[4].(acp.ToolCall)
	if !ok {
		t.Fatalf("tool replay update = %T, want acp.ToolCall", updates[4])
	}
	if !bytes.Equal(toolCall.RawInput, []byte(`{"path":"a.go"}`)) {
		t.Fatalf("tool raw input = %s", toolCall.RawInput)
	}
}

func TestCompactionFoldsIntoProviderHistoryWithoutChangingReplay(t *testing.T) {
	records, summary := compactionFixture(t)
	state, err := foldRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	if state.occupancy != 222 || state.usage.input != 1750 ||
		state.usage.output != 40 || state.cost < 0.599 || state.cost > 0.601 {
		t.Fatalf("usage = %#v, occupancy = %d, cost = %f", state.usage, state.occupancy, state.cost)
	}
	if len(state.history) != 4 ||
		state.history[0].Content[0].Text != "first request" ||
		state.history[1].Role != summary.Role ||
		state.history[1].Content[0].Text != summary.Content[0].Text ||
		state.history[2].Role != openrouter.RoleAssistant ||
		state.history[3].Role != openrouter.RoleTool {
		t.Fatalf("compacted history = %#v", state.history)
	}

	instance, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	updates, err := instance.replay(state)
	if err != nil {
		t.Fatal(err)
	}
	var userMessages []string
	for _, update := range updates {
		if chunk, ok := update.(acp.UserMessageChunk); ok {
			userMessages = append(userMessages, chunk.Content.Text)
		}
	}
	if !reflect.DeepEqual(userMessages, []string{"first request", "second request"}) {
		t.Fatalf("replayed user messages = %#v", userMessages)
	}
	usage, ok := updates[len(updates)-1].(acp.UsageUpdate)
	if !ok || usage.Used != 222 || usage.Cost == nil ||
		usage.Cost.Amount < 0.599 || usage.Cost.Amount > 0.601 {
		t.Fatalf("final replay update = %#v", updates[len(updates)-1])
	}
}

func TestCompactionSurvivesCheckpointWithoutDoubleCountingUsage(t *testing.T) {
	records, summary := compactionFixture(t)
	state, err := foldRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []sessionRecord{
		mustRecord(t, 9, recordUserMessage, userMessageRecord{
			TurnID: "third", MessageID: "third-user",
			Content: []acp.ContentBlock{{Type: "text", Text: "continue"}},
		}),
		mustRecord(t, 10, recordModelExchange, modelExchangeRecord{
			TurnID: "third", AnswerID: "third-answer", ThoughtID: "third-thought",
			Text: "done", FinishReason: "stop",
		}),
		mustRecord(t, 11, recordTurnFinished, turnFinishedRecord{
			TurnID: "third", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	} {
		if err := state.apply(record); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint, err := newCheckpointRecord(state)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := foldRecords(append(append(records, state.records[8:]...), checkpoint))
	if err != nil {
		t.Fatal(err)
	}
	if restored.occupancy != 222 || restored.usage.input != 1750 ||
		restored.usage.output != 40 || restored.cost < 0.599 || restored.cost > 0.601 {
		t.Fatalf(
			"restored usage = %#v, occupancy = %d, cost = %f",
			restored.usage,
			restored.occupancy,
			restored.cost,
		)
	}
	if len(restored.history) != 6 ||
		restored.history[1].Role != summary.Role ||
		restored.history[1].Content[0].Text != summary.Content[0].Text {
		t.Fatalf("restored history = %#v", restored.history)
	}
}

func TestCompactionRejectsOpenTurnsAndIncompleteToolGroups(t *testing.T) {
	records, summary := compactionFixture(t)
	base, err := foldRecords(records[:7])
	if err != nil {
		t.Fatal(err)
	}
	invalidBoundary := mustRecord(t, 8, recordCompaction, compactionRecord{
		HeadEnd: 1, TailStart: 4, Summary: summary, Occupancy: 222,
	})
	if err := base.apply(invalidBoundary); err == nil ||
		!strings.Contains(err.Error(), "separates a tool call") {
		t.Fatalf("incomplete tool group error = %v", err)
	}

	open := base.clone()
	if err := open.apply(mustRecord(t, 8, recordUserMessage, userMessageRecord{
		TurnID: "open", MessageID: "open-user",
		Content: []acp.ContentBlock{{Type: "text", Text: "new request"}},
	})); err != nil {
		t.Fatal(err)
	}
	if err := open.apply(mustRecord(t, 9, recordCompaction, compactionRecord{
		HeadEnd: 1, TailStart: 3, Summary: summary, Occupancy: 222,
	})); err == nil || !strings.Contains(err.Error(), "during a turn") {
		t.Fatalf("open-turn compaction error = %v", err)
	}
}

func compactionFixture(t *testing.T) ([]sessionRecord, openrouter.Message) {
	t.Helper()
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"}, ContextWindow: 1000,
		SystemPrompt: "system prompt",
		Tools: []openrouter.Tool{{
			Type: "function",
			Function: openrouter.ToolFunction{
				Name: "read", Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
		ToolKinds: map[string]acp.ToolKind{"read": acp.ToolKindRead},
	}
	summary := newSummaryMessage("preserved facts")
	return []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: "0123456789abcdef0123456789abcdef",
			CWD:       "/workspace", Configuration: configuration,
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "first", MessageID: "first-user",
			Content: []acp.ContentBlock{{Type: "text", Text: "first request"}},
		}),
		mustRecord(t, 3, recordModelExchange, modelExchangeRecord{
			TurnID: "first", AnswerID: "first-answer", ThoughtID: "first-thought",
			Text: "old answer", FinishReason: "stop",
			Usage: &openrouter.Usage{
				PromptTokens: 800, CompletionTokens: 20, TotalTokens: 820, Cost: 0.1,
			},
		}),
		mustRecord(t, 4, recordTurnFinished, turnFinishedRecord{
			TurnID: "first", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
		mustRecord(t, 5, recordUserMessage, userMessageRecord{
			TurnID: "second", MessageID: "second-user",
			Content: []acp.ContentBlock{{Type: "text", Text: "second request"}},
		}),
		mustRecord(t, 6, recordModelExchange, modelExchangeRecord{
			TurnID: "second", AnswerID: "second-answer", ThoughtID: "second-thought",
			FinishReason: "tool_calls",
			Usage: &openrouter.Usage{
				PromptTokens: 850, CompletionTokens: 10, TotalTokens: 860, Cost: 0.2,
			},
			ToolCalls: []openrouter.ToolCall{{
				ID: "call", Type: "function",
				Function: openrouter.ToolCallFunction{Name: "read", Arguments: `{"path":"a.go"}`},
			}},
			ToolResults: []storedToolResult{{CallID: "call", Content: "package a"}},
		}),
		mustRecord(t, 7, recordTurnFinished, turnFinishedRecord{
			TurnID: "second", Kind: "request_limit",
			StopReason: acp.StopReasonMaxTokens,
		}),
		mustRecord(t, 8, recordCompaction, compactionRecord{
			HeadEnd: 1, TailStart: 3, Summary: summary,
			Usage: &openrouter.Usage{
				PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, Cost: 0.3,
			},
			Occupancy: 222,
		}),
	}, summary
}

func TestFoldTracksChangedFilesAcrossCheckpointAndReplaysLocations(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	tool := func(id, name, target string) openrouter.ToolCall {
		return openrouter.ToolCall{
			ID: id, Type: "function",
			Function: openrouter.ToolCallFunction{
				Name: name, Arguments: fmt.Sprintf(`{"path":%q}`, target),
			},
		}
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		Tools: []openrouter.Tool{
			{Type: "function", Function: openrouter.ToolFunction{Name: "write_file"}},
			{Type: "function", Function: openrouter.ToolFunction{Name: "edit_file"}},
			{Type: "function", Function: openrouter.ToolFunction{Name: "task"}},
		},
		ToolKinds: map[string]acp.ToolKind{
			"write_file": acp.ToolKindEdit,
			"edit_file":  acp.ToolKindEdit,
			"task":       acp.ToolKindOther,
		},
	}
	firstCalls := []openrouter.ToolCall{
		tool("write", "write_file", "z.txt"),
		tool("edit", "edit_file", "z.txt"),
		tool("failed", "edit_file", "failed.txt"),
		tool("rejected", "edit_file", "rejected.txt"),
		tool("parent", "task", ""),
	}
	records := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: id, CWD: "/workspace", Configuration: configuration,
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "first", MessageID: "first-user",
			Content: []acp.ContentBlock{{Type: "text", Text: "change files"}},
		}),
		mustRecord(t, 3, recordModelExchange, modelExchangeRecord{
			TurnID: "first", AnswerID: "first-answer", ThoughtID: "first-thought",
			FinishReason: "tool_calls", ToolCalls: firstCalls,
			ToolResults: []storedToolResult{
				{CallID: "write", Content: "created", Target: "z.txt"},
				{CallID: "edit", Content: "edited", Target: "z.txt"},
				{CallID: "failed", Content: "failed", Failed: true, Target: "failed.txt"},
				{
					CallID: "rejected", Content: "rejected", Failed: true,
					ApprovalDecision: decisionRefused, Target: "rejected.txt",
				},
				{CallID: "parent", Content: "delegated", Delegation: &delegationRecord{
					Prompt: "edit", Answer: "done", Calls: []delegatedCall{
						{
							CallID: "child-ok", Name: "edit_file",
							Arguments: json.RawMessage(`{"path":"a.txt"}`),
							Content:   "edited", Target: "a.txt",
						},
						{
							CallID: "child-failed", Name: "edit_file",
							Arguments: json.RawMessage(`{"path":"child-failed.txt"}`),
							Content:   "failed", Failed: true, Target: "child-failed.txt",
						},
					},
				}},
			},
		}),
		mustRecord(t, 4, recordTurnFinished, turnFinishedRecord{
			TurnID: "first", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	}
	prefix := mustFold(t, records)
	checkpoint, err := newCheckpointRecord(prefix)
	if err != nil {
		t.Fatal(err)
	}
	records = append(records, checkpoint,
		mustRecord(t, 6, recordUserMessage, userMessageRecord{
			TurnID: "second", MessageID: "second-user",
			Content: []acp.ContentBlock{{Type: "text", Text: "change another"}},
		}),
		mustRecord(t, 7, recordModelExchange, modelExchangeRecord{
			TurnID: "second", AnswerID: "second-answer", ThoughtID: "second-thought",
			FinishReason: "tool_calls",
			ToolCalls:    []openrouter.ToolCall{tool("tail", "write_file", "b.txt")},
			ToolResults:  []storedToolResult{{CallID: "tail", Content: "created", Target: "b.txt"}},
		}),
		mustRecord(t, 8, recordTurnFinished, turnFinishedRecord{
			TurnID: "second", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	)
	state := mustFold(t, records)
	if got := sortedIdentitySet(state.changedFiles); !reflect.DeepEqual(got, []string{"a.txt", "b.txt", "z.txt"}) {
		t.Fatalf("changed files = %v", got)
	}

	instance, err := New(Config{Tools: []Tool{
		{Name: "write_file", Kind: acp.ToolKindEdit},
		{Name: "edit_file", Kind: acp.ToolKindEdit},
		{
			Name: "task", Kind: acp.ToolKindOther, Delegates: true,
			Label: func(json.RawMessage) string { return "task" },
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	updates, err := instance.replay(state)
	if err != nil {
		t.Fatal(err)
	}
	wantLocations := map[string]string{
		"write": "/workspace/z.txt", "edit": "/workspace/z.txt",
		"failed": "/workspace/failed.txt", "rejected": "/workspace/rejected.txt",
		"child-ok": "/workspace/a.txt", "child-failed": "/workspace/child-failed.txt",
		"tail": "/workspace/b.txt",
	}
	for _, update := range updates {
		call, ok := update.(acp.ToolCall)
		if !ok || wantLocations[call.ToolCallID] == "" {
			continue
		}
		if len(call.Locations) != 1 || call.Locations[0].Path != wantLocations[call.ToolCallID] {
			t.Fatalf("tool %q locations = %#v", call.ToolCallID, call.Locations)
		}
		delete(wantLocations, call.ToolCallID)
	}
	if len(wantLocations) != 0 {
		t.Fatalf("missing replay locations: %v", wantLocations)
	}
}

func TestFoldRejectsIncompleteToolGroupAndDuplicateMessage(t *testing.T) {
	base := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: "0123456789abcdef0123456789abcdef",
			CWD:       "/workspace",
			Configuration: requestConfiguration{
				Settings: settings.Resolved{Model: "test/model"},
			},
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "turn", MessageID: "same",
			Content: []acp.ContentBlock{{Type: "text", Text: "hello"}},
		}),
	}
	duplicate := append([]sessionRecord(nil), base...)
	duplicate = append(duplicate, mustRecord(t, 3, recordUserMessage, userMessageRecord{
		TurnID: "turn", MessageID: "same",
		Content: []acp.ContentBlock{{Type: "text", Text: "again"}},
	}))
	if _, err := foldRecords(duplicate); err == nil {
		t.Fatal("duplicate message ID was accepted")
	}

	incomplete := append([]sessionRecord(nil), base...)
	incomplete = append(incomplete, mustRecord(t, 3, recordModelExchange, modelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls",
		ToolCalls:    []openrouter.ToolCall{{ID: "call"}},
	}))
	if _, err := foldRecords(incomplete); err == nil {
		t.Fatal("incomplete tool group was accepted")
	}
}

func TestFoldResumesSuspendedPermissionGenerationAndReplaysOnce(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	call := openrouter.ToolCall{
		ID: "call", Type: "function",
		Function: openrouter.ToolCallFunction{Name: "edit", Arguments: `{"path":"a.go"}`},
	}
	configuration := requestConfiguration{
		Settings:      settings.Resolved{Model: "test/model"},
		ContextWindow: 1000,
		Tools: []openrouter.Tool{{
			Type: "function",
			Function: openrouter.ToolFunction{
				Name: "edit", Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
		ToolKinds: map[string]acp.ToolKind{"edit": acp.ToolKindEdit},
	}
	request := acp.RequestPermissionRequest{
		SessionID: id,
		ToolCall: acp.ToolCallUpdate{
			ToolCallID: call.ID, Name: call.Function.Name,
			Title: "edit", Kind: acp.ToolKindEdit,
			Locations: []acp.ToolCallLocation{{Path: "/workspace/a.go"}},
			RawInput:  json.RawMessage(call.Function.Arguments),
		},
		Options: permissionOptions("", false),
	}
	records := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: id, CWD: "/workspace", Configuration: configuration,
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "turn", MessageID: "user",
			Content: []acp.ContentBlock{{Type: "text", Text: "run it"}},
		}),
		mustRecord(t, 3, recordExchangePaused, suspendedModelExchangeRecord{
			TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
			Text: "working", FinishReason: "tool_calls",
			ToolCalls: []openrouter.ToolCall{call}, ToolTargets: map[string]string{"call": "a.go"},
			RequestCount: 3,
		}),
		mustRecord(t, 4, recordPermissionOpen, permissionRequestedRecord{
			TurnID: "turn",
			Pending: pendingPermissionRecord{
				CallID: call.ID, Generation: 1, Request: request,
			},
		}),
		mustRecord(t, 5, recordPermissionRetry, permissionReissuedRecord{
			TurnID: "turn", CallID: call.ID, Generation: 2,
		}),
	}
	state := mustFold(t, records)
	if state.suspended == nil || state.suspended.Pending == nil ||
		state.suspended.Pending.Generation != 2 || state.suspended.RequestCount != 3 {
		t.Fatalf("folded suspension = %#v", state.suspended)
	}
	value := &session{state: state}
	recorded, err := (&Agent{}).commitPermissionDecision(value, permissionDecidedRecord{
		TurnID: "turn", CallID: call.ID, Generation: 1,
		Decision: decisionAllowOnce,
	})
	if err != nil || recorded {
		t.Fatalf("stale decision = recorded %v, error %v", recorded, err)
	}
	if value.state.suspended.Pending.Generation != 2 {
		t.Fatal("stale decision changed the pending generation")
	}

	records = append(records,
		mustRecord(t, 6, recordPermissionDone, permissionDecidedRecord{
			TurnID: "turn", CallID: call.ID, Generation: 2,
			Decision: decisionAllowOnce,
		}),
		mustRecord(t, 7, recordModelExchange, modelExchangeRecord{
			TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
			Text: "working", FinishReason: "tool_calls",
			ToolCalls: []openrouter.ToolCall{call},
			ToolResults: []storedToolResult{{
				CallID: call.ID, Content: "done",
				ApprovalDecision: decisionAllowOnce, Target: "a.go",
			}},
		}),
		mustRecord(t, 8, recordTurnFinished, turnFinishedRecord{
			TurnID: "turn", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	)
	state = mustFold(t, records)
	if state.suspended != nil || state.openTurn != "" || len(state.history) != 3 {
		t.Fatalf("completed state = %#v", state)
	}
	if _, changed := state.changedFiles["a.go"]; !changed {
		t.Fatal("recovered edit was not recorded as changed")
	}
	instance, err := New(Config{Tools: []Tool{{Name: "edit", Kind: acp.ToolKindEdit}}})
	if err != nil {
		t.Fatal(err)
	}
	updates, err := instance.replay(state)
	if err != nil {
		t.Fatal(err)
	}
	var messages, pending, completed int
	for _, update := range updates {
		switch value := update.(type) {
		case acp.AgentMessageChunk:
			if value.Content.Text == "working" {
				messages++
			}
		case acp.ToolCall:
			if value.ToolCallID == call.ID {
				if len(value.Locations) != 1 || value.Locations[0].Path != "/workspace/a.go" {
					t.Fatalf("replay locations = %#v", value.Locations)
				}
				pending++
			}
		case acp.ToolCallUpdate:
			if value.ToolCallID == call.ID && value.Status == acp.ToolCallStatusCompleted {
				completed++
			}
		}
	}
	if messages != 1 || pending != 1 || completed != 1 {
		t.Fatalf("replay counts = message %d, pending %d, completed %d", messages, pending, completed)
	}
}

func TestFoldRejectsInvalidSuspendedPermissionRecords(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	call := openrouter.ToolCall{
		ID: "call", Type: "function",
		Function: openrouter.ToolCallFunction{Name: "shell", Arguments: `{}`},
	}
	base := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: id, CWD: "/workspace",
			Configuration: requestConfiguration{Settings: settings.Resolved{Model: "test/model"}},
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "turn", MessageID: "user",
			Content: []acp.ContentBlock{{Type: "text", Text: "run it"}},
		}),
		mustRecord(t, 3, recordExchangePaused, suspendedModelExchangeRecord{
			TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
			FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{call},
			RequestCount: 1,
		}),
	}
	request := acp.RequestPermissionRequest{
		SessionID: id,
		ToolCall: acp.ToolCallUpdate{
			ToolCallID: call.ID, Name: call.Function.Name,
			RawInput: json.RawMessage(call.Function.Arguments),
		},
		Options: permissionOptions("", false),
	}
	open := permissionRequestedRecord{
		TurnID:  "turn",
		Pending: pendingPermissionRecord{CallID: call.ID, Generation: 1, Request: request},
	}
	tests := []struct {
		name    string
		records []sessionRecord
	}{
		{
			name: "wrong initial generation",
			records: append(append([]sessionRecord(nil), base...),
				mustRecord(t, 4, recordPermissionOpen, func() permissionRequestedRecord {
					value := open
					value.Pending.Generation = 2
					return value
				}())),
		},
		{
			name: "unknown call",
			records: append(append([]sessionRecord(nil), base...),
				mustRecord(t, 4, recordPermissionOpen, func() permissionRequestedRecord {
					value := open
					value.Pending.CallID = "other"
					return value
				}())),
		},
		{
			name: "stale decision",
			records: append(append(append([]sessionRecord(nil), base...),
				mustRecord(t, 4, recordPermissionOpen, open),
				mustRecord(t, 5, recordPermissionRetry, permissionReissuedRecord{
					TurnID: "turn", CallID: call.ID, Generation: 2,
				})), mustRecord(t, 6, recordPermissionDone, permissionDecidedRecord{
				TurnID: "turn", CallID: call.ID, Generation: 1,
				Decision: decisionAllowOnce,
			})),
		},
		{
			name: "completion while pending",
			records: append(append(append([]sessionRecord(nil), base...),
				mustRecord(t, 4, recordPermissionOpen, open)),
				mustRecord(t, 5, recordModelExchange, modelExchangeRecord{
					TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
					FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{call},
					ToolResults: []storedToolResult{{CallID: call.ID}},
				})),
		},
		{
			name: "interruption while pending",
			records: append(append(append([]sessionRecord(nil), base...),
				mustRecord(t, 4, recordPermissionOpen, open)),
				mustRecord(t, 5, recordTurnFinished, turnFinishedRecord{
					TurnID: "turn", Kind: "interrupted",
				})),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := foldRecords(test.records); err == nil {
				t.Fatal("invalid suspended permission record was accepted")
			}
		})
	}

	interrupted := append(append([]sessionRecord(nil), base...),
		mustRecord(t, 4, recordTurnFinished, turnFinishedRecord{
			TurnID: "turn", Kind: "interrupted",
		}))
	state := mustFold(t, interrupted)
	if state.openTurn != "" || state.suspended != nil {
		t.Fatalf("interrupted non-pending suspension = %#v", state)
	}
}

func TestFoldAcceptsConfigurationWithoutSystemPrompt(t *testing.T) {
	record := mustRecord(t, 1, recordSessionCreated, sessionCreated{
		SessionID: "0123456789abcdef0123456789abcdef",
		CWD:       "/workspace",
		Configuration: requestConfiguration{
			Settings: settings.Resolved{Model: "test/model"},
		},
	})
	if bytes.Contains(record.Data, []byte("systemPrompt")) {
		t.Fatalf("legacy fixture unexpectedly contains systemPrompt: %s", record.Data)
	}

	state, err := foldRecords([]sessionRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	if state.configuration.SystemPrompt != "" {
		t.Fatalf("system prompt = %q, want empty legacy value", state.configuration.SystemPrompt)
	}
	if len(state.history) != 0 {
		t.Fatalf("configuration entered model history: %#v", state.history)
	}
	instance, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	updates, err := instance.replay(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("configuration entered ACP replay: %#v", updates)
	}
}

func TestExecutorCapabilitiesArePartOfDurableConfiguration(t *testing.T) {
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		ExecutorCapabilities: executorCapabilities{
			FileSystemRead: true,
			Terminal:       true,
		},
	}
	created := mustRecord(t, 1, recordSessionCreated, sessionCreated{
		SessionID:     "0123456789abcdef0123456789abcdef",
		CWD:           "/workspace",
		Configuration: configuration,
	})
	state := mustFold(t, []sessionRecord{created})
	if state.configuration.ExecutorCapabilities != configuration.ExecutorCapabilities {
		t.Fatalf("folded capabilities = %#v", state.configuration.ExecutorCapabilities)
	}

	cloned := state.clone()
	cloned.configuration.ExecutorCapabilities.FileSystemRead = false
	if !state.configuration.ExecutorCapabilities.FileSystemRead {
		t.Fatal("clone changed the original executor capabilities")
	}

	changed := cloneConfiguration(configuration)
	changed.ExecutorCapabilities = executorCapabilities{FileSystemWrite: true}
	if sameRequestConfiguration(configuration, changed) {
		t.Fatal("configuration equality ignored executor capabilities")
	}
	semanticallySame := cloneConfiguration(configuration)
	semanticallySame.Tools = []openrouter.Tool{}
	semanticallySame.Subagent.Tools = []openrouter.Tool{}
	if !sameRequestConfiguration(configuration, semanticallySame) {
		t.Fatal("configuration equality distinguished omitted empty tool lists")
	}
	change := mustRecord(t, 2, recordConfigChanged, configurationChanged{
		Configuration: changed,
	})
	if err := state.apply(change); err != nil {
		t.Fatal(err)
	}
	if state.configuration.ExecutorCapabilities != changed.ExecutorCapabilities {
		t.Fatalf("changed capabilities = %#v", state.configuration.ExecutorCapabilities)
	}
}

func TestFoldKeepsDelegationOutOfHistoryAndReplaysNestedCalls(t *testing.T) {
	instance, err := New(Config{Tools: []Tool{
		{
			Name:      "task",
			Kind:      acp.ToolKindOther,
			Delegates: true,
			Label: func(arguments json.RawMessage) string {
				var input struct {
					Description string `json:"description"`
				}
				_ = json.Unmarshal(arguments, &input)
				return input.Description
			},
		},
		{Name: "read", Kind: acp.ToolKindRead},
	}})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings:      settings.Resolved{Model: "test/model"},
		ContextWindow: 1000,
		Tools:         cloneTools(instance.primaryTools.modelTools),
		ToolKinds: map[string]acp.ToolKind{
			"task": acp.ToolKindOther,
			"read": acp.ToolKindRead,
		},
		Subagent: subagentConfiguration{
			SystemPrompt: "subagent",
			Tools:        cloneTools(instance.subagentTools.modelTools),
		},
	}
	records := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID:     "0123456789abcdef0123456789abcdef",
			CWD:           "/workspace",
			Configuration: configuration,
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "turn", MessageID: "user",
			Content: []acp.ContentBlock{{Type: "text", Text: "delegate"}},
		}),
		mustRecord(t, 3, recordModelExchange, modelExchangeRecord{
			TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
			FinishReason: "tool_calls",
			Usage: &openrouter.Usage{
				PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5, Cost: 0.1,
			},
			ToolCalls: []openrouter.ToolCall{{
				ID: "parent", Type: "function",
				Function: openrouter.ToolCallFunction{
					Name: "task", Arguments: `{"description":"Inspect","prompt":"read it"}`,
				},
			}},
			ToolResults: []storedToolResult{{
				CallID: "parent", Content: "child answer",
				Delegation: &delegationRecord{
					Prompt: "read it", Answer: "child answer",
					Calls: []delegatedCall{{
						CallID: "child", Name: "read",
						Arguments: json.RawMessage(`{"path":"a.go"}`),
						Content:   "contents",
					}},
					Usage: []openrouter.Usage{{
						PromptTokens: 7, CompletionTokens: 4, TotalTokens: 11, Cost: 0.2,
					}},
				},
			}},
		}),
		mustRecord(t, 4, recordTurnFinished, turnFinishedRecord{
			TurnID: "turn", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	}
	state, err := foldRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.history) != 3 {
		t.Fatalf("history = %#v", state.history)
	}
	if state.history[2].Role != openrouter.RoleTool ||
		state.history[2].ToolCallID != "parent" ||
		state.history[2].Content[0].Text != "child answer" {
		t.Fatalf("parent tool history = %#v", state.history[2])
	}
	if state.usage.input != 10 || state.usage.output != 6 ||
		state.cost < 0.299 || state.cost > 0.301 {
		t.Fatalf("usage = %#v, cost = %f", state.usage, state.cost)
	}
	updates, err := instance.replay(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 6 {
		t.Fatalf("updates = %#v", updates)
	}
	parent := updates[1].(acp.ToolCall)
	child := updates[2].(acp.ToolCall)
	childResult := updates[3].(acp.ToolCallUpdate)
	if parent.Title != "Inspect" || parent.Meta[acp.MetaSubagent] != true {
		t.Fatalf("parent replay = %#v", parent)
	}
	if child.Meta[acp.MetaParentToolCallID] != "parent" ||
		childResult.Meta[acp.MetaParentToolCallID] != "parent" {
		t.Fatalf("child replay = %#v, %#v", child, childResult)
	}
	usage := updates[5].(acp.UsageUpdate)
	if usage.Used != 3 || usage.Cost == nil ||
		usage.Cost.Amount < 0.299 || usage.Cost.Amount > 0.301 {
		t.Fatalf("replayed usage = %#v", usage)
	}

	duplicate := records[2]
	var exchange modelExchangeRecord
	if err := json.Unmarshal(duplicate.Data, &exchange); err != nil {
		t.Fatal(err)
	}
	exchange.ToolResults[0].Delegation.Calls[0].CallID = "parent"
	records[2] = mustRecord(t, 3, recordModelExchange, exchange)
	if _, err := foldRecords(records); err == nil ||
		!strings.Contains(err.Error(), `duplicate tool call ID "parent"`) {
		t.Fatalf("duplicate delegated ID error = %v", err)
	}
}

func TestFoldRestoresLatestCheckpointAndAppliesItsTail(t *testing.T) {
	baseTime := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	configuration := requestConfiguration{
		Settings:      settings.Resolved{Model: "test/first"},
		ContextWindow: 1000,
		SystemPrompt:  "first system prompt",
		ExecutorCapabilities: executorCapabilities{
			FileSystemRead: true,
		},
		Tools: []openrouter.Tool{{
			Type: "function",
			Function: openrouter.ToolFunction{
				Name: "read", Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
		ToolKinds: map[string]acp.ToolKind{"read": acp.ToolKindRead},
	}
	prefix := []sessionRecord{
		timedRecord(t, baseTime, 1, recordSessionCreated, sessionCreated{
			SessionID:     "0123456789abcdef0123456789abcdef",
			CWD:           "/workspace",
			Configuration: configuration,
		}),
		timedRecord(t, baseTime.Add(time.Second), 2, recordUserMessage, userMessageRecord{
			TurnID: "first-turn", MessageID: "first-user",
			Content: []acp.ContentBlock{{Type: "text", Text: "inspect the workspace"}},
		}),
		timedRecord(t, baseTime.Add(2*time.Second), 3, recordModelExchange, modelExchangeRecord{
			TurnID: "first-turn", AnswerID: "first-answer", ThoughtID: "first-thought",
			Text: "reading", Reasoning: "need the file", FinishReason: "tool_calls",
			Usage: &openrouter.Usage{
				PromptTokens: 7, CompletionTokens: 5, TotalTokens: 12, Cost: 0.25,
				PromptTokensDetails: &openrouter.PromptTokensDetails{
					CachedTokens: 2, CacheWriteTokens: 1,
				},
				CompletionTokensDetails: &openrouter.CompletionTokensDetails{ReasoningTokens: 3},
			},
			ToolCalls: []openrouter.ToolCall{{
				ID: "first-call", Type: "function",
				Function: openrouter.ToolCallFunction{Name: "read", Arguments: `{"path":"a.go"}`},
			}},
			ToolResults: []storedToolResult{{CallID: "first-call", Content: "package a"}},
		}),
		timedRecord(t, baseTime.Add(3*time.Second), 4, recordTurnFinished, turnFinishedRecord{
			TurnID: "first-turn", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	}
	prefixState, err := foldRecords(prefix)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := newCheckpointRecord(prefixState)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.At = baseTime.Add(4 * time.Second)

	changed := cloneConfiguration(configuration)
	changed.Settings.Model = "test/second"
	changed.SystemPrompt = "second system prompt"
	changed.ExecutorCapabilities = executorCapabilities{
		FileSystemWrite: true,
		Terminal:        true,
	}
	withCheckpoint := append(append([]sessionRecord(nil), prefix...), checkpoint)
	withCheckpoint = append(withCheckpoint,
		timedRecord(t, baseTime.Add(5*time.Second), 6, recordConfigChanged, configurationChanged{
			Configuration: changed,
		}),
		timedRecord(t, baseTime.Add(6*time.Second), 7, recordUserMessage, userMessageRecord{
			TurnID: "second-turn", MessageID: "second-user",
			Content: []acp.ContentBlock{{Type: "text", Text: "summarize"}},
		}),
		timedRecord(t, baseTime.Add(7*time.Second), 8, recordModelExchange, modelExchangeRecord{
			TurnID: "second-turn", AnswerID: "second-answer", ThoughtID: "second-thought",
			Text: "summary", FinishReason: "stop",
			Usage: &openrouter.Usage{
				PromptTokens: 11, CompletionTokens: 4, TotalTokens: 15, Cost: 0.5,
			},
		}),
		timedRecord(t, baseTime.Add(8*time.Second), 9, recordTurnFinished, turnFinishedRecord{
			TurnID: "second-turn", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	)

	fullRecords := make([]sessionRecord, 0, len(withCheckpoint)-1)
	for _, record := range withCheckpoint {
		if record.Type == recordCheckpoint {
			continue
		}
		record.Sequence = uint64(len(fullRecords) + 1)
		fullRecords = append(fullRecords, record)
	}
	full, err := foldRecords(fullRecords)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := foldRecords(withCheckpoint)
	if err != nil {
		t.Fatal(err)
	}

	fullHistory, err := json.Marshal(full.history)
	if err != nil {
		t.Fatal(err)
	}
	restoredHistory, err := json.Marshal(restored.history)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restoredHistory, fullHistory) {
		t.Fatalf("checkpoint history differs:\nrestored = %s\nfull = %s", restoredHistory, fullHistory)
	}
	full.sequence, restored.sequence = 0, 0
	full.records, restored.records = nil, nil
	full.history, restored.history = nil, nil
	if !reflect.DeepEqual(restored, full) {
		t.Fatalf("checkpoint projection differs:\nrestored = %#v\nfull = %#v", restored, full)
	}
	if restored.configuration.Settings.Model != "test/second" ||
		restored.configuration.ExecutorCapabilities != changed.ExecutorCapabilities ||
		restored.usage.input != 18 || restored.usage.output != 9 ||
		restored.usage.thought != 3 || restored.usage.cachedRead != 2 ||
		restored.usage.cachedWrite != 1 || restored.cost != 0.75 ||
		restored.title != "inspect the workspace" {
		t.Fatalf("restored projection = %#v", restored)
	}
	if _, exists := restored.messageIDs["first-answer"]; !exists {
		t.Fatal("checkpoint omitted a covered message identity")
	}
	if _, exists := restored.toolCallIDs["first-call"]; !exists {
		t.Fatal("checkpoint omitted a covered tool-call identity")
	}

	instance, err := New(Config{Tools: []Tool{{Name: "read", Kind: acp.ToolKindRead}}})
	if err != nil {
		t.Fatal(err)
	}
	fullReplay, err := instance.replay(mustFold(t, fullRecords))
	if err != nil {
		t.Fatal(err)
	}
	checkpointReplay, err := instance.replay(mustFold(t, withCheckpoint))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(checkpointReplay, fullReplay) {
		t.Fatalf("checkpoint replay differs:\ncheckpoint = %#v\nfull = %#v", checkpointReplay, fullReplay)
	}
	covered := append([]sessionRecord(nil), withCheckpoint...)
	covered[1].Data = json.RawMessage(`{}`)
	if _, err := foldRecords(covered); err != nil {
		t.Fatalf("checkpoint-covered record was applied again: %v", err)
	}
}

func TestFoldRejectsInvalidCheckpoints(t *testing.T) {
	baseTime := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	prefix := []sessionRecord{
		timedRecord(t, baseTime, 1, recordSessionCreated, sessionCreated{
			SessionID: "0123456789abcdef0123456789abcdef",
			CWD:       "/workspace",
			Configuration: requestConfiguration{
				Settings: settings.Resolved{Model: "test/model"},
			},
		}),
		timedRecord(t, baseTime.Add(time.Second), 2, recordUserMessage, userMessageRecord{
			TurnID: "turn", MessageID: "user",
			Content: []acp.ContentBlock{{Type: "text", Text: "hello"}},
		}),
		timedRecord(t, baseTime.Add(2*time.Second), 3, recordTurnFinished, turnFinishedRecord{
			TurnID: "turn", Kind: "cancelled",
		}),
	}
	closed := mustFold(t, prefix)
	checkpoint, err := newCheckpointRecord(closed)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.At = baseTime.Add(3 * time.Second)

	mutateCheckpoint := func(t *testing.T, mutate func(*checkpointRecord)) sessionRecord {
		t.Helper()
		var value checkpointRecord
		if err := json.Unmarshal(checkpoint.Data, &value); err != nil {
			t.Fatal(err)
		}
		mutate(&value)
		return timedRecord(t, checkpoint.At, checkpoint.Sequence, recordCheckpoint, value)
	}
	tests := []struct {
		name    string
		records []sessionRecord
	}{
		{
			name: "malformed payload",
			records: append(append([]sessionRecord(nil), prefix...), sessionRecord{
				Version: recordVersion, Sequence: 4, Type: recordCheckpoint,
				At: checkpoint.At, Data: json.RawMessage(`{`),
			}),
		},
		{
			name: "misplaced",
			records: []sessionRecord{prefix[0], func() sessionRecord {
				misplaced := checkpoint
				misplaced.Sequence = 2
				return misplaced
			}()},
		},
		{
			name: "sequence-breaking envelope",
			records: append(append([]sessionRecord(nil), prefix...), func() sessionRecord {
				broken := checkpoint
				broken.Sequence++
				return broken
			}()),
		},
		{
			name: "sequence-breaking projection",
			records: append(append([]sessionRecord(nil), prefix...), mutateCheckpoint(t, func(value *checkpointRecord) {
				value.State.Sequence--
			})),
		},
		{
			name: "open turn",
			records: append(append([]sessionRecord(nil), prefix...), mutateCheckpoint(t, func(value *checkpointRecord) {
				value.State.OpenTurn = "turn"
			})),
		},
		{
			name: "unsupported version",
			records: append(append([]sessionRecord(nil), prefix...), mutateCheckpoint(t, func(value *checkpointRecord) {
				value.Version++
			})),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := foldRecords(test.records); err == nil {
				t.Fatal("invalid checkpoint was accepted")
			}
		})
	}

	open := mustFold(t, prefix[:2])
	if _, err := newCheckpointRecord(open); err == nil {
		t.Fatal("checkpoint was created during an open turn")
	}
}

func timedRecord(
	t *testing.T,
	at time.Time,
	sequence uint64,
	kind string,
	value any,
) sessionRecord {
	t.Helper()
	record := mustRecord(t, sequence, kind, value)
	record.At = at
	return record
}

func mustFold(t *testing.T, records []sessionRecord) durableState {
	t.Helper()
	state, err := foldRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func mustRecord(t *testing.T, sequence uint64, kind string, value any) sessionRecord {
	t.Helper()
	record, err := newRecord(sequence, kind, value)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
