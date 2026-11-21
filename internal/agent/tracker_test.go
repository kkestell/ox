package agent

import (
	"slices"
	"testing"
)

// tracked returns a tracker and the updates it publishes, each as its type,
// message ID, and state.
func tracked() (*Tracker, *[]string) {
	var updates []string
	t := NewTracker(func(u Update) { updates = append(updates, u.Type+" "+u.MessageID+" "+u.State) })
	return t, &updates
}

func expect(t *testing.T, updates *[]string, want ...string) {
	t.Helper()
	if !slices.Equal(*updates, want) {
		t.Fatalf("published %q, not %q", *updates, want)
	}
	*updates = nil
}

func TestTrackerQueuedInputIsRunning(t *testing.T) {
	tr, updates := tracked()
	tr.Accept(Input{MessageID: "p1"})
	expect(t, updates, "prompt_queued p1 ", "state p1 running")
	tr.Accept(Input{MessageID: "p2"})
	expect(t, updates, "prompt_queued p2 ")

	tr.Report(StateIdle, "")
	expect(t, updates)
	if tr.State() != StateRunning || tr.Idle() {
		t.Fatalf("an idle report with inputs queued left %s", tr.State())
	}

	if taken := tr.Take(); taken.MessageID != "p1" {
		t.Fatalf("took %s, not the oldest input", taken.MessageID)
	}
	expect(t, updates, "prompt p1 ")
	if first, ok := tr.First(); !ok || first.MessageID != "p2" || tr.Len() != 1 {
		t.Fatalf("after a take, the first input is %+v, %v", first, ok)
	}

	tr.DropFirst()
	expect(t, updates, "prompt_cancelled p2 ")
	if tr.State() != StateRunning || len(tr.Queued()) != 0 || tr.Queued() == nil {
		t.Fatalf("a take did not leave the harness running with nothing queued: %s, %v", tr.State(), tr.Queued())
	}

	tr.Report(StateIdle, "p1")
	expect(t, updates, "state p1 idle")
	if !tr.Idle() {
		t.Fatal("idle is not idle")
	}
}

func TestTrackerDropFrom(t *testing.T) {
	tr, updates := tracked()
	for _, id := range []string{"p1", "p2", "p3"} {
		tr.Accept(Input{MessageID: id})
	}
	*updates = nil
	tr.DropFrom(2)
	expect(t, updates, "prompt_cancelled p3 ")
	if q := tr.Queued(); len(q) != 2 || q[0].MessageID != "p1" || q[1].MessageID != "p2" {
		t.Fatalf("dropping from 2 left %+v", q)
	}
	tr.DropFrom(0)
	expect(t, updates, "prompt_cancelled p1 ", "prompt_cancelled p2 ", "state p2 idle")
	if _, ok := tr.First(); ok {
		t.Fatal("an empty tracker has a first input")
	}
	tr.DropFrom(0)
	expect(t, updates)

	tr.Accept(Input{MessageID: "p4"})
	*updates = nil
	tr.DropFirst()
	expect(t, updates, "prompt_cancelled p4 ", "state p4 idle")
}

func TestTrackerReportedState(t *testing.T) {
	tr, updates := tracked()
	tr.Report(StateRunning, "")
	tr.Report(StateRequiresAction, "")
	expect(t, updates, "state  running", "state  requires_action")
	if tr.Idle() {
		t.Fatal("requires_action is idle")
	}

	tr.Accept(Input{MessageID: "p1"})
	expect(t, updates, "prompt_queued p1 ")
	if tr.State() != StateRequiresAction {
		t.Fatalf("an input queued during an approval hid it: %s", tr.State())
	}
}

func TestTrackerBackground(t *testing.T) {
	tr, updates := tracked()
	tr.Report(StateRunning, "")
	*updates = nil
	tr.SetTasks(1)
	expect(t, updates)
	tr.Report(StateIdle, "")
	expect(t, updates, "state  background")
	if !tr.Idle() || tr.Tasks() != 1 {
		t.Fatalf("background is not idle, or lost its task: %d", tr.Tasks())
	}

	tr.Accept(Input{MessageID: "p1"})
	expect(t, updates, "prompt_queued p1 ", "state p1 running")
	tr.DropFrom(0)
	expect(t, updates, "prompt_cancelled p1 ", "state p1 background")

	tr.SetTasks(0)
	expect(t, updates, "state  idle")
	tr.SetTasks(2)
	expect(t, updates, "state  background")
}
