package agent

import (
	"encoding/json"
	"reflect"
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

func TestEstimateRequestConservativelySizesAdversarialText(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("!@#$%^&*()_+{}[]:;,.?/|", 40),
		strings.Repeat("漢字🙂e\u0301", 100),
	} {
		request := admissionTestRequest([]openrouter.Message{
			textMessage(openrouter.RoleUser, text),
		})
		occupancy, _, err := estimateProviderRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		if occupancy < len(text) {
			t.Fatalf("occupancy = %d, want at least %d source bytes", occupancy, len(text))
		}
		if _, err := planRequestAdmission(request, len(text)/2); err == nil {
			t.Fatalf("dense %q request fit an undersized context", text[:min(len(text), 12)])
		}
	}
}

func TestRequestAdmissionIncludesPendingPrompt(t *testing.T) {
	request := admissionTestRequest([]openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, strings.Repeat("older ", 1400)),
	})
	base, _, err := estimateProviderRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	window := base * 100 / 79
	withoutPending, err := planRequestAdmission(request, window)
	if err != nil {
		t.Fatal(err)
	}
	if withoutPending.plan != nil {
		t.Fatal("request compacted before the pending prompt was added")
	}
	request.Messages = append(
		request.Messages,
		textMessage(openrouter.RoleUser, strings.Repeat("pending ", 250)),
	)
	withPending, err := planRequestAdmission(request, window)
	if err != nil {
		t.Fatal(err)
	}
	if withPending.plan == nil {
		t.Fatalf("pending prompt did not trigger compaction: occupancy=%d window=%d", withPending.occupancy, window)
	}
}

func TestRequestAdmissionIncludesToolSchemas(t *testing.T) {
	request := admissionTestRequest([]openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, strings.Repeat("older ", 1200)),
		textMessage(openrouter.RoleUser, "continue"),
	})
	withoutTools, _, err := estimateProviderRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	window := withoutTools * 100 / 79
	admission, err := planRequestAdmission(request, window)
	if err != nil || admission.plan != nil {
		t.Fatalf("admission without tools = %#v, %v", admission, err)
	}
	request.Tools = []openrouter.Tool{{
		Type: "function",
		Function: openrouter.ToolFunction{
			Name:        "read_file",
			Description: strings.Repeat("long tool description ", 100),
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	}}
	admission, err = planRequestAdmission(request, window)
	if err != nil {
		t.Fatal(err)
	}
	if admission.plan == nil {
		t.Fatal("tool schema did not trigger compaction")
	}
}

func TestRequestAdmissionReservesConfiguredOutput(t *testing.T) {
	request := admissionTestRequest([]openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, strings.Repeat("older ", 1200)),
		textMessage(openrouter.RoleUser, "continue"),
	})
	occupancy, _, err := estimateProviderRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	window := occupancy * 100 / 79
	admission, err := planRequestAdmission(request, window)
	if err != nil || admission.plan != nil {
		t.Fatalf("admission without reserve = %#v, %v", admission, err)
	}
	reserve := window / 10
	request.MaxTokens = &reserve
	admission, err = planRequestAdmission(request, window)
	if err != nil {
		t.Fatal(err)
	}
	if admission.plan == nil {
		t.Fatal("configured output reserve did not trigger compaction")
	}
}

func TestRequestAdmissionRejectsCheckedOverflow(t *testing.T) {
	maximum := int(^uint(0) >> 1)
	request := admissionTestRequest([]openrouter.Message{
		textMessage(openrouter.RoleUser, "prompt"),
	})
	request.MaxTokens = &maximum
	if _, err := planRequestAdmission(request, maximum); err == nil ||
		!strings.Contains(err.Error(), "integer overflow") {
		t.Fatalf("error = %v", err)
	}
	if shouldCompact(maximum, maximum) != true {
		t.Fatal("threshold arithmetic overflowed at the maximum int")
	}
}

func TestRequestAdmissionRejectsUnsupportedMultimodalSizing(t *testing.T) {
	for _, test := range []struct {
		name  string
		block openrouter.ContentBlock
		want  string
	}{
		{name: "image", block: openrouter.ContentBlock{Type: "image_url", ImageURL: "data:image/png;base64,aaaa"}, want: "image content"},
		{name: "audio", block: openrouter.ContentBlock{Type: "input_audio", AudioData: "aaaa", AudioFormat: "wav"}, want: "audio content"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := admissionTestRequest([]openrouter.Message{{
				Role: openrouter.RoleUser, Content: []openrouter.ContentBlock{test.block},
			}})
			if _, err := planRequestAdmission(request, 1000); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRequestAdmissionRejectsOversizedProtectedPrefix(t *testing.T) {
	reserve := 200
	request := admissionTestRequest([]openrouter.Message{
		textMessage(openrouter.RoleUser, strings.Repeat("protected ", 1000)),
		textMessage(openrouter.RoleAssistant, strings.Repeat("old ", 1000)),
		textMessage(openrouter.RoleUser, "recent"),
	})
	request.MaxTokens = &reserve
	if _, err := planRequestAdmission(request, 500); err == nil ||
		!strings.Contains(err.Error(), "protected prefix") {
		t.Fatalf("error = %v", err)
	}
}

func TestRequestAdmissionRejectsIrreducibleRecentGroup(t *testing.T) {
	request := admissionTestRequest([]openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, "older context"),
		{
			Role: openrouter.RoleAssistant,
			ToolCalls: []openrouter.ToolCall{{
				ID: "recent", Type: "function",
				Function: openrouter.ToolCallFunction{Name: "read_file", Arguments: `{}`},
			}},
		},
		{
			Role: openrouter.RoleTool, ToolCallID: "recent",
			Content: []openrouter.ContentBlock{{Type: "text", Text: strings.Repeat("x", 8000)}},
		},
	})
	if _, err := planRequestAdmission(request, 500); err == nil ||
		!strings.Contains(err.Error(), "newest complete message group") {
		t.Fatalf("error = %v", err)
	}
}

func TestCompactRequestValidatesPostSummaryFitWithoutMutation(t *testing.T) {
	request := admissionTestRequest([]openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, strings.Repeat("older ", 1000)),
		textMessage(openrouter.RoleUser, "recent"),
	})
	before := append([]openrouter.Message(nil), request.Messages...)
	planned, err := planRequestAdmission(request, 500)
	if err != nil {
		t.Fatal(err)
	}
	if planned.plan == nil {
		t.Fatal("expected a compaction plan")
	}
	if _, err := compactRequest(
		request,
		*planned.plan,
		strings.Repeat("oversized summary ", 1000),
		500,
	); err == nil || !strings.Contains(err.Error(), "summary is too large") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(request.Messages, before) {
		t.Fatalf("request changed after failed compaction: %#v", request.Messages)
	}
	if _, err := compactRequest(request, *planned.plan, "   ", 500); err == nil ||
		!strings.Contains(err.Error(), "empty summary") {
		t.Fatalf("empty summary error = %v", err)
	}
	if !reflect.DeepEqual(request.Messages, before) {
		t.Fatalf("request changed after empty summary: %#v", request.Messages)
	}
}

func TestCompactRequestPreservesCompleteRecentToolGroup(t *testing.T) {
	request := admissionTestRequest([]openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, strings.Repeat("older ", 1000)),
		{
			Role: openrouter.RoleAssistant,
			ToolCalls: []openrouter.ToolCall{{
				ID: "recent", Type: "function",
				Function: openrouter.ToolCallFunction{Name: "read_file", Arguments: `{}`},
			}},
		},
		{Role: openrouter.RoleTool, ToolCallID: "recent", Content: []openrouter.ContentBlock{{Type: "text", Text: "result"}}},
	})
	planned, err := planRequestAdmission(request, 500)
	if err != nil {
		t.Fatal(err)
	}
	if planned.plan == nil {
		t.Fatal("expected a compaction plan")
	}
	admitted, err := compactRequest(request, *planned.plan, "kept facts", 500)
	if err != nil {
		t.Fatal(err)
	}
	if got := admitted.request.Messages[len(admitted.request.Messages)-2:]; !reflect.DeepEqual(got, cloneMessages(request.Messages[len(request.Messages)-2:])) {
		t.Fatalf("recent tool group changed: %#v", got)
	}
	if !messageGroupBoundary(admitted.request.Messages, len(admitted.request.Messages)-2) {
		t.Fatal("compacted request starts its tail inside a tool group")
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

func admissionTestRequest(history []openrouter.Message) openrouter.Request {
	messages := []openrouter.Message{textMessage(openrouter.RoleSystem, "system instructions")}
	messages = append(messages, history...)
	return openrouter.Request{Model: "test/model", Messages: messages}
}
