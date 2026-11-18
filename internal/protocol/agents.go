package protocol

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/kkestell/ox/internal/agents"
)

func (c *client) dispatch(registry *agents.Registry, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "agent/create":
		return createAgent(registry, params)
	case "agent/destroy":
		return destroyAgent(registry, params)
	case "agent/send":
		return c.sendAgent(registry, params)
	case "agent/approve":
		return c.approveAgent(registry, params)
	case "agent/configure":
		return configureAgent(registry, params)
	default:
		return nil, &rpcError{Code: -32601, Message: "Method not found"}
	}
}

func (c *client) sendAgent(registry *agents.Registry, params json.RawMessage) (any, *rpcError) {
	var input struct {
		AgentID string `json:"agent_id"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.AgentID == "" || strings.TrimSpace(input.Text) == "" {
		return nil, &rpcError{Code: -32602, Message: "agent_id and text are required"}
	}
	id, err := registry.Send(input.AgentID, c.id, input.Text, func(update agents.Update) {
		c.write(notification{
			JSONRPC: version, Method: "agent/update",
			Params: struct {
				AgentID string `json:"agent_id"`
				agents.Update
			}{AgentID: input.AgentID, Update: update},
		})
	})
	if err != nil {
		switch {
		case errors.Is(err, agents.ErrNotFound):
			return nil, &rpcError{Code: -32001, Message: "Agent not found"}
		case errors.Is(err, agents.ErrBusy):
			return nil, &rpcError{Code: -32002, Message: "Agent is busy"}
		default:
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
	}
	return struct {
		MessageID string `json:"message_id"`
	}{MessageID: id}, nil
}

func createAgent(registry *agents.Registry, params json.RawMessage) (any, *rpcError) {
	var options agents.Options
	if err := json.Unmarshal(params, &options); err != nil {
		return nil, &rpcError{Code: -32602, Message: "Expected agent creation parameters"}
	}
	if !filepath.IsAbs(options.Workspace) {
		return nil, &rpcError{Code: -32602, Message: "workspace must be an absolute directory path"}
	}
	// Claude silently falls back to its default for unrecognized effort values.
	if !validEffort(options.Effort) {
		return nil, &rpcError{Code: -32602, Message: "Unknown effort level"}
	}

	id, err := registry.Create(options)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	return struct {
		AgentID string `json:"agent_id"`
	}{AgentID: id}, nil
}

func destroyAgent(registry *agents.Registry, params json.RawMessage) (any, *rpcError) {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.AgentID == "" {
		return nil, &rpcError{Code: -32602, Message: "agent_id is required"}
	}
	if err := registry.Destroy(input.AgentID); err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			return nil, &rpcError{Code: -32001, Message: "Agent not found"}
		}
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	return struct{}{}, nil
}
