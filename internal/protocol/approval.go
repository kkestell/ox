package protocol

import (
	"encoding/json"
	"errors"

	"github.com/kkestell/ox/internal/agents"
)

func (c *client) approveAgent(registry *agents.Registry, params json.RawMessage) (any, *rpcError) {
	var input struct {
		AgentID   string          `json:"agent_id"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.AgentID == "" || input.RequestID == "" || !validApprovalResponse(input.Response) {
		return nil, &rpcError{Code: -32602, Message: "agent_id, request_id and an allow or deny response are required"}
	}
	err := registry.Approve(input.AgentID, c.id, input.RequestID, input.Response)
	switch {
	case err == nil:
		return struct{}{}, nil
	case errors.Is(err, agents.ErrNotFound):
		return nil, &rpcError{Code: -32001, Message: "Agent not found"}
	case errors.Is(err, agents.ErrApprovalNotFound):
		return nil, &rpcError{Code: -32003, Message: err.Error()}
	case errors.Is(err, agents.ErrApprovalOwner):
		return nil, &rpcError{Code: -32004, Message: err.Error()}
	default:
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
}

// Check the response shape, while retaining Claude's fields as raw JSON.
func validApprovalResponse(raw json.RawMessage) bool {
	var response struct {
		Behavior               string          `json:"behavior"`
		UpdatedInput           json.RawMessage `json:"updatedInput"`
		UpdatedPermissions     json.RawMessage `json:"updatedPermissions"`
		Message                string          `json:"message"`
		Interrupt              bool            `json:"interrupt"`
		ToolUseID              string          `json:"toolUseID"`
		DecisionClassification string          `json:"decisionClassification"`
	}
	if json.Unmarshal(raw, &response) != nil || (response.Behavior != "allow" && response.Behavior != "deny") {
		return false
	}
	return (len(response.UpdatedInput) == 0 || response.UpdatedInput[0] == '{') &&
		(len(response.UpdatedPermissions) == 0 || response.UpdatedPermissions[0] == '[')
}
