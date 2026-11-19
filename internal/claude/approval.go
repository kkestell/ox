package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

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
			a.onError(fmt.Errorf("reject Claude control request: %w", err))
		}
		return
	}
	a.approvals = append(a.approvals, agent.Approval{RequestID: requestID, Request: request})
	a.publish(agent.Update{Type: agent.UpdateApproval, RequestID: requestID, Request: request})
}

func (a *Agent) Approve(requestID string, response json.RawMessage) error {
	if !validApprovalResponse(response) {
		return errors.New("response must be an allow or deny permission response")
	}
	if !slices.ContainsFunc(a.approvals, func(p agent.Approval) bool { return p.RequestID == requestID }) {
		return agent.ErrApprovalNotFound
	}
	// A failed write leaves the approval pending; the process's exit, which
	// a broken pipe means, withdraws it.
	if err := a.input.Respond(requestID, response); err != nil {
		return fmt.Errorf("answer Claude: %w", err)
	}
	a.removeApproval(requestID)
	a.publish(agent.Update{Type: agent.UpdateApprovalAnswered, RequestID: requestID})
	return nil
}

func (a *Agent) cancelApproval(requestID string) {
	if a.removeApproval(requestID) {
		a.publish(agent.Update{Type: agent.UpdateApprovalCancelled, RequestID: requestID})
	}
}

// removeApproval reports whether requestID was pending.
func (a *Agent) removeApproval(requestID string) bool {
	i := slices.IndexFunc(a.approvals, func(p agent.Approval) bool { return p.RequestID == requestID })
	if i < 0 {
		return false
	}
	a.approvals = slices.Delete(a.approvals, i, i+1)
	return true
}

func (a *Agent) withdrawApprovals() {
	approvals := a.approvals
	a.approvals = nil
	for _, p := range approvals {
		a.publish(agent.Update{Type: agent.UpdateApprovalCancelled, RequestID: p.RequestID})
	}
}

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
