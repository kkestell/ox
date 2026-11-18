package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/claude"
)

func output(t *testing.T, a *Agent, line string) {
	t.Helper()
	var message claude.Message
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		t.Fatal(err)
	}
	a.handleOutput(message)
	a.armPark()
}

func TestParkTimerFollowsActivity(t *testing.T) {
	a, _ := testAgent()
	a.parkAfter = time.Hour
	a.armPark()
	first := a.parkTimer
	if first == 0 {
		t.Fatal("idle agent did not arm the park timer")
	}

	output(t, a, `{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"b1"}]}`)
	if a.parkTimer != 0 {
		t.Fatal("background task left the park timer armed")
	}
	output(t, a, `{"type":"system","subtype":"background_tasks_changed","tasks":[]}`)
	if a.parkTimer == 0 || a.parkTimer == first {
		t.Fatal("finished background task did not restart the park timer")
	}

	a.parkTimedOut(first)
	if a.phase != available {
		t.Fatal("a stale park timer parked the agent")
	}
}

func TestSchedulesBlockParking(t *testing.T) {
	a, input := testAgent()
	a.parkAfter = time.Hour
	stop := func(crons string) {
		output(t, a, `{"type":"control_request","request_id":"hook","request":{"subtype":"hook_callback","callback_id":"stop","input":{"hook_event_name":"Stop","session_crons":`+crons+`}}}`)
	}

	output(t, a, `{"type":"assistant","parent_tool_use_id":"toolu_agent","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"ScheduleWakeup","input":{"delaySeconds":60}}]}}`)
	output(t, a, `{"type":"user","message":{"content":"hello"}}`)
	if a.parkTimer != 0 {
		t.Fatal("a scheduling tool call left the park timer armed before the Stop hook")
	}
	stop(`[{"id":"b2286d1a","schedule":"45 9 * * *","recurring":false,"prompt":"tick"}]`)
	if a.parkTimer != 0 {
		t.Fatal("a pending schedule left the park timer armed")
	}
	if !strings.Contains(input.String(), `"request_id":"hook"`) {
		t.Fatalf("Stop hook was not answered: %s", input)
	}
	stop(`[]`)
	if a.parkTimer == 0 {
		t.Fatal("an empty schedule list did not arm the park timer")
	}
}

func TestParkingRefusesPrompts(t *testing.T) {
	a, _ := testAgent()
	a.phase = parking
	if _, err := a.submit(new(recorder), "hello"); !errors.Is(err, ErrExited) {
		t.Fatalf("parking agent took a prompt: %v", err)
	}
	reply := make(chan error, 1)
	model := "haiku"
	a.reconfigure(Configuration{Model: &model}, reply)
	if err := <-reply; !errors.Is(err, ErrExited) {
		t.Fatalf("parking agent took configuration: %v", err)
	}
}
