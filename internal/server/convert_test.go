package server

import (
	"encoding/json"
	"testing"

	"ox/internal/agent"
	"ox/internal/transcript"
)

func TestHookContextIsSentAsAUserMessage(t *testing.T) {
	userText := func(update any) string {
		t.Helper()
		var decoded struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct{ Text string }
		}
		data, _ := json.Marshal(update)
		if json.Unmarshal(data, &decoded) != nil || decoded.SessionUpdate != "user_message_chunk" {
			t.Fatalf("update = %s, want a user message", data)
		}
		return decoded.Content.Text
	}
	if got := userText(update(agent.HookContext("live"))); got != "live" {
		t.Errorf("live update text = %q", got)
	}
	batch := &transcript.AssistantBatch{
		Message:     transcript.AssistantMessage{ToolCalls: []transcript.ToolCall{{CallID: "c", Name: "shell", Arguments: "{}"}}},
		Outcomes:    []transcript.ToolOutcome{transcript.Completed("out")},
		HookContext: []string{"one", "two"},
	}
	var sent []any
	if err := replay([]transcript.Entry{batch}, func(u any) error { sent = append(sent, u); return nil }); err != nil {
		t.Fatal(err)
	}
	if got := userText(sent[len(sent)-1]); got != transcript.HookMessage([]string{"one", "two"}) {
		t.Errorf("replayed text = %q", got)
	}

	message := transcript.TextMessage("Hello")
	start := &transcript.TurnStart{Input: transcript.TurnInput{Message: &message}, HookContext: []string{"facts"}}
	sent = nil
	if err := replay([]transcript.Entry{start}, func(u any) error { sent = append(sent, u); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || userText(sent[0]) != "Hello" || userText(sent[1]) != "\n\n"+transcript.HookMessage([]string{"facts"}) {
		t.Errorf("replayed turn start = %v", sent)
	}
}
