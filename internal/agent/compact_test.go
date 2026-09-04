package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/openrouter"
)

func TestCompactionThreshold(t *testing.T) {
	if shouldCompact(799, 1000) {
		t.Fatal("compaction started below 80 percent")
	}
	if !shouldCompact(800, 1000) {
		t.Fatal("compaction did not start at 80 percent")
	}
	if shouldCompact(0, 1000) || shouldCompact(800, 0) {
		t.Fatal("compaction started without measured occupancy and a context window")
	}
}

func TestPlanCompactionKeepsFirstUserAndCompleteToolTail(t *testing.T) {
	messages := []openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, strings.Repeat("older ", 1000)),
		{
			Role: openrouter.RoleAssistant,
			ToolCalls: []openrouter.ToolCall{{
				ID: "recent", Type: "function",
				Function: openrouter.ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.go"}`},
			}},
		},
		{
			Role: openrouter.RoleTool, ToolCallID: "recent",
			Content: []openrouter.ContentBlock{{Type: "text", Text: "package a"}},
		},
	}
	plan := planCompaction(messages, 4000)
	if plan == nil {
		t.Fatal("expected a compaction plan")
	}
	if plan.headEnd != 1 || plan.tailStart != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	if !messageGroupBoundary(messages, plan.headEnd) ||
		!messageGroupBoundary(messages, plan.tailStart) {
		t.Fatalf("plan separates a message group: %#v", plan)
	}
}

func TestPlanCompactionKeepsLatestGroupPastBudget(t *testing.T) {
	messages := []openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, strings.Repeat("older ", 1000)),
		textMessage(openrouter.RoleUser, "recent request"),
	}
	plan := planCompaction(messages, 1)
	if plan == nil || plan.tailStart != 2 {
		t.Fatalf("plan = %#v, want the latest message retained", plan)
	}
}

func TestPlanCompactionRejectsNoUsefulMiddle(t *testing.T) {
	messages := []openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleUser, "Summary of earlier conversation:\n\nsmall summary"),
		{
			Role: openrouter.RoleAssistant,
			ToolCalls: []openrouter.ToolCall{{
				ID: "recent", Type: "function",
				Function: openrouter.ToolCallFunction{Name: "read_file", Arguments: `{"path":"large"}`},
			}},
		},
		{
			Role: openrouter.RoleTool, ToolCallID: "recent",
			Content: []openrouter.ContentBlock{{Type: "text", Text: strings.Repeat("x", 8000)}},
		},
	}
	if plan := planCompaction(messages, 4000); plan != nil {
		t.Fatalf("plan = %#v, want no ineffective compaction", plan)
	}
}

func TestEstimateRequestIncludesProtectedPrefix(t *testing.T) {
	history := []openrouter.Message{textMessage(openrouter.RoleUser, "prompt")}
	withoutPrefix := estimateRequestTokens("", nil, history)
	withSystem := estimateRequestTokens(strings.Repeat("system", 100), nil, history)
	withTools := estimateRequestTokens("", []openrouter.Tool{{
		Type: "function",
		Function: openrouter.ToolFunction{
			Name: "read_file", Description: strings.Repeat("description", 100),
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
	}}, history)
	if withSystem <= withoutPrefix || withTools <= withoutPrefix {
		t.Fatalf(
			"estimates without/system/tools = %d/%d/%d",
			withoutPrefix,
			withSystem,
			withTools,
		)
	}
}

func TestRenderCompactionTranscriptOmitsReasoningAndBinaryContent(t *testing.T) {
	messages := []openrouter.Message{
		{
			Role: openrouter.RoleAssistant,
			Content: []openrouter.ContentBlock{
				{Type: "text", Text: `find "needle"`},
				{Type: "image_url", ImageURL: "data:image/png;base64,secret-image"},
				{Type: "input_audio", AudioData: "secret-audio", AudioFormat: "wav"},
			},
			ReasoningDetails: []json.RawMessage{json.RawMessage(`{"text":"private reasoning"}`)},
			ToolCalls: []openrouter.ToolCall{{
				ID: "grep", Type: "function",
				Function: openrouter.ToolCallFunction{Name: "grep", Arguments: `{"pattern":"needle"}`},
			}},
		},
		textMessage(openrouter.RoleTool, "found it"),
	}
	transcript := renderCompactionTranscript(messages)
	for _, secret := range []string{"secret-image", "secret-audio", "private reasoning"} {
		if strings.Contains(transcript, secret) {
			t.Fatalf("transcript contains %q: %s", secret, transcript)
		}
	}
	for _, text := range []string{
		`[assistant] find "needle"`,
		`calls grep({"pattern":"needle"})`,
		"[tool] found it",
	} {
		if !strings.Contains(transcript, text) {
			t.Fatalf("transcript omitted %q: %s", text, transcript)
		}
	}
}

func textMessage(role openrouter.Role, text string) openrouter.Message {
	return openrouter.Message{
		Role:    role,
		Content: []openrouter.ContentBlock{{Type: "text", Text: text}},
	}
}
