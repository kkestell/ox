package agent

// Tracker is a harness's untaken inputs and the execution state it publishes.
// The harness decides when an input is written, taken, or dropped, and reports
// its own state; Tracker publishes the input's lifecycle update and any change
// to the published state.
type Tracker struct {
	publish  func(Update)
	queued   []Input // untaken, oldest first
	reported string  // the harness's last reported state
	tasks    int     // background tasks running
}

// NewTracker returns an idle tracker that publishes through publish, the
// harness's own publisher, which sets the update's Time and tags its turn.
func NewTracker(publish func(Update)) *Tracker {
	return &Tracker{publish: publish, reported: StateIdle}
}

// State is the published state: the reported state, except that an idle
// harness is running while any input is untaken, and otherwise background
// while tasks run.
func (t *Tracker) State() string {
	if t.reported != StateIdle {
		return t.reported
	}
	if len(t.queued) > 0 {
		return StateRunning
	}
	if t.tasks > 0 {
		return StateBackground
	}
	return StateIdle
}

// Reported is the harness's last reported state.
func (t *Tracker) Reported() string {
	return t.reported
}

// Idle reports that the published state is idle or background.
func (t *Tracker) Idle() bool {
	state := t.State()
	return state == StateIdle || state == StateBackground
}

// Queued is a copy of the untaken inputs, oldest first.
func (t *Tracker) Queued() []Input {
	return append([]Input{}, t.queued...)
}

// First is the oldest untaken input.
func (t *Tracker) First() (Input, bool) {
	if len(t.queued) == 0 {
		return Input{}, false
	}
	return t.queued[0], true
}

func (t *Tracker) Len() int {
	return len(t.queued)
}

func (t *Tracker) Tasks() int {
	return t.tasks
}

// Accept queues in and publishes prompt_queued.
func (t *Tracker) Accept(in Input) {
	was := t.State()
	t.queued = append(t.queued, in)
	t.publish(in.Update(UpdatePromptQueued))
	t.changed(was, in.MessageID)
}

// Take removes the oldest untaken input and publishes prompt. A harness takes
// an input only into a running turn, so the reported state becomes running.
func (t *Tracker) Take() Input {
	was := t.State()
	taken := t.queued[0]
	t.queued = t.queued[1:]
	t.publish(taken.Update(UpdatePrompt))
	t.reported = StateRunning
	t.changed(was, "")
	return taken
}

// DropFirst drops the oldest untaken input with prompt_cancelled. A change to
// the published state names the dropped input.
func (t *Tracker) DropFirst() {
	was := t.State()
	dropped := t.queued[0]
	t.queued = t.queued[1:]
	t.publish(dropped.Update(UpdatePromptCancelled))
	t.changed(was, dropped.MessageID)
}

// DropFrom drops the untaken inputs from index i on, oldest first, each with
// prompt_cancelled. A change to the published state names the last dropped.
func (t *Tracker) DropFrom(i int) {
	was := t.State()
	dropped := t.queued[i:]
	t.queued = t.queued[:i:i]
	last := ""
	for _, in := range dropped {
		t.publish(in.Update(UpdatePromptCancelled))
		last = in.MessageID
	}
	t.changed(was, last)
}

// End drops every untaken input with prompt_cancelled, for a harness that has
// ended, and publishes no state: the session's parked update follows, so the
// session never shows a state between its last one and parked.
func (t *Tracker) End() {
	for _, in := range t.queued {
		t.publish(in.Update(UpdatePromptCancelled))
	}
	t.queued = nil
}

// Report records the harness's state. A change to the published state names
// messageID, or is left for the harness's publisher to tag when it is empty.
func (t *Tracker) Report(state, messageID string) {
	was := t.State()
	t.reported = state
	t.changed(was, messageID)
}

// SetTasks records how many background tasks run.
func (t *Tracker) SetTasks(n int) {
	was := t.State()
	t.tasks = n
	t.changed(was, "")
}

// changed publishes the published state when it differs from was.
func (t *Tracker) changed(was, messageID string) {
	if now := t.State(); now != was {
		t.publish(Update{MessageID: messageID, Type: UpdateState, State: now})
	}
}
