package claude

import (
	"strings"
	"testing"
)

func TestParkableFollowsActivity(t *testing.T) {
	a, input, _ := testAgent()
	if !a.Parkable() {
		t.Fatal("an idle agent is not parkable")
	}
	output(t, a, `{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"b1"}]}`)
	if a.Parkable() {
		t.Fatal("a background task left the agent parkable")
	}
	output(t, a, `{"type":"system","subtype":"background_tasks_changed","tasks":[]}`)
	if !a.Parkable() {
		t.Fatal("a finished background task kept the agent from parking")
	}

	stop := func(crons string) {
		output(t, a, `{"type":"control_request","request_id":"hook","request":{"subtype":"hook_callback","callback_id":"stop","input":{"hook_event_name":"Stop","session_crons":`+crons+`}}}`)
	}
	stop(`[{"id":"b2286d1a","schedule":"45 9 * * *","recurring":false,"prompt":"tick"}]`)
	if a.Parkable() {
		t.Fatal("a pending schedule left the agent parkable")
	}
	if !strings.Contains(input.String(), `"request_id":"hook"`) {
		t.Fatalf("Stop hook was not answered: %s", input)
	}
	stop(`[]`)
	if !a.Parkable() {
		t.Fatal("an empty schedule list kept the agent from parking")
	}
}

// A turn that ends by interrupt or error sends no Stop hook, so the schedules
// reported before it still count, and a scheduling tool call in it counts
// until the next Stop hook.
func TestTurnWithoutStopHookKeepsSchedules(t *testing.T) {
	stop := func(a *Agent, crons string) {
		output(t, a, `{"type":"control_request","request_id":"hook","request":{"subtype":"hook_callback","callback_id":"stop","input":{"hook_event_name":"Stop","session_crons":`+crons+`}}}`)
	}
	failedTurn := func(a *Agent, assistant string) {
		output(t, a, `{"type":"system","subtype":"session_state_changed","state":"running"}`)
		if assistant != "" {
			output(t, a, assistant)
		}
		output(t, a, `{"type":"result","subtype":"error_during_execution","is_error":true}`)
		output(t, a, `{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	}

	earlier, _, _ := testAgent()
	stop(earlier, `[{"id":"c1","schedule":"*/30 * * * *","recurring":true,"prompt":"tick"}]`)
	failedTurn(earlier, "")
	if earlier.Parkable() {
		t.Fatal("a failed turn forgot the schedules from the last completed turn")
	}

	during, _, _ := testAgent()
	failedTurn(during, `{"type":"assistant","parent_tool_use_id":"toolu_agent","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"ScheduleWakeup","input":{"delaySeconds":600}}]}}`)
	if during.Parkable() {
		t.Fatal("a scheduling tool call in a failed turn left the agent parkable")
	}
	stop(during, `[]`)
	if !during.Parkable() {
		t.Fatal("the next Stop hook did not settle the scheduling tool call")
	}
}
