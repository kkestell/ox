package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
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

func TestCompactionKeepsTrailingTodoContextOutOfTheSplice(t *testing.T) {
	todo := todoContextMessage([]acp.PlanEntry{{
		Content: "keep this", Priority: acp.PlanEntryPriorityMedium,
		Status: acp.PlanEntryStatusInProgress,
	}})
	messages := []openrouter.Message{
		textMessage(openrouter.RoleSystem, "instructions"),
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, strings.Repeat("older ", 1000)),
		textMessage(openrouter.RoleUser, "continue"),
		todo,
	}
	request := openrouter.Request{Model: "test/model", Messages: messages}
	plan := planCompaction(messages, 4000)
	if plan == nil || plan.headEnd != 2 || plan.tailStart != 3 {
		t.Fatalf("plan = %#v, want the newest history group retained", plan)
	}
	compacted, err := compactRequest(request, *plan, "summary", 4000)
	if err != nil {
		t.Fatal(err)
	}
	gotTodo := compacted.request.Messages[len(compacted.request.Messages)-1]
	if gotTodo.Role != todo.Role || len(gotTodo.Content) != 1 ||
		gotTodo.Content[0].Text != todo.Content[0].Text {
		t.Fatalf("todo context did not stay last: %#v", compacted.request.Messages)
	}
	retained := compacted.request.Messages[len(compacted.request.Messages)-2]
	if retained.Content[0].Text != "continue" {
		t.Fatalf("newest retained message = %#v, want the newest history group", retained)
	}
	if _, err := compactRequest(
		request, compactionPlan{headEnd: 2, tailStart: 4}, "summary", 4000,
	); err == nil {
		t.Fatal("compaction retained the todo context in place of history")
	}
}

// A large protected prefix forces the newest-complete-group fallback. The
// trailing todo context is not a message group, so the fallback retains the
// newest history group rather than discarding the conversation for it.
func TestRequestAdmissionFallbackRetainsNewestHistoryGroup(t *testing.T) {
	todo := todoContextMessage([]acp.PlanEntry{{
		Content: "keep this", Priority: acp.PlanEntryPriorityMedium,
		Status: acp.PlanEntryStatusInProgress,
	}})
	request := openrouter.Request{
		Model: "test/model",
		Messages: []openrouter.Message{
			textMessage(openrouter.RoleSystem, strings.Repeat("s", 16_400)),
			textMessage(openrouter.RoleUser, "do the task"),
			textMessage(openrouter.RoleAssistant, strings.Repeat("a", 5_000)),
			textMessage(openrouter.RoleUser, strings.Repeat("b", 2_400)),
			textMessage(openrouter.RoleUser, strings.Repeat("c", 1_400)),
			todo,
		},
	}
	planned, err := planRequestAdmission(request, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if planned.plan == nil || planned.plan.headEnd != 2 || planned.plan.tailStart != 4 {
		t.Fatalf("plan = %#v, want the newest history group retained", planned.plan)
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

// TestEstimateRequestSizesDenseTextInTokens checks that an estimate is a token
// count derived from the serialized request rather than its byte count, and
// that the conversion still leaves headroom for text far denser than prose.
func TestEstimateRequestSizesDenseTextInTokens(t *testing.T) {
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
		if occupancy < len(text)/bytesPerToken {
			t.Fatalf("occupancy = %d, want at least %d tokens for %d dense bytes",
				occupancy, len(text)/bytesPerToken, len(text))
		}
		if occupancy >= len(text) {
			t.Fatalf("occupancy = %d for %d bytes, want a token count rather than a byte count",
				occupancy, len(text))
		}
		if _, err := planRequestAdmission(request, occupancy/2); err == nil {
			t.Fatalf("dense %q request fit an undersized context", text[:min(len(text), 12)])
		}
	}
}

// TestCompactionWaitsForTheDocumentedShareOfTheWindow pins the unit the
// threshold is expressed in: a conversation that fills half the window must not
// trigger compaction at an eighty percent threshold.
func TestCompactionWaitsForTheDocumentedShareOfTheWindow(t *testing.T) {
	const contextWindow = 100_000
	half := strings.Repeat("word ", contextWindow*bytesPerToken/2/len("word "))
	request := admissionTestRequest([]openrouter.Message{
		textMessage(openrouter.RoleUser, "do the task"),
		textMessage(openrouter.RoleAssistant, half),
		textMessage(openrouter.RoleUser, "continue"),
	})
	planned, err := planRequestAdmission(request, contextWindow)
	if err != nil {
		t.Fatal(err)
	}
	if planned.plan != nil {
		t.Fatalf("compaction planned at occupancy %d of a %d token window",
			planned.occupancy, contextWindow)
	}
	if planned.occupancy < contextWindow/4 || planned.occupancy > contextWindow*3/4 {
		t.Fatalf("occupancy = %d, want roughly half of %d", planned.occupancy, contextWindow)
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

// TestRequestAdmissionChargesMultimodalAnAllowance checks that a media block
// costs its allowance rather than its payload. Sizing an inline image by its
// base64 bytes would refuse an ordinary picture, and ignoring it would let a
// request overrun the model's window.
func TestRequestAdmissionChargesMultimodalAnAllowance(t *testing.T) {
	payload := strings.Repeat("a", 200_000)
	for _, test := range []struct {
		name      string
		block     openrouter.ContentBlock
		allowance int
	}{
		{
			name:      "linked image",
			block:     openrouter.ContentBlock{Type: "image_url", ImageURL: "https://example.com/a.png"},
			allowance: imageBlockTokens,
		},
		{
			name:      "inline image",
			block:     openrouter.ContentBlock{Type: "image_url", ImageURL: "data:image/png;base64," + payload},
			allowance: imageBlockTokens,
		},
		{
			name:      "inline audio",
			block:     openrouter.ContentBlock{Type: "input_audio", AudioData: payload, AudioFormat: "wav"},
			allowance: audioBlockTokens,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := admissionTestRequest([]openrouter.Message{{
				Role: openrouter.RoleUser, Content: []openrouter.ContentBlock{test.block},
			}})
			occupancy, _, err := estimateProviderRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			if occupancy <= test.allowance || occupancy > test.allowance+1000 {
				t.Fatalf("occupancy = %d, want just over the %d allowance", occupancy, test.allowance)
			}
			planned, err := planRequestAdmission(request, 100_000)
			if err != nil {
				t.Fatalf("admission = %v", err)
			}
			if planned.plan != nil {
				t.Fatal("a single multimodal request asked to be compacted")
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
