package agent

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrExited           = errors.New("agent has exited")
	ErrBusy             = errors.New("agent is busy")
	ErrApprovalNotFound = errors.New("approval request is no longer pending")
	ErrNoConversation   = errors.New("the conversation to resume no longer exists")
)

// Agent runs one harness instance. The session goroutine owns all method
// calls and runs the Options callbacks during those calls.
type Agent interface {
	// Events delivers the harness's own input, such as its process's output
	// and its timers, until the agent has exited.
	Events() <-chan Event
	Handle(Event)
	Snapshot() Snapshot
	// Send writes input's Content and returns its message ID. The harness
	// takes it into the running turn, or starts a turn with it, and publishes
	// its Text and From. Send returns ErrBusy while an input the harness has
	// not taken yet is queued, or while a configuration is in progress.
	Send(input Input) (string, error)
	// Approve answers a pending approval, or returns ErrApprovalNotFound.
	Approve(requestID string, response json.RawMessage) error
	// Interrupt stops the running turn and drops a queued input with
	// prompt_cancelled. The turn's end arrives as updates; an idle agent has
	// nothing to stop.
	Interrupt() error
	// Configure applies settings to the running process and calls done, now
	// or from a later Handle, with the settings the process then uses and the
	// outcome: ErrBusy unless the agent is idle, and ErrExited if the process
	// exits first. A failure can leave some of the settings applied.
	Configure(settings Settings, done func(applied Settings, err error))
	// Parkable reports that nothing is using the process, so stopping it
	// loses no work.
	Parkable() bool
	// Stop shuts the process down and returns once it has exited and the
	// agent has published its last update. A process that does not exit when
	// asked is killed.
	Stop() error
	// Exited reports that the process has exited and the agent has
	// published its last update.
	Exited() bool
}

// Event is a harness's own input, opaque to the server.
type Event any

type Start func(Options) (Agent, error)

type Options struct {
	SessionID string // the session's ID, which the harness's own log lines name
	Workspace string
	// Resume is the harness's token for the conversation to continue; empty
	// starts a new one. Start returns ErrNoConversation when the harness has
	// no conversation for it.
	Resume   string
	Settings Settings
	MCP      MCPServer
	OnUpdate func(Update)
	// OnResumable receives the token that resumes a new conversation, once
	// the harness has saved enough to resume it. It is called at most once.
	OnResumable func(token string)
	// OnConfigured receives the settings the process now uses when the harness
	// changes them itself, without a Configure call, such as leaving plan mode
	// once its plan is approved. It is called during Handle.
	OnConfigured func(Settings)
	// OnError receives failures that no call returns, for the session's log.
	OnError func(error)
}

// MCPServer is Ox's MCP server for one session: a stdio command the harness
// registers for its agent.
type MCPServer struct {
	Command  string            // absolute path of the forwarder
	Env      map[string]string // the forwarder's environment
	Approved []string          // tools that run without an approval prompt where the permission mode allows
}

type Settings struct {
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
}

// Update types.
const (
	UpdatePrompt            = "prompt"           // the harness took an input
	UpdatePromptQueued      = "prompt_queued"    // an input was written; the harness has not taken it yet
	UpdatePromptCancelled   = "prompt_cancelled" // a queued input was dropped before the harness took it
	UpdateState             = "state"
	UpdateTextDelta         = "text_delta"
	UpdateToolUse           = "tool_use"
	UpdateToolResult        = "tool_result"
	UpdateResult            = "result"
	UpdateApproval          = "approval"
	UpdateApprovalAnswered  = "approval_answered"
	UpdateApprovalCancelled = "approval_cancelled"
	UpdateExited            = "exited"
	UpdateParked            = "parked"
)

// Execution states, which state updates and snapshots carry.
const (
	StateIdle           = "idle"
	StateRunning        = "running"
	StateRequiresAction = "requires_action"
	StateParked         = "parked" // snapshots only: no process is running
)

type Update struct {
	MessageID string          `json:"message_id,omitempty"` // empty for turns the harness started itself
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	From      string          `json:"from,omitempty"` // the sending session of a message's prompt updates
	State     string          `json:"state,omitempty"`
	Result    *Result         `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Request   json.RawMessage `json:"request,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Time      time.Time       `json:"time,omitzero"` // when the agent published it
}

type Result struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error"`
}

type Snapshot struct {
	State     string     `json:"state"`
	Approvals []Approval `json:"approvals"`
	Queued    *Prompt    `json:"queued,omitempty"`
}

// Input is what a session gives its harness: the user's prompt, or a
// message from another session.
type Input struct {
	Text    string // as the transcript shows it
	From    string // the sending session's ID; empty for the user's prompt
	Content string // what the model receives: Text for a prompt, Text wrapped with its sender for a message
}

// Prompt is an input the harness has written, named by its message ID.
type Prompt struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
	From      string `json:"from,omitempty"`
}

type Approval struct {
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}
