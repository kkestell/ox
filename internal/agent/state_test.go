package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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
	if usage.Used != 16 || usage.Cost == nil ||
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

func mustRecord(t *testing.T, sequence uint64, kind string, value any) sessionRecord {
	t.Helper()
	record, err := newRecord(sequence, kind, value)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
