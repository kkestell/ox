package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
	"github.com/kkestell/ox/internal/skills"
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

func TestReplayUsesFrozenMCPTitleAndRawName(t *testing.T) {
	configuration := requestConfiguration{MCPTools: []mcpToolConfiguration{
		{Name: "mcp__issues__create", ServerName: "Issue tracker", ToolName: "create", Title: "Create an issue"},
		{Name: "mcp__files__lookup", ServerName: "Files", ToolName: "lookup"},
	}}
	for _, test := range []struct {
		name string
		want string
	}{
		{"mcp__issues__create", "Create an issue"},
		{"mcp__files__lookup", "Files / lookup"},
	} {
		t.Run(test.name, func(t *testing.T) {
			call := openrouter.ToolCall{
				ID: "call", Type: "function",
				Function: openrouter.ToolCallFunction{Name: test.name, Arguments: `{}`},
			}
			update := replayToolCall(&Agent{}, call, configuration, "/workspace", "")
			if update.Title != test.want || update.Name != test.name || update.Meta[acp.MetaToolDisplayName] != test.want || update.Meta[acp.MetaToolDisplayArguments] != nil {
				t.Fatalf("replayed tool call = %#v", update)
			}
		})
	}
}

func TestSkillReferencesAreValidatedWithConfiguration(t *testing.T) {
	reference := skills.Reference{
		Name: "review", Description: "Review work.",
		Path: ".agents/skills/review/SKILL.md", Digest: strings.Repeat("0", 64),
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"}, Skills: []skills.Reference{reference},
	}
	if err := validateConfiguration(configuration); err != nil {
		t.Fatal(err)
	}
	configuration.Skills[0].Digest = "bad"
	if err := validateConfiguration(configuration); err == nil ||
		!strings.Contains(err.Error(), "configuration skills") {
		t.Fatalf("invalid skill reference error = %v", err)
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

func TestTodoReplacementFoldsAndReplays(t *testing.T) {
	call := openrouter.ToolCall{
		ID: "todo-call", Type: "function",
		Function: openrouter.ToolCallFunction{Name: "todo", Arguments: `{"todos":[]}`},
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"}, ContextWindow: 1000,
		Tools: []openrouter.Tool{{
			Type: "function", Function: openrouter.ToolFunction{
				Name: "todo", Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
		ToolKinds: map[string]acp.ToolKind{"todo": acp.ToolKindOther},
		PlanTools: map[string]bool{"todo": true},
	}
	entries := []acp.PlanEntry{{
		Content: "Implement state", Priority: acp.PlanEntryPriorityHigh,
		Status: acp.PlanEntryStatusInProgress,
	}}
	result := storedToolResult{CallID: call.ID, Content: "updated"}
	records := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: "0123456789abcdef0123456789abcdef", CWD: "/workspace",
			Configuration: configuration,
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "turn", MessageID: "user",
			Content: []acp.ContentBlock{{Type: "text", Text: "work"}},
		}),
		mustRecord(t, 3, recordExchangePaused, suspendedModelExchangeRecord{
			TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
			FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{call},
			RequestCount: 1,
		}),
		mustRecord(t, 4, recordToolStarted, toolStartedRecord{TurnID: "turn", Call: call}),
		mustRecord(t, 5, recordTodoChanged, todoChanged{
			TurnID: "turn", CallID: call.ID, Entries: entries,
		}),
		mustRecord(t, 6, recordToolCompleted, toolCompletedRecord{
			TurnID: "turn", CallID: call.ID, Result: result,
		}),
		mustRecord(t, 7, recordModelExchange, modelExchangeRecord{
			TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
			FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{call},
			ToolResults: []storedToolResult{result},
		}),
		mustRecord(t, 8, recordTurnFinished, turnFinishedRecord{
			TurnID: "turn", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	}
	assertImmutableTransitions(t, records)
	state := mustFold(t, records)
	if !reflect.DeepEqual(state.todo, entries) {
		t.Fatalf("todo = %#v, want %#v", state.todo, entries)
	}
	updates, err := (&Agent{}).replay(state)
	if err != nil {
		t.Fatal(err)
	}
	var plans []acp.Plan
	for _, update := range updates {
		if plan, ok := update.(acp.Plan); ok {
			plans = append(plans, plan)
		}
	}
	if len(plans) != 1 || !reflect.DeepEqual(plans[0].Entries, entries) {
		t.Fatalf("replayed plans = %#v", plans)
	}

	base := mustFold(t, records[:4])
	invalid := mustRecord(t, 5, recordTodoChanged, todoChanged{
		TurnID: "turn", CallID: call.ID,
		Entries: []acp.PlanEntry{
			{Content: "one", Priority: acp.PlanEntryPriorityMedium, Status: acp.PlanEntryStatusInProgress},
			{Content: "two", Priority: acp.PlanEntryPriorityMedium, Status: acp.PlanEntryStatusInProgress},
		},
	})
	if err := base.apply(invalid); err == nil || len(base.todo) != 0 || base.sequence != 4 {
		t.Fatalf("invalid replacement = error %v, todo %#v, sequence %d", err, base.todo, base.sequence)
	}
	orphan := mustRecord(t, 5, recordTodoChanged, todoChanged{
		TurnID: "turn", CallID: "other", Entries: []acp.PlanEntry{},
	})
	if err := base.apply(orphan); err == nil || len(base.todo) != 0 || base.sequence != 4 {
		t.Fatalf("orphan replacement = error %v, todo %#v, sequence %d", err, base.todo, base.sequence)
	}
}

func TestCompactionSurvivesRestartWithoutDoubleCountingUsage(t *testing.T) {
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
	restored, err := foldRecords(state.records)
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

func TestCompactionAcceptsOpenTurnBoundariesAndRejectsIncompleteToolGroups(t *testing.T) {
	records, summary := compactionFixture(t)
	base, err := foldRecords(records[:6])
	if err != nil {
		t.Fatal(err)
	}
	invalidBoundary := mustRecord(t, 7, recordCompaction, compactionRecord{
		TurnID: "second", HeadEnd: 1, TailStart: 4, Summary: summary, Occupancy: 222,
	})
	if err := base.apply(invalidBoundary); err == nil ||
		!strings.Contains(err.Error(), "separates a tool call") {
		t.Fatalf("incomplete tool group error = %v", err)
	}

	open := base.clone()
	if err := open.apply(mustRecord(t, 7, recordCompaction, compactionRecord{
		TurnID: "second", HeadEnd: 1, TailStart: 3, Summary: summary, Occupancy: 222,
	})); err != nil {
		t.Fatalf("open-turn compaction error = %v", err)
	}
	restored, err := foldRecords(open.records)
	if err != nil {
		t.Fatal(err)
	}
	if restored.openTurn != "second" || restored.suspended != nil ||
		!reflect.DeepEqual(open.history, restored.history) {
		t.Fatalf("restored open-turn compaction = %#v", restored)
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
		mustRecord(t, 7, recordCompaction, compactionRecord{
			TurnID:  "second",
			HeadEnd: 1, TailStart: 3, Summary: summary,
			Usage: &openrouter.Usage{
				PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, Cost: 0.3,
			},
			Occupancy: 222,
		}),
		mustRecord(t, 8, recordTurnFinished, turnFinishedRecord{
			TurnID: "second", Kind: "completed",
			StopReason: acp.StopReasonEndTurn,
		}),
	}, summary
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
			Title: "Edit", Kind: acp.ToolKindEdit,
			Locations: []acp.ToolCallLocation{{Path: "/workspace/a.go"}},
			RawInput:  json.RawMessage(call.Function.Arguments),
			Meta:      acp.Metadata{acp.MetaToolDisplayName: "Edit", acp.MetaToolDisplayArguments: "a.go"},
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
	if tool := state.suspended.Pending.Request.ToolCall; tool.Title != "Edit" || tool.Name != "edit" || tool.Meta[acp.MetaToolDisplayName] != "Edit" || tool.Meta[acp.MetaToolDisplayArguments] != "a.go" {
		t.Fatalf("recovered permission tool call = %#v", tool)
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
		mustRecord(t, 7, recordToolStarted, toolStartedRecord{
			TurnID: "turn", Call: call, ApprovalDecision: decisionAllowOnce, Target: "a.go",
		}),
		mustRecord(t, 8, recordToolCompleted, toolCompletedRecord{
			TurnID: "turn", CallID: call.ID,
			Result: storedToolResult{
				CallID: call.ID, Content: "done",
				ApprovalDecision: decisionAllowOnce, Target: "a.go",
			},
		}),
		mustRecord(t, 9, recordModelExchange, modelExchangeRecord{
			TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
			Text: "working", FinishReason: "tool_calls",
			ToolCalls: []openrouter.ToolCall{call},
			ToolResults: []storedToolResult{{
				CallID: call.ID, Content: "done",
				ApprovalDecision: decisionAllowOnce, Target: "a.go",
			}},
		}),
		mustRecord(t, 10, recordTurnFinished, turnFinishedRecord{
			TurnID: "turn", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	)
	assertImmutableTransitions(t, records)
	state = mustFold(t, records)
	if state.suspended != nil || state.openTurn != "" || len(state.history) != 3 {
		t.Fatalf("completed state = %#v", state)
	}
	if _, changed := state.changedFiles["a.go"]; !changed {
		t.Fatal("recovered edit was not recorded as changed")
	}
	instance, err := New(Config{Tools: []Tool{{Name: "edit", Kind: acp.ToolKindEdit, Execute: testToolExecutor}}})
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

func TestFoldRejectsInvalidToolExecutionLifecycle(t *testing.T) {
	call := openrouter.ToolCall{
		ID: "call", Type: "function",
		Function: openrouter.ToolCallFunction{Name: "mutation", Arguments: `{}`},
	}
	base := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: "0123456789abcdef0123456789abcdef", CWD: "/workspace",
			Configuration: requestConfiguration{
				Settings: settings.Resolved{Model: "test/model"},
				Tools: []openrouter.Tool{{Type: "function", Function: openrouter.ToolFunction{
					Name: "mutation", Parameters: json.RawMessage(`{"type":"object"}`),
				}}},
			},
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "turn", MessageID: "user",
			Content: []acp.ContentBlock{{Type: "text", Text: "run it"}},
		}),
		mustRecord(t, 3, recordExchangePaused, suspendedModelExchangeRecord{
			TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
			FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{call}, RequestCount: 1,
		}),
	}
	started := mustRecord(t, 4, recordToolStarted, toolStartedRecord{TurnID: "turn", Call: call})
	tests := []struct {
		name    string
		records []sessionRecord
	}{
		{
			name: "completion before dispatch",
			records: append(append([]sessionRecord(nil), base...),
				mustRecord(t, 4, recordToolCompleted, toolCompletedRecord{
					TurnID: "turn", CallID: call.ID,
					Result: storedToolResult{CallID: call.ID, Content: "done"},
				})),
		},
		{
			name: "duplicate dispatch",
			records: append(append(append([]sessionRecord(nil), base...), started),
				mustRecord(t, 5, recordToolStarted, toolStartedRecord{TurnID: "turn", Call: call})),
		},
		{
			name: "invalid unknown outcome",
			records: append(append(append([]sessionRecord(nil), base...), started),
				mustRecord(t, 5, recordToolCompleted, toolCompletedRecord{
					TurnID: "turn", CallID: call.ID,
					Result: storedToolResult{CallID: call.ID, Content: unknownToolOutcome, Unknown: true},
				})),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := foldRecords(test.records); err == nil {
				t.Fatal("invalid lifecycle was accepted")
			}
		})
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

	changed := configuration
	changed.ExecutorCapabilities = executorCapabilities{FileSystemWrite: true}
	if sameRequestConfiguration(configuration, changed) {
		t.Fatal("configuration equality ignored executor capabilities")
	}
	semanticallySame := configuration
	semanticallySame.Tools = []openrouter.Tool{}
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

func TestFoldRestoresConfigurationHistoryUsageAndIdentity(t *testing.T) {
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
	changed := configuration
	changed.Settings.Model = "test/second"
	changed.SystemPrompt = "second system prompt"
	changed.ExecutorCapabilities = executorCapabilities{
		FileSystemWrite: true,
		Terminal:        true,
	}
	fullRecords := slices.Clone(prefix)
	fullRecords = append(fullRecords,
		timedRecord(t, baseTime.Add(5*time.Second), 5, recordConfigChanged, configurationChanged{
			Configuration: changed,
		}),
		timedRecord(t, baseTime.Add(6*time.Second), 6, recordUserMessage, userMessageRecord{
			TurnID: "second-turn", MessageID: "second-user",
			Content: []acp.ContentBlock{{Type: "text", Text: "summarize"}},
		}),
		timedRecord(t, baseTime.Add(7*time.Second), 7, recordModelExchange, modelExchangeRecord{
			TurnID: "second-turn", AnswerID: "second-answer", ThoughtID: "second-thought",
			Text: "summary", FinishReason: "stop",
			Usage: &openrouter.Usage{
				PromptTokens: 11, CompletionTokens: 4, TotalTokens: 15, Cost: 0.5,
			},
		}),
		timedRecord(t, baseTime.Add(8*time.Second), 8, recordTurnFinished, turnFinishedRecord{
			TurnID: "second-turn", Kind: "completed", StopReason: acp.StopReasonEndTurn,
		}),
	)

	assertImmutableTransitions(t, fullRecords)
	restored := mustFold(t, fullRecords)
	if restored.configuration.Settings.Model != "test/second" ||
		restored.configuration.ExecutorCapabilities != changed.ExecutorCapabilities ||
		restored.usage.input != 18 || restored.usage.output != 9 ||
		restored.usage.thought != 3 || restored.usage.cachedRead != 2 ||
		restored.usage.cachedWrite != 1 || restored.cost != 0.75 ||
		restored.title != "inspect the workspace" {
		t.Fatalf("restored projection = %#v", restored)
	}
	if _, exists := restored.messageIDs["first-answer"]; !exists {
		t.Fatal("fold omitted a recorded message identity")
	}
	if _, exists := restored.toolCallIDs["first-call"]; !exists {
		t.Fatal("fold omitted a recorded tool-call identity")
	}

	covered := slices.Clone(fullRecords)
	covered[1].Data = json.RawMessage(`{}`)
	if _, err := foldRecords(covered); err == nil {
		t.Fatal("invalid authoritative record was skipped")
	}
}

func TestFoldRejectsUnsupportedCheckpoint(t *testing.T) {
	records := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: "0123456789abcdef0123456789abcdef", CWD: "/workspace",
			Configuration: requestConfiguration{Settings: settings.Resolved{Model: "test/model"}},
		}),
		mustRecord(t, 2, "checkpoint", map[string]any{"version": 11}),
	}
	if _, err := foldRecords(records); err == nil || !strings.Contains(err.Error(), "unsupported record type") {
		t.Fatalf("checkpoint = %v", err)
	}
}

func timedRecord(t *testing.T, at time.Time, sequence uint64, kind string, value any) sessionRecord {
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

func assertImmutableTransitions(t *testing.T, records []sessionRecord) {
	t.Helper()
	encode := func(s durableState) []byte {
		data, err := json.Marshal([]any{s.history, s.configuration, s.todo, s.suspended,
			s.toolExecutions, s.messageIDs, s.toolCallIDs, s.changedFiles, s.openTurnBase, s.records})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var state durableState
	var held []durableState
	var snapshots [][]byte
	for _, record := range records {
		held = append(held, state)
		snapshots = append(snapshots, encode(state))
		next := state.clone()
		if err := next.apply(record); err != nil {
			t.Fatal(err)
		}
		state = next
		for index, previous := range held {
			if !bytes.Equal(encode(previous), snapshots[index]) {
				t.Fatalf("record %d (%s) rewrote snapshot %d", record.Sequence, record.Type, index)
			}
		}
	}
}

func TestImmutableHistoryAcrossCompactionAndRefusal(t *testing.T) {
	records, _ := compactionFixture(t)
	records = append(records,
		mustRecord(t, 9, recordUserMessage, userMessageRecord{TurnID: "refused", MessageID: "refused-user", Content: []acp.ContentBlock{{Type: "text", Text: "refuse"}}}),
		mustRecord(t, 10, recordModelExchange, modelExchangeRecord{TurnID: "refused", AnswerID: "refused-answer", ThoughtID: "refused-thought", Text: "no", FinishReason: "refusal"}),
		mustRecord(t, 11, recordTurnFinished, turnFinishedRecord{TurnID: "refused", Kind: "refusal"}),
		mustRecord(t, 12, recordUserMessage, userMessageRecord{TurnID: "next", MessageID: "next-user", Content: []acp.ContentBlock{{Type: "text", Text: "continue"}}}),
		mustRecord(t, 13, recordModelExchange, modelExchangeRecord{TurnID: "next", AnswerID: "next-answer", ThoughtID: "next-thought", Text: "yes", FinishReason: "stop"}),
	)
	assertImmutableTransitions(t, records)
}

func TestAccumulatedStateDoesNotBoundTurnCompletion(t *testing.T) {
	instance, err := New(Config{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	value := durableTestSession(t, instance, testConfiguration(instance), "large")
	for index := range 9 {
		id := strconv.Itoa(index)
		if err := instance.commit(value, recordModelExchange, modelExchangeRecord{
			TurnID: "large", AnswerID: "answer" + id, ThoughtID: "thought" + id,
			Text: strings.Repeat("x", 1<<20), FinishReason: "stop",
			Usage: &openrouter.Usage{PromptTokens: index + 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := instance.commit(value, recordTurnFinished, turnFinishedRecord{TurnID: "large", Kind: "completed"}); err != nil {
		t.Fatal(err)
	}
	records, _, err := readRecords(value.log.file)
	if err != nil {
		t.Fatal(err)
	}
	restored := mustFold(t, records)
	if restored.openTurn != "" || len(restored.history) != 10 || restored.occupancy != 9 {
		t.Fatalf("restored history = %d, open turn = %q, occupancy = %d", len(restored.history), restored.openTurn, restored.occupancy)
	}
	for _, record := range records {
		if record.Type == "checkpoint" {
			t.Fatal("checkpoint was written")
		}
	}
}

func TestProviderRequestCountAdvancesAndIsCloned(t *testing.T) {
	state := durableState{sequence: 1, openTurn: "turn"}
	if err := state.apply(mustRecord(t, 2, recordProviderStarted, providerRequestStarted{
		TurnID: "turn", Count: 1,
	})); err != nil {
		t.Fatal(err)
	}
	cloned := state.clone()
	if err := state.apply(mustRecord(t, 3, recordProviderStarted, providerRequestStarted{
		TurnID: "turn", Count: 2,
	})); err != nil {
		t.Fatal(err)
	}
	if cloned.turnRequests != 1 || state.turnRequests != 2 {
		t.Fatalf("request counts = clone %d, state %d", cloned.turnRequests, state.turnRequests)
	}
}

func TestSubagentUsageAdvancesTotalsWithoutHistory(t *testing.T) {
	state := durableState{sequence: 1, openTurn: "turn"}
	usage := &openrouter.Usage{
		PromptTokens: 7, CompletionTokens: 5, TotalTokens: 12, Cost: 0.25,
	}
	if err := state.apply(mustRecord(t, 2, recordSubagentUsage, subagentUsageRecord{
		TurnID: "turn", SubagentID: "child", Usage: usage, Occupancy: 7,
	})); err != nil {
		t.Fatal(err)
	}
	if len(state.history) != 0 || state.usage.input != 7 || state.usage.output != 5 ||
		state.cost != 0.25 {
		t.Fatalf("state = history %#v, usage %#v, cost %g", state.history, state.usage, state.cost)
	}
	before := state.clone()
	if err := state.apply(mustRecord(t, 3, recordSubagentUsage, subagentUsageRecord{
		TurnID: "other", SubagentID: "child", Usage: usage, Occupancy: 7,
	})); err == nil || state.usage != before.usage {
		t.Fatalf("mismatched usage = error %v, state %#v", err, state.usage)
	}
}

func mustRecord(t *testing.T, sequence uint64, kind string, value any) sessionRecord {
	t.Helper()
	record, err := newRecord(sequence, kind, value)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// BenchmarkCommitCopyAtSessionSize measures the copy every durable commit makes
// before it applies a record. The cost must stay flat as a session accumulates
// records and history, or the session's total commit work grows with its square.
func BenchmarkCommitCopyAtSessionSize(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			state := agedState(size)
			b.ReportAllocs()
			for b.Loop() {
				next := state.clone()
				if len(next.history) != size {
					b.Fatalf("cloned history = %d, want %d", len(next.history), size)
				}
			}
		})
	}
}

func BenchmarkLongSessionLoad(b *testing.B) {
	for _, turns := range []int{100, 1000, 5000} {
		b.Run(strconv.Itoa(turns), func(b *testing.B) {
			var state durableState
			add := func(kind string, payload any) {
				record, err := newRecord(state.sequence+1, kind, payload)
				if err != nil {
					b.Fatal(err)
				}
				if err := state.apply(record); err != nil {
					b.Fatal(err)
				}
			}
			add(recordSessionCreated, sessionCreated{
				SessionID: "0123456789abcdef0123456789abcdef", CWD: "/workspace",
				Configuration: requestConfiguration{Settings: settings.Resolved{Model: "test/model"}},
			})
			for index := range turns {
				id := strconv.Itoa(index)
				add(recordUserMessage, userMessageRecord{
					TurnID: id, MessageID: "u" + id,
					Content: []acp.ContentBlock{{Type: "text", Text: "continue"}},
				})
				add(recordModelExchange, modelExchangeRecord{
					TurnID: id, AnswerID: "a" + id, ThoughtID: "r" + id,
					Text: strings.Repeat("recorded output ", 64), FinishReason: "stop",
					Usage: &openrouter.Usage{PromptTokens: 100, CompletionTokens: 10},
				})
				add(recordTurnFinished, turnFinishedRecord{TurnID: id, Kind: "completed"})
			}
			file, err := os.CreateTemp(b.TempDir(), "session")
			if err != nil {
				b.Fatal(err)
			}
			defer file.Close()
			for _, record := range state.records {
				data, err := encodeRecord(record)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := file.Write(data); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				records, _, err := readRecords(file)
				if err != nil {
					b.Fatal(err)
				}
				restored, err := foldRecords(records)
				if err != nil || len(restored.history) != turns*2 {
					b.Fatalf("history = %d, error = %v", len(restored.history), err)
				}
			}
		})
	}
}

func agedState(size int) durableState {
	state := durableState{
		id:            "0123456789abcdef0123456789abcdef",
		cwd:           "/workspace",
		createdAt:     time.Unix(0, 0).UTC(),
		updatedAt:     time.Unix(0, 0).UTC(),
		configuration: requestConfiguration{Settings: settings.Resolved{Model: "test/model"}},
		messageIDs:    map[string]struct{}{},
		toolCallIDs:   map[string]struct{}{},
		changedFiles:  map[string]struct{}{},
	}
	for index := range size {
		state.records = append(state.records, sessionRecord{
			Version:  recordVersion,
			Sequence: uint64(index + 1),
			Type:     recordTurnFinished,
			At:       time.Unix(int64(index), 0).UTC(),
			Data:     json.RawMessage(`{"turnId":"t"}`),
		})
		state.history = append(state.history, openrouter.Message{
			Role: openrouter.RoleAssistant,
			Content: []openrouter.ContentBlock{{
				Type: "text",
				Text: strings.Repeat("a recorded sentence of model output. ", 8),
			}},
		})
		state.messageIDs[strconv.Itoa(index)] = struct{}{}
	}
	return state
}

// TestOutputTailKeepsValidOutputAroundBadBytes covers the retained tool-output
// tail: a byte cut can split a rune, and a stream can carry a stray invalid
// byte. Neither may cost the valid output around it.
func TestOutputTailKeepsValidOutputAroundBadBytes(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "short output is untouched", value: "hello", want: "hello"},
		{
			name:  "an interior bad byte is replaced, not truncated to",
			value: "before\xffafter",
			want:  "before�after",
		},
		{
			// The cut lands inside the leading rune, so only its bytes go.
			name:  "a cut mid-rune drops only that rune",
			value: "é" + strings.Repeat("a", maxToolOutputTail-1),
			want:  strings.Repeat("a", maxToolOutputTail-1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := outputTail(test.value); got != test.want {
				t.Fatalf("outputTail = %q, want %q", got, test.want)
			}
		})
	}

	// A run of small chunks must stay bounded and keep the newest output.
	var tail []byte
	for index := range 4 * maxToolOutputTail {
		tail = appendOutput(tail, string(rune('a'+index%26)))
	}
	if len(tail) > 2*maxToolOutputTail {
		t.Fatalf("accumulated tail = %d bytes, want at most %d", len(tail), 2*maxToolOutputTail)
	}
	bounded := outputTail(string(tail))
	if len(bounded) != maxToolOutputTail {
		t.Fatalf("flushed tail = %d bytes, want %d", len(bounded), maxToolOutputTail)
	}
	if want := string(rune('a' + (4*maxToolOutputTail-1)%26)); !strings.HasSuffix(bounded, want) {
		t.Fatalf("flushed tail ends %q, want it to end with the newest output %q", bounded, want)
	}

	// A single chunk larger than the bound is still trimmed to it.
	if got := outputTail(string(appendOutput(nil, strings.Repeat("x", 3*maxToolOutputTail)))); len(got) != maxToolOutputTail {
		t.Fatalf("oversized chunk tail = %d bytes, want %d", len(got), maxToolOutputTail)
	}
}

// TestListProjectionAgreesWithAFullFold pins the projection to the state it
// stands in for. A listing that disagreed on the timestamp would hand out
// cursors the next page rejects as stale.
func TestListProjectionAgreesWithAFullFold(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	cwd := t.TempDir()
	configuration := requestConfiguration{Settings: settings.Resolved{Model: "test/model"}}
	records := []sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: id, CWD: cwd, Configuration: configuration,
		}),
		mustRecord(t, 2, recordUserMessage, userMessageRecord{
			TurnID: "turn", MessageID: "message",
			Content:       []acp.ContentBlock{{Type: "text", Text: "first prompt"}},
			Configuration: configuration,
		}),
		mustRecord(t, 3, recordTurnFinished, turnFinishedRecord{
			TurnID: "turn", Kind: "completed", StopReason: "end_turn", MessageID: "outcome",
		}),
	}
	for _, test := range []struct {
		name    string
		records []sessionRecord
	}{
		{name: "finished turn", records: records},
		{name: "open turn", records: records[:2]},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := foldRecords(test.records)
			if err != nil {
				t.Fatal(err)
			}
			projected, err := foldListProjection(test.records)
			if err != nil {
				t.Fatal(err)
			}
			if projected.id != state.id || projected.cwd != state.cwd ||
				projected.title != state.title || !projected.updatedAt.Equal(state.updatedAt) {
				t.Fatalf("projection = %#v, folded = %s %s %q %s",
					projected, state.id, state.cwd, state.title, state.updatedAt)
			}
		})
	}
}

// TestTurnConfigurationIsSharedButNeverRewritten pins the invariant that lets a
// turn read its frozen configuration without copying it: a commit builds a
// successor rather than writing through the configuration a reader holds.
func TestTurnConfigurationIsSharedButNeverRewritten(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	cwd := t.TempDir()
	configuration := requestConfiguration{
		Settings:  settings.Resolved{Model: "test/model"},
		Tools:     []openrouter.Tool{{Type: "function", Function: openrouter.ToolFunction{Name: "read"}}},
		ToolKinds: map[string]acp.ToolKind{"read": acp.ToolKindRead},
	}
	state, err := foldRecords([]sessionRecord{
		mustRecord(t, 1, recordSessionCreated, sessionCreated{
			SessionID: id, CWD: cwd, Configuration: configuration,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	held := state.turnConfiguration()
	if len(held.ToolKinds) != 1 {
		t.Fatalf("configuration = %#v", held)
	}

	replacement := configuration
	replacement.Tools = nil
	replacement.ToolKinds = map[string]acp.ToolKind{}
	next := state.clone()
	if err := next.apply(mustRecord(t, 2, recordConfigChanged, configurationChanged{
		Configuration: replacement,
	})); err != nil {
		t.Fatal(err)
	}

	if len(held.Tools) != 1 || held.ToolKinds["read"] != acp.ToolKindRead {
		t.Fatalf("a commit rewrote a configuration a reader was holding: %#v", held)
	}
	if state.turnToolKind("read") != acp.ToolKindRead {
		t.Fatal("a commit rewrote the state it succeeded")
	}
	if next.turnToolKind("read") != "" {
		t.Fatalf("successor kept the replaced tool kind: %#v", next.configuration)
	}
}
