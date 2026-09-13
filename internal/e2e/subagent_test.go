package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestParentCompletionCancelsLiveSubagentThroughShippedBinary(t *testing.T) {
	model := startModel(t)
	model.queueFor(
		"delegate",
		toolResponse(
			"start-child",
			"subagent_start",
			`{"name":"worker","task":"keep working"}`,
		),
	)
	childHeld := model.holdFor("keep working", "")
	parentHeld := model.hold("")
	child, session := startSession(t, withModel(model))

	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("delegate"),
	})
	childHeld.await(t)
	parentHeld.await(t)
	parentHeld.finish(sse(evText("parent done"), evFinishReason("stop")))
	response := promptResponse(t, child.result(child.await(turn)))
	updates(t, child, session)
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}

	request := model.requestFor("keep working")
	if len(request.Messages) == 0 || !strings.Contains(request.Messages[0].text(), "<subagent-role>") {
		t.Fatal("child request omitted the subagent role")
	}
	var report, start bool
	for _, raw := range request.Tools {
		text := string(raw)
		report = report || strings.Contains(text, `"name":"subagent_report"`)
		start = start || strings.Contains(text, `"name":"subagent_start"`)
	}
	if !report || start {
		t.Fatalf("child tool scope: report=%t start=%t", report, start)
	}
}

// TestConcurrentSubagentsCoordinateThroughShippedBinary covers the successful
// coordination path through the real process: two children run at once, a
// parent message reaches one of them, a child report reaches the parent, and
// both final answers come back. Child responses are matched by the text they
// answer, and the parent keeps waiting until its conversation holds both
// results, so the test never guesses how many waits that takes.
func TestConcurrentSubagentsCoordinateThroughShippedBinary(t *testing.T) {
	model := startModel(t)
	model.queueFor("delegate", sse(
		evToolCall(0, "start-a", "function", "subagent_start", `{"name":"a","task":"task A"}`),
		evToolCall(1, "start-b", "function", "subagent_start", `{"name":"b","task":"task B"}`),
		evFinishReason("tool_calls"),
	))
	childA := model.holdFor("task A", "")
	childB := model.holdFor("task B", "")
	model.queuePrimaryFunc(func(request modelRequest) string {
		return sse(
			evToolCall(0, "send-a", "function", "subagent_send",
				`{"id":"`+subagentID(t, request, "a")+`","message":"focus on parsing"}`),
			evFinishReason("tool_calls"),
		)
	})
	firstWait := model.holdPrimary("")
	var waits atomic.Int32
	model.queuePrimaryDefault(func(request modelRequest) string {
		conversation := requestText(request)
		if strings.Contains(conversation, "A done after follow-up") &&
			strings.Contains(conversation, "B done") {
			return sse(evText("parent done"), evFinishReason("stop"))
		}
		return sse(
			evToolCall(0, fmt.Sprintf("wait-%d", waits.Add(1)), "function",
				"subagent_wait", `{}`),
			evFinishReason("tool_calls"),
		)
	})
	model.queueFor("Messages from the primary agent:\n\nfocus on parsing",
		sse(evText("A done after follow-up"), evFinishReason("stop")))
	model.queueFor("message delivered to the primary agent",
		sse(evText("B done"), evFinishReason("stop")))

	child, session := startSession(t, withModel(model))
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("delegate"),
	})
	// Both children reach their own provider request before either is
	// released, which is what proves they run concurrently. Holding the
	// parent's next step until then keeps its message ahead of child A's
	// release, so the message reaches a running child.
	childA.await(t)
	childB.await(t)
	firstWait.await(t)
	childB.finish(sse(
		evToolCall(0, "report-b", "function", "subagent_report", `{"message":"B progress"}`),
		evFinishReason("tool_calls"),
	))
	childA.finish(sse(evText("A first answer"), evFinishReason("stop")))
	firstWait.finish(sse(
		evToolCall(0, "wait-first", "function", "subagent_wait", `{}`),
		evFinishReason("tool_calls"),
	))

	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)

	parent := parentConversation(t, model)
	for _, want := range []string{"B progress", "A done after follow-up", "B done"} {
		if !strings.Contains(parent, want) {
			t.Fatalf("parent conversation omitted %q:\n%s", want, parent)
		}
	}
}

// subagentID reads the ID ox assigned a named child out of the start results
// already in the parent's conversation.
func subagentID(t *testing.T, request modelRequest, name string) string {
	t.Helper()
	for _, message := range request.Messages {
		var result struct {
			Subagents []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"subagents"`
		}
		if json.Unmarshal([]byte(message.text()), &result) != nil {
			continue
		}
		for _, child := range result.Subagents {
			if child.Name == name {
				return child.ID
			}
		}
	}
	t.Errorf("no subagent named %q in the conversation", name)
	return ""
}

// parentConversation renders the last primary request, which carries every
// coordination result the turn produced.
func parentConversation(t *testing.T, model *mockModel) string {
	t.Helper()
	var latest modelRequest
	found := false
	for _, request := range model.requests() {
		if fromSubagent(request) {
			continue
		}
		latest, found = request, true
	}
	if !found {
		t.Fatal("model received no primary request")
	}
	return requestText(latest)
}
