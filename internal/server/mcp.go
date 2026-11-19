package server

import (
	"encoding/json"
	"fmt"

	"github.com/kkestell/ox/internal/jsonrpc"
)

// attach handles a connection's first line when it is an mcp/attach request.
// It reports whether the line was one, and the session ID it attached to,
// empty when the request was refused. An attached connection leaves s.conns
// before the result is queued, so every notification it was ever sent is
// ahead of the result.
func (s *Server) attach(c *conn, reply replyFunc, line []byte) (string, bool) {
	request, invalid := jsonrpc.Parse(line)
	if invalid != nil || request.Method != "mcp/attach" || len(request.ID) == 0 {
		return "", false
	}
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(request.Params, &input); err != nil || input.SessionID == "" {
		reply(invalidParams(request.ID, "session_id is required"))
		return "", true
	}
	s.connsMu.Lock()
	delete(s.conns, c)
	s.connsMu.Unlock()
	reply(jsonrpc.Success(request.ID, struct{}{}))
	return input.SessionID, true
}

// handleMCP answers one MCP request from the forwarder of a session. It reads
// only session membership and the configuration, so it never waits on a
// session's goroutine.
func (s *Server) handleMCP(reply replyFunc, sessionID string, line []byte) {
	request, invalid := jsonrpc.Parse(line)
	if invalid != nil {
		reply(invalid)
		return
	}
	if len(request.ID) == 0 {
		return
	}
	id := request.ID
	s.mu.Lock()
	closed, sess := s.closed, s.sessions[sessionID]
	s.mu.Unlock()
	switch {
	case closed:
		reply(shuttingDown(id))
		return
	case sess == nil:
		reply(sessionNotFound(id))
		return
	}
	switch request.Method {
	case "initialize":
		var input struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(request.Params, &input); err != nil || input.ProtocolVersion == "" {
			reply(invalidParams(id, "protocolVersion is required"))
			return
		}
		reply(jsonrpc.Success(id, map[string]any{
			"protocolVersion": input.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "ox", "version": "0"},
		}))
	case "ping":
		reply(jsonrpc.Success(id, struct{}{}))
	case "tools/list":
		reply(jsonrpc.Success(id, map[string]any{"tools": tools}))
	case "tools/call":
		var input struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(request.Params, &input); err != nil {
			reply(invalidParams(id, "invalid params"))
			return
		}
		if input.Name != "list_models" {
			reply(invalidParams(id, fmt.Sprintf("unknown tool %q", input.Name)))
			return
		}
		text, err := json.Marshal(s.config)
		if err != nil {
			reply(failure(id, err))
			return
		}
		reply(jsonrpc.Success(id, map[string]any{
			"content": []any{map[string]any{"type": "text", "text": string(text)}},
		}))
	default:
		reply(jsonrpc.Failure(id, jsonrpc.MethodNotFound, "Method not found"))
	}
}

var tools = []map[string]any{{
	"name":        "list_models",
	"description": "List the harnesses Ox offers, with their models, effort levels, and permission modes, and the defaults for a new session.",
	"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	// Claude's plan mode prompts for an MCP tool its --allowedTools names
	// unless the tool is annotated read-only.
	"annotations": map[string]any{"readOnlyHint": true},
}}

// approvedTools are the tools annotated read-only, which run without an
// approval prompt.
var approvedTools = func() []string {
	var names []string
	for _, tool := range tools {
		if annotations, _ := tool["annotations"].(map[string]any); annotations["readOnlyHint"] == true {
			names = append(names, tool["name"].(string))
		}
	}
	return names
}()
