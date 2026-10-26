package acp

import protocol "github.com/coder/acp-go-sdk"

// Method names.
const (
	MethodInitialize        = protocol.AgentMethodInitialize
	MethodNewSession        = protocol.AgentMethodSessionNew
	MethodLoadSession       = protocol.AgentMethodSessionLoad
	MethodListSessions      = protocol.AgentMethodSessionList
	MethodCloseSession      = protocol.AgentMethodSessionClose
	MethodDeleteSession     = protocol.AgentMethodSessionDelete
	MethodSetConfigOption   = protocol.AgentMethodSessionSetConfigOption
	MethodPrompt            = protocol.AgentMethodSessionPrompt
	MethodCancel            = protocol.AgentMethodSessionCancel
	MethodUpdate            = protocol.ClientMethodSessionUpdate
	MethodRequestPermission = protocol.ClientMethodSessionRequestPermission
	// MethodTeam is Ox's notification of an orchestrator's workers. ACP leaves
	// method names starting with an underscore to extensions.
	MethodTeam = "_ox/team"
)

// ProtocolVersion is the one ACP version Ox speaks.
const ProtocolVersion = protocol.ProtocolVersionNumber

// Ox includes the tool name for the terminal client's tool-specific views.
type ToolCall struct {
	protocol.SessionUpdateToolCall
	Name string `json:"name,omitempty"`
}

type PermissionToolCall struct {
	protocol.ToolCallUpdate
	Name string `json:"name,omitempty"`
}

type RequestPermissionRequest struct {
	protocol.RequestPermissionRequest
	ToolCall PermissionToolCall `json:"toolCall"`
}

type SessionNotification struct {
	SessionID string `json:"sessionId"`
	Update    any    `json:"update"`
}

// TeamNotification carries an orchestrator's whole team, so the client
// replaces its list.
type TeamNotification struct {
	SessionID string       `json:"sessionId"`
	Members   []TeamMember `json:"members"`
}

type TeamMember struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}
