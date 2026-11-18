package claude

import (
	"encoding/json"
	"errors"
	"log"

	"github.com/kkestell/ox/internal/agent"
)

func (a *Agent) controlRequest(requestID string, request json.RawMessage) {
	var control struct {
		Subtype string `json:"subtype"`
		Input   struct {
			SessionCrons *[]json.RawMessage `json:"session_crons"`
		} `json:"input"`
	}
	if json.Unmarshal(request, &control) == nil && control.Subtype == "hook_callback" {
		a.stopHook(requestID, control.Input.SessionCrons)
		return
	}
	if control.Subtype != "can_use_tool" {
		if err := a.input.Reject(requestID, "Unsupported control request"); err != nil {
			log.Printf("reject Claude control request: %v", err)
		}
		return
	}
	a.approvals[requestID] = request
	a.publish(agent.Update{Type: "approval", RequestID: requestID, Request: request})
}

// Approve answers a pending approval with Claude's permission response,
// keeping its fields as they are.
func (a *Agent) Approve(requestID string, response json.RawMessage) error {
	if !validApprovalResponse(response) {
		return errors.New("response must be an allow or deny permission response")
	}
	if _, pending := a.approvals[requestID]; !pending {
		return agent.ErrApprovalNotFound
	}
	delete(a.approvals, requestID)
	if err := a.input.Respond(requestID, response); err != nil {
		return err
	}
	a.publish(agent.Update{Type: "approval_answered", RequestID: requestID})
	return nil
}

func (a *Agent) cancelApproval(requestID string) {
	if _, pending := a.approvals[requestID]; pending {
		delete(a.approvals, requestID)
		a.publish(agent.Update{Type: "approval_cancelled", RequestID: requestID})
	}
}

// withdrawApprovals cancels every unanswered approval, once Claude is idle or
// has exited.
func (a *Agent) withdrawApprovals() {
	for id := range a.approvals {
		a.publish(agent.Update{Type: "approval_cancelled", RequestID: id})
	}
	clear(a.approvals)
}

// validApprovalResponse checks the response shape while keeping Claude's
// fields as raw JSON.
func validApprovalResponse(raw json.RawMessage) bool {
	var response struct {
		Behavior           string          `json:"behavior"`
		UpdatedInput       json.RawMessage `json:"updatedInput"`
		UpdatedPermissions json.RawMessage `json:"updatedPermissions"`
		Message            string          `json:"message"`
		Interrupt          bool            `json:"interrupt"`
		ToolUseID          string          `json:"toolUseID"`
	}
	if json.Unmarshal(raw, &response) != nil || (response.Behavior != "allow" && response.Behavior != "deny") {
		return false
	}
	return (len(response.UpdatedInput) == 0 || response.UpdatedInput[0] == '{') &&
		(len(response.UpdatedPermissions) == 0 || response.UpdatedPermissions[0] == '[')
}
