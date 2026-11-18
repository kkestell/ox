package agent

import (
	"encoding/json"
	"log"
)

func (a *Agent) controlRequest(requestID string, request json.RawMessage) {
	var control struct {
		Subtype string `json:"subtype"`
	}
	if json.Unmarshal(request, &control) != nil || control.Subtype != "can_use_tool" {
		if err := a.input.Reject(requestID, "Unsupported control request"); err != nil {
			log.Printf("reject Claude control request: %v", err)
		}
		return
	}
	if a.prompt == nil {
		// Claude started this turn itself, so no client is following it.
		err := a.input.Respond(requestID, map[string]any{
			"behavior": "deny", "message": "No prompt owns this approval", "interrupt": true,
		})
		if err != nil {
			log.Printf("deny unowned approval: %v", err)
		}
		return
	}
	a.prompt.approvals[requestID] = request
	a.publish(Update{Type: "approval", RequestID: requestID, Request: request})
}

func (a *Agent) answer(requestID string, response json.RawMessage) error {
	if a.prompt == nil {
		return ErrApprovalNotFound
	}
	if _, pending := a.prompt.approvals[requestID]; !pending {
		return ErrApprovalNotFound
	}
	delete(a.prompt.approvals, requestID)
	return a.input.Respond(requestID, response)
}

func (a *Agent) cancelApproval(requestID string) {
	if a.prompt == nil {
		return
	}
	if _, pending := a.prompt.approvals[requestID]; pending {
		delete(a.prompt.approvals, requestID)
		a.publish(Update{Type: "approval_cancelled", RequestID: requestID})
	}
}
