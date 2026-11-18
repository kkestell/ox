package server

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/claude"
	"github.com/kkestell/ox/internal/jsonrpc"
)

// Ox error codes.
const (
	operationFailed    = -32000
	agentNotFound      = -32001
	agentBusy          = -32002
	approvalNotPending = -32003
)

// handle runs one request line and returns the response to queue, or nil when
// there is none to send.
func (s *Server) handle(c *conn, line []byte) *jsonrpc.Response {
	request, invalid := jsonrpc.Parse(line)
	if invalid != nil {
		return invalid
	}
	// Agent operations require a request ID so the client receives the outcome.
	if len(request.ID) == 0 {
		return nil
	}
	id, params := request.ID, request.Params
	switch request.Method {
	case "agent/create":
		return s.createAgent(id, params)
	case "agent/attach":
		return s.attachAgent(c, id, params)
	case "agent/send":
		return s.sendAgent(c, id, params)
	case "agent/approve":
		return s.approveAgent(id, params)
	case "agent/configure":
		return s.configureAgent(id, params)
	case "agent/destroy":
		return s.destroyAgent(id, params)
	default:
		return jsonrpc.Failure(id, jsonrpc.MethodNotFound, "Method not found")
	}
}

// failure maps an agent error to its response.
func failure(id json.RawMessage, err error) *jsonrpc.Response {
	switch {
	case errors.Is(err, agent.ErrExited):
		return jsonrpc.Failure(id, agentNotFound, "Agent not found")
	case errors.Is(err, agent.ErrBusy):
		return jsonrpc.Failure(id, agentBusy, "Agent is busy")
	case errors.Is(err, agent.ErrApprovalNotFound):
		return jsonrpc.Failure(id, approvalNotPending, err.Error())
	default:
		return jsonrpc.Failure(id, operationFailed, err.Error())
	}
}

func invalidParams(id json.RawMessage, message string) *jsonrpc.Response {
	return jsonrpc.Failure(id, jsonrpc.InvalidParams, message)
}

// lookup finds the agent named in params, or returns the response to send.
func (s *Server) lookup(id json.RawMessage, agentID string) (*agent.Agent, *jsonrpc.Response) {
	a := s.agent(agentID)
	if a == nil {
		return nil, failure(id, agent.ErrExited)
	}
	return a, nil
}

func (s *Server) createAgent(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		Workspace      string `json:"workspace"`
		Model          string `json:"model"`
		Effort         string `json:"effort"`
		PermissionMode string `json:"permission_mode"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return invalidParams(id, "Expected agent creation parameters")
	}
	if !filepath.IsAbs(input.Workspace) {
		return invalidParams(id, "workspace must be an absolute directory path")
	}
	// Claude silently falls back to its default for unrecognized effort values.
	if !validEffort(input.Effort) {
		return invalidParams(id, "Unknown effort level")
	}
	agentID, err := s.create(claude.Options(input))
	if err != nil {
		return failure(id, err)
	}
	return jsonrpc.Success(id, struct {
		AgentID string `json:"agent_id"`
	}{agentID})
}

func (s *Server) attachAgent(c *conn, id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.AgentID == "" {
		return invalidParams(id, "agent_id is required")
	}
	a, missing := s.lookup(id, input.AgentID)
	if missing != nil {
		return missing
	}
	// The agent calls back with its state before publishing any update, so the
	// response is queued ahead of the updates that follow it.
	err := a.Attach(subscription{c, input.AgentID}, func(snapshot agent.Snapshot) {
		c.queue(jsonrpc.Success(id, snapshot))
	})
	if err != nil {
		return failure(id, err)
	}
	return nil
}

func (s *Server) sendAgent(c *conn, id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		AgentID string `json:"agent_id"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.AgentID == "" || strings.TrimSpace(input.Text) == "" {
		return invalidParams(id, "agent_id and text are required")
	}
	a, missing := s.lookup(id, input.AgentID)
	if missing != nil {
		return missing
	}
	messageID, err := a.Send(subscription{c, input.AgentID}, input.Text)
	if err != nil {
		return failure(id, err)
	}
	return jsonrpc.Success(id, struct {
		MessageID string `json:"message_id"`
	}{messageID})
}

func (s *Server) approveAgent(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		AgentID   string          `json:"agent_id"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.AgentID == "" || input.RequestID == "" || !validApprovalResponse(input.Response) {
		return invalidParams(id, "agent_id, request_id and an allow or deny response are required")
	}
	a, missing := s.lookup(id, input.AgentID)
	if missing != nil {
		return missing
	}
	if err := a.Approve(input.RequestID, input.Response); err != nil {
		return failure(id, err)
	}
	return jsonrpc.Success(id, struct{}{})
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

func (s *Server) configureAgent(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		AgentID        string  `json:"agent_id"`
		Model          *string `json:"model"`
		Effort         *string `json:"effort"`
		PermissionMode *string `json:"permission_mode"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.AgentID == "" {
		return invalidParams(id, "agent_id and configuration fields are required")
	}
	if input.Model == nil && input.Effort == nil && input.PermissionMode == nil {
		return invalidParams(id, "At least one of model, effort, or permission_mode is required")
	}
	if input.Model != nil && strings.TrimSpace(*input.Model) == "" {
		return invalidParams(id, "model must be nonblank")
	}
	if input.Effort != nil && !validEffort(*input.Effort) {
		return invalidParams(id, "Unknown effort level")
	}
	if input.PermissionMode != nil && strings.TrimSpace(*input.PermissionMode) == "" {
		return invalidParams(id, "permission_mode must be nonblank")
	}
	a, missing := s.lookup(id, input.AgentID)
	if missing != nil {
		return missing
	}
	err := a.Configure(agent.Configuration{Model: input.Model, Effort: input.Effort, PermissionMode: input.PermissionMode})
	if err != nil {
		return failure(id, err)
	}
	return jsonrpc.Success(id, struct{}{})
}

func validEffort(effort string) bool {
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max", "ultracode":
		return true
	default:
		return false
	}
}

func (s *Server) destroyAgent(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.AgentID == "" {
		return invalidParams(id, "agent_id is required")
	}
	a, missing := s.lookup(id, input.AgentID)
	if missing != nil {
		return missing
	}
	if err := a.Stop(); err != nil {
		return failure(id, err)
	}
	return jsonrpc.Success(id, struct{}{})
}
