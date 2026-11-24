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

// Style is Ox's instruction on how every harness's model writes.
const Style = "Lead with the big idea up front. " +
	"Use crisp, deadpan, military-efficient delivery. " +
	"Use dry wit with complete emotional neutrality."

// Agent runs one harness instance. The session goroutine owns all method
// calls and runs the Options callbacks during those calls.
type Agent interface {
	// Events delivers the harness's own input, such as its process's output
	// and its timers, until the agent has exited.
	Events() <-chan Event
	Handle(Event)
	Snapshot() Snapshot
	// Send queues input behind every input the harness has not taken, and
	// writes its Content with its session-assigned ID once the harness has
	// taken or dropped the inputs ahead of it, has started, and has finished
	// any configuration. A Tracker keeps the queue, publishes the input's
	// lifecycle updates, and derives the published state. Send returns an
	// error only when it writes the input at once and the write fails, or when
	// the agent has ended; it then publishes and keeps nothing.
	Send(input Input) error
	// Approve answers a pending approval, or returns ErrApprovalNotFound.
	Approve(requestID string, response json.RawMessage) error
	// Interrupt stops the running turn and drops every input the harness has
	// not taken, each with prompt_cancelled. The turn's end arrives as
	// updates; an idle agent has no turn to stop.
	Interrupt() error
	// Configure applies the changed fields and calls done with the outcome,
	// now or from a later Handle. Already applied changes are reported through
	// OnConfigured even if a later change fails. Configure reports ErrBusy unless
	// idle, or ErrExited if the process exits first.
	Configure(patch SettingsPatch, done func(error))
	// Parkable reports that nothing is using the process, so stopping it
	// loses no work.
	Parkable() bool
	// Stop ends the agent at once, dropping every input the harness has not
	// taken and publishing its last update, and asks the process to exit. A
	// process that does not exit when asked is killed.
	Stop()
	// Exited reports that the agent has ended and published its last update.
	Exited() bool
}

// Event is a harness's own input, opaque to the server.
type Event any

// Harness starts an agent and may check user prompts and the configuration
// offered for it. Start returns at once with the agent starting, and an error
// only when the harness cannot be launched; a start that fails later ends the
// agent.
type Harness struct {
	Start               func(Options) (Agent, error)
	CheckPrompt         func(string) error // nil means the harness accepts all prompts
	CheckEffort         func(string) error // nil means the harness takes every effort level
	CheckPermissionMode func(string) error // nil means the harness takes every permission mode
	TakesProviders      bool               // whether a model may set a provider order
}

type Options struct {
	SessionID string // the session's ID, which the harness's own log lines name
	Workspace string
	// Resume is the harness's token for the conversation to continue; empty
	// starts a new one.
	Resume   string
	Settings Settings
	MCP      MCPServer
	OnUpdate func(Update)
	// OnResumable receives the token when a newly observed conversation is
	// saved and can be resumed, including when the conversation changes, and
	// an empty token when the harness no longer has the conversation to
	// resume.
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
	UpdateReasoningDelta    = "reasoning_delta"
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
	StateBackground     = "background" // idle while background tasks the harness started still run
	StateParked         = "parked"     // snapshots only: the harness has stopped, though its process may still be exiting
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
	Images    []string        `json:"images,omitempty"` // the image IDs of a prompt's lifecycle updates
	Time      time.Time       `json:"time,omitzero"`    // when the agent published it
}

type Result struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error"`
}

type Snapshot struct {
	State     string     `json:"state"`
	Approvals []Approval `json:"approvals"`
	// Queued is every input the harness has not taken, oldest first.
	Queued []Input `json:"queued"`
}

// Input is the user prompt or wrapped message the harness writes to the model.
type Input struct {
	MessageID string  `json:"message_id"`
	Text      string  `json:"text"`
	From      string  `json:"from,omitempty"` // the sending session of a message
	FromLabel string  `json:"from_label,omitempty"`
	Images    []Image `json:"images,omitempty"` // a user prompt's images, which the model receives with Content
	Content   string  `json:"-"`                // what the model receives
}

// Update builds a lifecycle update of kind for the input. Its Time is left for
// the publisher.
func (i Input) Update(kind string) Update {
	return Update{Type: kind, MessageID: i.MessageID, Text: i.Text, From: i.From, FromLabel: i.FromLabel, Images: i.ImageIDs()}
}

// ImageIDs is the IDs of the input's images, or nil when it has none.
func (i Input) ImageIDs() []string {
	var ids []string
	for _, image := range i.Images {
		ids = append(ids, image.ID)
	}
	return ids
}

// Image is an image attached to a user prompt, stored under its ID. Its type
// is PNG, JPEG, GIF, or WebP, taken from its bytes. It marshals as its ID, so
// updates and snapshots carry the IDs of the data the harness writes.
type Image struct {
	ID       string
	MimeType string
	Data     []byte
}

func (i Image) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.ID)
}

type Approval struct {
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}
