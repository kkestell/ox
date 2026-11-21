package claude

import (
	"encoding/json"
	"fmt"
)

func (a *Agent) Parkable() bool {
	return a.idle() && len(a.approvals) == 0 && a.tracker.Tasks() == 0 && !a.mayHaveSchedules
}

// Only a Stop report with a schedule list settles whether Claude has work pending.
func (a *Agent) stopHook(requestID string, crons *[]json.RawMessage) {
	if crons != nil {
		a.mayHaveSchedules = len(*crons) > 0
	}
	if err := a.input.Respond(requestID, map[string]any{}); err != nil {
		a.onError(fmt.Errorf("answer Claude's Stop hook: %w", err))
	}
}
