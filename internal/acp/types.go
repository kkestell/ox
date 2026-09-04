package acp

import "encoding/json"

const ProtocolVersion = 1

const (
	ErrCodeAuthRequired            = -32000
	ErrCodeRequestCancelled        = -32800
	MetaBlockIndex                 = "kkestell.ox/blockIndex"
	MetaMessageID                  = "kkestell.ox/messageId"
	MetaOutcome                    = "kkestell.ox/outcome"
	MetaParentToolCallID           = "kkestell.ox/parentToolCallId"
	MetaSubagent                   = "kkestell.ox/subagent"
	MethodFSReadTextFile           = "fs/read_text_file"
	MethodFSWriteTextFile          = "fs/write_text_file"
	MethodSessionRequestPermission = "session/request_permission"
)

type Metadata map[string]any

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Title   string `json:"title,omitempty"`
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

type AgentCapabilities struct {
	PromptCapabilities  *PromptCapabilities    `json:"promptCapabilities,omitempty"`
	SessionCapabilities *SessionCapabilities   `json:"sessionCapabilities,omitempty"`
	LoadSession         bool                   `json:"loadSession"`
	Auth                *AgentAuthCapabilities `json:"auth,omitempty"`
	Meta                Metadata               `json:"_meta,omitempty"`
}

type AgentAuthCapabilities struct {
	Logout *LogoutCapabilities `json:"logout,omitempty"`
	Meta   Metadata            `json:"_meta,omitempty"`
}

type LogoutCapabilities struct {
	Meta Metadata `json:"_meta,omitempty"`
}

type PromptCapabilities struct {
	Image           bool     `json:"image"`
	Audio           bool     `json:"audio"`
	EmbeddedContext bool     `json:"embeddedContext"`
	Meta            Metadata `json:"_meta,omitempty"`
}

type SessionCapabilities struct {
	List   *SessionListCapabilities   `json:"list,omitempty"`
	Delete *SessionDeleteCapabilities `json:"delete,omitempty"`
	Resume *SessionResumeCapabilities `json:"resume,omitempty"`
	Close  *SessionCloseCapabilities  `json:"close,omitempty"`
	Meta   Metadata                   `json:"_meta,omitempty"`
}

type (
	SessionListCapabilities   struct{}
	SessionDeleteCapabilities struct{}
	SessionResumeCapabilities struct{}
	SessionCloseCapabilities  struct{}
)

type InitializeRequest struct {
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities *ClientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         *Implementation     `json:"clientInfo,omitempty"`
	Meta               Metadata            `json:"_meta,omitempty"`
}

type InitializeResponse struct {
	ProtocolVersion   int                `json:"protocolVersion"`
	AgentCapabilities *AgentCapabilities `json:"agentCapabilities,omitempty"`
	AuthMethods       []AuthMethod       `json:"authMethods,omitempty"`
	AgentInfo         *Implementation    `json:"agentInfo,omitempty"`
	Meta              Metadata           `json:"_meta,omitempty"`
}

type ReadTextFileRequest struct {
	SessionID string   `json:"sessionId"`
	Path      string   `json:"path"`
	Line      *int     `json:"line,omitempty"`
	Limit     *int     `json:"limit,omitempty"`
	Meta      Metadata `json:"_meta,omitempty"`
}

type ReadTextFileResponse struct {
	Content string   `json:"content"`
	Meta    Metadata `json:"_meta,omitempty"`
}

type WriteTextFileRequest struct {
	SessionID string   `json:"sessionId"`
	Path      string   `json:"path"`
	Content   string   `json:"content"`
	Meta      Metadata `json:"_meta,omitempty"`
}

type WriteTextFileResponse struct {
	Meta Metadata `json:"_meta,omitempty"`
}

type AuthMethod struct {
	ID          string   `json:"id"`
	Type        string   `json:"type,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Args        []string `json:"args,omitempty"`
	Meta        Metadata `json:"_meta,omitempty"`
}

type AuthenticateRequest struct {
	MethodID string   `json:"methodId"`
	Meta     Metadata `json:"_meta,omitempty"`
}

type AuthenticateResponse struct {
	Meta Metadata `json:"_meta,omitempty"`
}

type LogoutRequest struct {
	Meta Metadata `json:"_meta,omitempty"`
}

type LogoutResponse struct {
	Meta Metadata `json:"_meta,omitempty"`
}

type NewSessionRequest struct {
	CWD                   string            `json:"cwd"`
	AdditionalDirectories []string          `json:"additionalDirectories,omitempty"`
	MCPServers            []json.RawMessage `json:"mcpServers"`
	Meta                  Metadata          `json:"_meta,omitempty"`
}

type NewSessionResponse struct {
	SessionID string   `json:"sessionId"`
	Meta      Metadata `json:"_meta,omitempty"`
}

type LoadSessionRequest struct {
	SessionID             string            `json:"sessionId"`
	CWD                   string            `json:"cwd"`
	AdditionalDirectories []string          `json:"additionalDirectories,omitempty"`
	MCPServers            []json.RawMessage `json:"mcpServers"`
	Meta                  Metadata          `json:"_meta,omitempty"`
}

type LoadSessionResponse struct {
	Meta Metadata `json:"_meta,omitempty"`
}

type ResumeSessionRequest struct {
	SessionID             string            `json:"sessionId"`
	CWD                   string            `json:"cwd"`
	AdditionalDirectories []string          `json:"additionalDirectories,omitempty"`
	MCPServers            []json.RawMessage `json:"mcpServers,omitempty"`
	Meta                  Metadata          `json:"_meta,omitempty"`
}

type ResumeSessionResponse struct {
	Meta Metadata `json:"_meta,omitempty"`
}

type ListSessionsRequest struct {
	CWD    string   `json:"cwd,omitempty"`
	Cursor string   `json:"cursor,omitempty"`
	Meta   Metadata `json:"_meta,omitempty"`
}

type ListSessionsResponse struct {
	Sessions   []SessionInfo `json:"sessions"`
	NextCursor string        `json:"nextCursor,omitempty"`
	Meta       Metadata      `json:"_meta,omitempty"`
}

type SessionInfo struct {
	SessionID string   `json:"sessionId"`
	CWD       string   `json:"cwd"`
	Title     string   `json:"title,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
	Meta      Metadata `json:"_meta,omitempty"`
}

type CloseSessionRequest struct {
	SessionID string   `json:"sessionId"`
	Meta      Metadata `json:"_meta,omitempty"`
}

type CloseSessionResponse struct {
	Meta Metadata `json:"_meta,omitempty"`
}

type DeleteSessionRequest struct {
	SessionID string   `json:"sessionId"`
	Meta      Metadata `json:"_meta,omitempty"`
}

type DeleteSessionResponse struct {
	Meta Metadata `json:"_meta,omitempty"`
}

type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
	Meta      Metadata       `json:"_meta,omitempty"`
}

type PromptResponse struct {
	StopReason StopReason `json:"stopReason"`
	Usage      *Usage     `json:"usage,omitempty"`
	Meta       Metadata   `json:"_meta,omitempty"`
}

type CancelNotification struct {
	SessionID string   `json:"sessionId"`
	Meta      Metadata `json:"_meta,omitempty"`
}

type CancelRequestNotification struct {
	RequestID json.RawMessage `json:"requestId"`
}

type RequestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallUpdate     `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
	Meta      Metadata           `json:"_meta,omitempty"`
}

type PermissionOption struct {
	OptionID string               `json:"optionId"`
	Name     string               `json:"name"`
	Kind     PermissionOptionKind `json:"kind"`
	Meta     Metadata             `json:"_meta,omitempty"`
}

type PermissionOptionKind string

const (
	PermissionOptionAllowOnce    PermissionOptionKind = "allow_once"
	PermissionOptionAllowAlways  PermissionOptionKind = "allow_always"
	PermissionOptionRejectOnce   PermissionOptionKind = "reject_once"
	PermissionOptionRejectAlways PermissionOptionKind = "reject_always"
)

type RequestPermissionResponse struct {
	Outcome RequestPermissionOutcome `json:"outcome"`
	Meta    Metadata                 `json:"_meta,omitempty"`
}

type RequestPermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

type ContentBlock struct {
	Type        string            `json:"type"`
	Annotations any               `json:"annotations,omitempty"`
	Text        string            `json:"text,omitempty"`
	Name        string            `json:"name,omitempty"`
	URI         string            `json:"uri,omitempty"`
	Description *string           `json:"description,omitempty"`
	MIMEType    string            `json:"mimeType,omitempty"`
	Data        string            `json:"data,omitempty"`
	Resource    *EmbeddedResource `json:"resource,omitempty"`
	Size        *int64            `json:"size,omitempty"`
	Title       *string           `json:"title,omitempty"`
	Meta        Metadata          `json:"_meta,omitempty"`
}

type EmbeddedResource struct {
	URI      string  `json:"uri"`
	MIMEType string  `json:"mimeType,omitempty"`
	Text     *string `json:"text,omitempty"`
	Blob     *string `json:"blob,omitempty"`
}

func (c ContentBlock) MarshalJSON() ([]byte, error) {
	switch c.Type {
	case "text":
		return json.Marshal(struct {
			Type        string   `json:"type"`
			Annotations any      `json:"annotations,omitempty"`
			Text        string   `json:"text"`
			Meta        Metadata `json:"_meta,omitempty"`
		}{
			Type:        c.Type,
			Annotations: c.Annotations,
			Text:        c.Text,
			Meta:        c.Meta,
		})
	case "resource_link":
		return json.Marshal(struct {
			Type        string   `json:"type"`
			Annotations any      `json:"annotations,omitempty"`
			Name        string   `json:"name"`
			URI         string   `json:"uri"`
			Description *string  `json:"description,omitempty"`
			MIMEType    string   `json:"mimeType,omitempty"`
			Size        *int64   `json:"size,omitempty"`
			Title       *string  `json:"title,omitempty"`
			Meta        Metadata `json:"_meta,omitempty"`
		}{
			Type:        c.Type,
			Annotations: c.Annotations,
			Name:        c.Name,
			URI:         c.URI,
			Description: c.Description,
			MIMEType:    c.MIMEType,
			Size:        c.Size,
			Title:       c.Title,
			Meta:        c.Meta,
		})
	case "image", "audio":
		return json.Marshal(struct {
			Type        string   `json:"type"`
			Annotations any      `json:"annotations,omitempty"`
			MIMEType    string   `json:"mimeType"`
			Data        string   `json:"data"`
			Meta        Metadata `json:"_meta,omitempty"`
		}{c.Type, c.Annotations, c.MIMEType, c.Data, c.Meta})
	case "resource":
		return json.Marshal(struct {
			Type        string            `json:"type"`
			Annotations any               `json:"annotations,omitempty"`
			Resource    *EmbeddedResource `json:"resource"`
			Meta        Metadata          `json:"_meta,omitempty"`
		}{c.Type, c.Annotations, c.Resource, c.Meta})
	default:
		type raw ContentBlock
		return json.Marshal(raw(c))
	}
}

type SessionNotification struct {
	SessionID string   `json:"sessionId"`
	Update    any      `json:"update"`
	Meta      Metadata `json:"_meta,omitempty"`
}

type AgentMessageChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
	MessageID     string       `json:"messageId,omitempty"`
	Meta          Metadata     `json:"_meta,omitempty"`
}

type UserMessageChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
	MessageID     string       `json:"messageId,omitempty"`
	Meta          Metadata     `json:"_meta,omitempty"`
}

type AgentThoughtChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
	MessageID     string       `json:"messageId,omitempty"`
	Meta          Metadata     `json:"_meta,omitempty"`
}

type ToolCall struct {
	SessionUpdate string          `json:"sessionUpdate"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title"`
	Name          string          `json:"name,omitempty"`
	Kind          ToolKind        `json:"kind,omitempty"`
	Status        ToolCallStatus  `json:"status,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
	Meta          Metadata        `json:"_meta,omitempty"`
}

type ToolCallUpdate struct {
	SessionUpdate string            `json:"sessionUpdate,omitempty"`
	ToolCallID    string            `json:"toolCallId"`
	Kind          ToolKind          `json:"kind,omitempty"`
	Status        ToolCallStatus    `json:"status,omitempty"`
	Title         string            `json:"title,omitempty"`
	Name          string            `json:"name,omitempty"`
	Content       []ToolCallContent `json:"content,omitempty"`
	RawInput      json.RawMessage   `json:"rawInput,omitempty"`
	Meta          Metadata          `json:"_meta,omitempty"`
}

type ToolKind string

const (
	SessionUpdateAgentMessageChunk = "agent_message_chunk"
	SessionUpdateAgentThoughtChunk = "agent_thought_chunk"
	SessionUpdateUserMessageChunk  = "user_message_chunk"

	ToolKindRead    ToolKind = "read"
	ToolKindSearch  ToolKind = "search"
	ToolKindEdit    ToolKind = "edit"
	ToolKindExecute ToolKind = "execute"
	ToolKindOther   ToolKind = "other"
)

type ToolCallContent struct {
	Type    string       `json:"type"`
	Content ContentBlock `json:"content"`
	Meta    Metadata     `json:"_meta,omitempty"`
}

type UsageUpdate struct {
	SessionUpdate string   `json:"sessionUpdate"`
	Used          uint64   `json:"used"`
	Size          uint64   `json:"size"`
	Cost          *Cost    `json:"cost,omitempty"`
	Meta          Metadata `json:"_meta,omitempty"`
}

type Cost struct {
	Amount   float64  `json:"amount"`
	Currency string   `json:"currency"`
	Meta     Metadata `json:"_meta,omitempty"`
}

type Usage struct {
	TotalTokens       uint64   `json:"totalTokens"`
	InputTokens       uint64   `json:"inputTokens"`
	OutputTokens      uint64   `json:"outputTokens"`
	ThoughtTokens     *uint64  `json:"thoughtTokens,omitempty"`
	CachedReadTokens  *uint64  `json:"cachedReadTokens,omitempty"`
	CachedWriteTokens *uint64  `json:"cachedWriteTokens,omitempty"`
	Meta              Metadata `json:"_meta,omitempty"`
}

type StopReason string

const (
	StopReasonEndTurn         StopReason = "end_turn"
	StopReasonMaxTokens       StopReason = "max_tokens"
	StopReasonMaxTurnRequests StopReason = "max_turn_requests"
	StopReasonRefusal         StopReason = "refusal"
	StopReasonCancelled       StopReason = "cancelled"
)

type ToolCallStatus string

const (
	ToolCallStatusPending    ToolCallStatus = "pending"
	ToolCallStatusInProgress ToolCallStatus = "in_progress"
	ToolCallStatusCompleted  ToolCallStatus = "completed"
	ToolCallStatusFailed     ToolCallStatus = "failed"
)
