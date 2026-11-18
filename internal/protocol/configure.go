package protocol

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/kkestell/ox/internal/agents"
)

func configureAgent(registry *agents.Registry, params json.RawMessage) (any, *rpcError) {
	var input struct {
		AgentID string `json:"agent_id"`
		agents.Configuration
	}
	if err := json.Unmarshal(params, &input); err != nil || input.AgentID == "" {
		return nil, &rpcError{Code: -32602, Message: "agent_id and configuration fields are required"}
	}
	if input.Model == nil && input.Effort == nil && input.PermissionMode == nil {
		return nil, &rpcError{Code: -32602, Message: "At least one of model, effort, or permission_mode is required"}
	}
	if input.Model != nil && strings.TrimSpace(*input.Model) == "" {
		return nil, &rpcError{Code: -32602, Message: "model must be nonblank"}
	}
	if input.Effort != nil && !validEffort(*input.Effort) {
		return nil, &rpcError{Code: -32602, Message: "Unknown effort level"}
	}
	if input.PermissionMode != nil && strings.TrimSpace(*input.PermissionMode) == "" {
		return nil, &rpcError{Code: -32602, Message: "permission_mode must be nonblank"}
	}
	err := registry.Configure(input.AgentID, input.Configuration)
	switch {
	case err == nil:
		return struct{}{}, nil
	case errors.Is(err, agents.ErrNotFound):
		return nil, &rpcError{Code: -32001, Message: "Agent not found"}
	case errors.Is(err, agents.ErrBusy):
		return nil, &rpcError{Code: -32002, Message: "Agent is busy"}
	default:
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
}

func validEffort(effort string) bool {
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max", "ultracode":
		return true
	default:
		return false
	}
}
