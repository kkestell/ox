// Package agent is the contract between the server and a harness: an Agent
// runs one harness process for one session, accepts prompts, approvals, and
// settings, and publishes the session's progress to its subscribers. Each
// harness package implements it and provides a Start function.
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

// Agent is one running harness process for one session.
type Agent interface {
	// Inspect calls inspect on the agent's goroutine, between updates: the
	// snapshot reflects every update published before the call and none after.
	Inspect(inspect func(Snapshot)) error
	// Send writes a prompt to an idle agent and returns its message ID. It
	// returns ErrBusy unless the agent is idle with no prompt or
	// configuration in progress.
	Send(text string) (string, error)
	// Approve answers a pending approval, or returns ErrApprovalNotFound.
	Approve(requestID string, response json.RawMessage) error
	// Configure applies settings to the running process. ErrExited means the
	// process is gone; the caller stores them for the next start.
	Configure(Settings) error
	// Stop terminates the process and waits for the agent to settle.
	Stop() error
	// Done is closed once the process has exited and the agent has published
	// its last update, parked.
	Done() <-chan struct{}
}

// Start starts a harness process and returns its agent.
type Start func(Options) (Agent, error)

type Options struct {
	Workspace string
	// Resume is the harness's token for the conversation to continue; empty
	// starts a new one.
	Resume    string
	Settings  Settings
	ParkAfter time.Duration // terminate the process once unused this long; zero never parks
	// Subscribers receive every update, on the agent's goroutine.
	Subscribers []Subscriber
	// OnResumable receives the token that resumes a new conversation, once
	// the harness has saved enough to resume it. It is called at most once,
	// on the agent's goroutine.
	OnResumable func(token string)
}

// Settings are a session's model, effort, and permission mode.
type Settings struct {
	Model          string
	Effort         string
	PermissionMode string
}

// Subscriber receives an agent's updates.
type Subscriber interface {
	// Publish delivers an update without blocking. Returning false detaches
	// the subscriber.
	Publish(Update) bool
}

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

// Snapshot is an agent's current state, returned by Inspect. State is
// running, requires_action, or idle.
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
