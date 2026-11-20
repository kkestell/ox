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
	// Send writes input's Content with its session-assigned ID. It publishes
	// prompt_queued after writing, then prompt or prompt_cancelled on uptake
	// or drop. Each update is built with Input.Update, so it carries the
	// input's Text, From, and FromLabel. Snapshot().Queued reports the input
	// from the write until it is taken or dropped. Send returns ErrBusy
	// during configuration or with an untaken input.
	Send(input Input) error
	// Approve answers a pending approval, or returns ErrApprovalNotFound.
	Approve(requestID string, response json.RawMessage) error
	// Interrupt stops the running turn and drops a queued input with
	// prompt_cancelled. The turn's end arrives as updates; an idle agent has
	// nothing to stop.
	Interrupt() error
	// Configure applies the changed fields and calls done with the outcome,
	// now or from a later Handle. Already applied changes are reported through
	// OnConfigured even if a later change fails. Configure reports ErrBusy unless
	// idle, or ErrExited if the process exits first.
	Configure(patch SettingsPatch, done func(error))
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

// Harness starts an agent and may check user prompts before starting it.
type Harness struct {
	Start       func(Options) (Agent, error)
	CheckPrompt func(string) error // nil means the harness accepts all prompts
}

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
	// OnResumable receives the token when a newly observed conversation is
	// saved and can be resumed, including when the conversation changes.
	OnResumable func(token string)
	// OnConfigured receives every settings change the harness makes, whether
	// it makes the change itself or acknowledges a Configure control.
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

// SettingsPatch names only the fields the request changes.
type SettingsPatch struct {
	Model          *string `json:"model"`
	Effort         *string `json:"effort"`
	PermissionMode *string `json:"permission_mode"`
}

func (p SettingsPatch) Apply(s Settings) Settings {
	if p.Model != nil {
		s.Model = *p.Model
	}
	if p.Effort != nil {
		s.Effort = *p.Effort
	}
	if p.PermissionMode != nil {
		s.PermissionMode = *p.PermissionMode
	}
	return s
}

// Update types.
const (
	UpdatePrompt            = "prompt"           // the harness took an input
	UpdatePromptQueued      = "prompt_queued"    // an input was accepted; the harness has not taken it yet
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
	FromLabel string          `json:"from_label,omitempty"`
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
	// Queued is the input written to the harness and not yet taken. It is not
	// a snapshot field in the protocol: session/get lists it in queued, ahead
	// of the session's waiting messages.
	Queued *Input `json:"-"`
}

// Input is the user prompt or wrapped message the harness writes to the model.
type Input struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
	From      string `json:"from,omitempty"` // the sending session of a message
	FromLabel string `json:"from_label,omitempty"`
	Content   string `json:"-"` // what the model receives
}

// Update builds a lifecycle update of kind for the input. Its Time is left for
// the publisher.
func (i Input) Update(kind string) Update {
	return Update{Type: kind, MessageID: i.MessageID, Text: i.Text, From: i.From, FromLabel: i.FromLabel}
}

type Approval struct {
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}
