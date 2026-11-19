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
)

// Agent runs one harness instance. The session goroutine owns all method
// calls and runs the Options callbacks during those calls.
type Agent interface {
	// Events delivers the harness's own input, such as its process's output
	// and its timers, until the agent has exited.
	Events() <-chan Event
	Handle(Event)
	Snapshot() Snapshot
	// Send writes a prompt to an idle agent and returns its message ID. It
	// returns ErrBusy unless the agent is idle with no prompt or
	// configuration in progress.
	Send(text string) (string, error)
	// Approve answers a pending approval, or returns ErrApprovalNotFound.
	Approve(requestID string, response json.RawMessage) error
	// Interrupt stops the running turn, including a prompt the harness has
	// not taken yet. The turn's end arrives as updates; an idle agent has
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
	// agent has published its last update, parked. A process that does not
	// exit when asked is killed.
	Stop() error
	// Exited reports that the process has exited and the agent has
	// published its last update, parked.
	Exited() bool
}

// Event is a harness's own input, opaque to the server.
type Event any

type Start func(Options) (Agent, error)

type Options struct {
	SessionID string // the session's ID, which the harness's own log lines name
	Workspace string
	// Resume is the harness's token for the conversation to continue; empty
	// starts a new one.
	Resume   string
	Settings Settings
	OnUpdate func(Update)
	// OnResumable receives the token that resumes a new conversation, once
	// the harness has saved enough to resume it. It is called at most once.
	OnResumable func(token string)
	// OnError receives failures that no call returns, for the session's log.
	OnError func(error)
}

type Settings struct {
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
}

// Update types.
const (
	UpdatePrompt            = "prompt"
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
	State     string          `json:"state"`
	Prompt    *PromptSnapshot `json:"prompt"`
	Approvals []Approval      `json:"approvals"`
}

type PromptSnapshot struct {
	MessageID string `json:"message_id"`
}

type Approval struct {
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}
