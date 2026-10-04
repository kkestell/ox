package acp

import "encoding/json"

// Method names.
const (
	MethodInitialize        = "initialize"
	MethodNewSession        = "session/new"
	MethodLoadSession       = "session/load"
	MethodListSessions      = "session/list"
	MethodCloseSession      = "session/close"
	MethodDeleteSession     = "session/delete"
	MethodSetConfigOption   = "session/set_config_option"
	MethodPrompt            = "session/prompt"
	MethodCancel            = "session/cancel"
	MethodUpdate            = "session/update"
	MethodRequestPermission = "session/request_permission"
)

// ProtocolVersion is the one ACP version Ox speaks.
const ProtocolVersion = 1

type SessionRequest struct {
	SessionID string `json:"sessionId"`
}

type NewSessionRequest struct {
	Cwd string `json:"cwd"`
}

type LoadSessionRequest struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

type ListSessionsRequest struct {
	Cwd string `json:"cwd"`
}

type SetConfigOptionRequest struct {
	SessionID string          `json:"sessionId"`
	ConfigID  string          `json:"configId"`
	Value     json.RawMessage `json:"value"`
}

type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// ContentBlock is text, an image, or a resource link.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Name     string `json:"name,omitempty"`
	URI      string `json:"uri,omitempty"`
}

func TextBlock(text string) ContentBlock { return ContentBlock{Type: "text", Text: text} }

type SessionResponse struct {
	SessionID     string         `json:"sessionId,omitempty"`
	ConfigOptions []ConfigOption `json:"configOptions"`
}

type SessionInfo struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type ListSessionsResponse struct {
	Sessions []SessionInfo `json:"sessions"`
}

type PromptResponse struct {
	StopReason string `json:"stopReason"`
}

type ConfigOption struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Category     string         `json:"category"`
	Type         string         `json:"type"`
	CurrentValue string         `json:"currentValue"`
	Options      []ConfigChoice `json:"options"`
}

type ConfigChoice struct {
	Value       string         `json:"value"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

type SessionNotification struct {
	SessionID string `json:"sessionId"`
	Update    any    `json:"update"`
}

// Chunk is a user_message_chunk, agent_message_chunk, or agent_thought_chunk
// update.
type Chunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
}

// ToolCall is a tool_call update, or a tool_call_update when SessionUpdate
// says so. Empty fields are omitted; a new call's kind defaults to other and
// its status to pending.
type ToolCall struct {
	SessionUpdate string            `json:"sessionUpdate,omitempty"`
	ToolCallID    string            `json:"toolCallId"`
	Title         string            `json:"title,omitempty"`
	Name          string            `json:"name,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Status        string            `json:"status,omitempty"`
	Content       []ToolCallContent `json:"content,omitempty"`
	RawInput      any               `json:"rawInput,omitempty"`
	RawOutput     any               `json:"rawOutput,omitempty"`
}

// ToolCallContent is a content block, or a diff when Type is "diff".
type ToolCallContent struct {
	Type    string        `json:"type"`
	Content *ContentBlock `json:"content,omitempty"`
	Path    string        `json:"path,omitempty"`
	OldText *string       `json:"oldText,omitempty"`
	NewText *string       `json:"newText,omitempty"`
}

type AvailableCommandsUpdate struct {
	SessionUpdate     string             `json:"sessionUpdate"`
	AvailableCommands []AvailableCommand `json:"availableCommands"`
}

type AvailableCommand struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Input       *CommandInput `json:"input,omitempty"`
}

type CommandInput struct {
	Hint string `json:"hint"`
}

type UsageUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	Used          uint64 `json:"used"`
	Size          int    `json:"size"`
	Cost          *Cost  `json:"cost,omitempty"`
}

type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type SessionInfoUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	Title         string `json:"title,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type RequestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCall           `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type RequestPermissionResponse struct {
	Outcome struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	} `json:"outcome"`
}
