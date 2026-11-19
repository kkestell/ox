package claude

import (
	"encoding/json"
	"log"
)

func (a *Agent) Parkable() bool {
	return a.idle() && len(a.approvals) == 0 && a.background == 0 && a.schedules == 0 && !a.scheduling
}

// A scheduling tool call keeps the agent active until Stop supplies a fresh
// count. Interrupted and failed turns can leave the previous count in effect.
func (a *Agent) stopHook(requestID string, crons *[]json.RawMessage) {
	if crons != nil {
		a.schedules = len(*crons)
		a.scheduling = false
	}
	if err := a.input.Respond(requestID, map[string]any{}); err != nil {
		log.Printf("answer Stop hook in Claude session %s: %v", a.sessionID, err)
	}
}
