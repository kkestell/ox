package e2e

import (
	"strings"
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
