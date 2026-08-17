package acp

import "encoding/json"

const ProtocolVersion = 1

const (
	ErrCodeAuthRequired     = -32000
	ErrCodeRequestCancelled = -32800
)

type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type FileSystemCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

type ClientCapabilities struct {
	FS       *FileSystemCapabilities `json:"fs,omitempty"`
	Terminal bool                    `json:"terminal,omitempty"`
	Auth     *ClientAuthCapabilities `json:"auth,omitempty"`
}

type ClientAuthCapabilities struct {
	Terminal bool `json:"terminal,omitempty"`
}

type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

type AgentCapabilities struct {
	LoadSession        bool                   `json:"loadSession"`
	PromptCapabilities PromptCapabilities     `json:"promptCapabilities"`
	Auth               *AgentAuthCapabilities `json:"auth,omitempty"`
}

type AgentAuthCapabilities struct {
	Logout *LogoutCapabilities `json:"logout,omitempty"`
}

type LogoutCapabilities struct{}

type AuthMethod struct {
	ID          string   `json:"id"`
	Type        string   `json:"type,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Args        []string `json:"args,omitempty"`
}

type InitializeRequest struct {
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities *ClientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         *Implementation     `json:"clientInfo,omitempty"`
}

type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         Implementation    `json:"agentInfo"`
	AuthMethods       []AuthMethod      `json:"authMethods"`
}

type AuthenticateRequest struct {
	MethodID string `json:"methodId"`
}

type AuthenticateResponse struct{}

type LogoutRequest struct{}

type LogoutResponse struct{}

type CancelRequestNotification struct {
	RequestID json.RawMessage `json:"requestId"`
}

type NewSessionRequest struct {
	CWD                   string            `json:"cwd"`
	MCPServers            []json.RawMessage `json:"mcpServers"`
	AdditionalDirectories []string          `json:"additionalDirectories,omitempty"`
}

type NewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type PromptResponse struct {
	StopReason StopReason `json:"stopReason"`
}

type CancelNotification struct {
	SessionID string `json:"sessionId"`
}

type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonRefusal   StopReason = "refusal"
	StopReasonCancelled StopReason = "cancelled"
)

// ContentBlock is one piece of a message.
type ContentBlock struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	Name     string            `json:"name,omitempty"`
	URI      string            `json:"uri,omitempty"`
	MIMEType string            `json:"mimeType,omitempty"`
	Data     string            `json:"data,omitempty"`
	Resource *EmbeddedResource `json:"resource,omitempty"`
}

// EmbeddedResource carries either text or a base64-encoded blob. Pointers
// distinguish an empty value from a missing variant.
type EmbeddedResource struct {
	URI      string  `json:"uri"`
	MIMEType string  `json:"mimeType,omitempty"`
	Text     *string `json:"text,omitempty"`
	Blob     *string `json:"blob,omitempty"`
}

// MarshalJSON emits the fields the block's variant requires, which differ
// between variants and are required even when empty.
func (c ContentBlock) MarshalJSON() ([]byte, error) {
	switch c.Type {
	case "text":
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: c.Type, Text: c.Text})
	case "resource_link":
		return json.Marshal(struct {
			Type string `json:"type"`
			Name string `json:"name"`
			URI  string `json:"uri"`
		}{Type: c.Type, Name: c.Name, URI: c.URI})
	case "image", "audio":
		return json.Marshal(struct {
			Type     string `json:"type"`
			MIMEType string `json:"mimeType"`
			Data     string `json:"data"`
		}{Type: c.Type, MIMEType: c.MIMEType, Data: c.Data})
	case "resource":
		return json.Marshal(struct {
			Type     string            `json:"type"`
			Resource *EmbeddedResource `json:"resource"`
		}{Type: c.Type, Resource: c.Resource})
	default:
		type raw ContentBlock
		return json.Marshal(raw(c))
	}
}

const (
	SessionUpdateAgentMessageChunk = "agent_message_chunk"
	SessionUpdateAgentThoughtChunk = "agent_thought_chunk"
)

// ContentChunk is a streamed piece of a message. Chunks sharing a message ID
// belong to the same message.
type ContentChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
	MessageID     string       `json:"messageId,omitempty"`
}

type SessionNotification struct {
	SessionID string       `json:"sessionId"`
	Update    ContentChunk `json:"update"`
}
