package agent

import (
	"encoding/json"
	"log"
	"time"
)

// parkable reports whether nothing is using the process: no prompt or
// configuration, Claude idle, no background task, and nothing scheduled.
func (a *Agent) parkable() bool {
	return a.idle() && a.background == 0 && a.schedules == 0 && !a.scheduling
}

// stopHook records the schedules Claude reports at the end of a completed
// turn and lets the turn finish. A turn that ends by interrupt or error sends
// no Stop hook, so a scheduling tool call keeps the agent from parking until
// the next one.
func (a *Agent) stopHook(requestID string, crons *[]json.RawMessage) {
	if crons != nil {
		a.schedules = len(*crons)
		a.scheduling = false
	}
	if err := a.input.Respond(requestID, map[string]any{}); err != nil {
		log.Printf("answer Stop hook: %v", err)
	}
}

// armPark starts the park timer when the agent becomes parkable and disarms
// it when the agent stops being parkable, so the full period restarts each
// time.
func (a *Agent) armPark() {
	switch {
	case a.parkAfter == 0 || a.phase == exited:
	case !a.parkable():
		a.parkTimer = 0
	case a.parkTimer == 0:
		a.timers++
		timer := a.timers
		a.parkTimer = timer
		time.AfterFunc(a.parkAfter, func() { a.post(parkTimeout{timer}) })
	}
}

// parkTimedOut terminates the process if the timer is still armed. Prompts and
// configuration are refused from then on, as if the process had exited.
func (a *Agent) parkTimedOut(timer int) {
	if timer != a.parkTimer || !a.parkable() {
		return
	}
	a.phase = parking
	if err := a.proc.Terminate(); err != nil {
		log.Print(err)
	}
}
