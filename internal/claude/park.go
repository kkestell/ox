package claude

import (
	"encoding/json"
	"log"
)

// Parkable reports whether nothing is using the process: no prompt or
// configuration, Claude idle, no approval pending, no background task, and
// nothing scheduled.
func (a *Agent) Parkable() bool {
	return a.idle() && len(a.approvals) == 0 && a.background == 0 && a.schedules == 0 && !a.scheduling
}

// stopHook records the schedules Claude reports at the end of a completed
// turn and lets the turn finish. A turn that ends by interrupt or error sends
// no Stop hook, so the count from the last completed turn stands, and a
// scheduling tool call keeps the agent from parking until the next Stop hook.
func (a *Agent) stopHook(requestID string, crons *[]json.RawMessage) {
	if crons != nil {
		a.schedules = len(*crons)
		a.scheduling = false
	}
	if err := a.input.Respond(requestID, map[string]any{}); err != nil {
		log.Printf("answer Stop hook: %v", err)
	}
}
