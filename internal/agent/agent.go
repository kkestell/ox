// Package agent runs one Claude Code process as an agent: a state machine that
// accepts prompts, approvals, and configuration, and publishes the prompt's
// progress to attached subscribers. One goroutine owns all of an agent's state
// and handles client requests and Claude's output as events in order.
package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/claude"
)

var (
	ErrExited           = errors.New("agent has exited")
	ErrBusy             = errors.New("agent is busy")
	ErrApprovalNotFound = errors.New("approval request is no longer pending")
)

// phase is the agent's ownership of its Claude process. Claude's reported
// session state is tracked separately in Agent.claudeState.
type phase int

const (
	available   phase = iota // nothing owns the process
	queued                   // prompt written; Claude has not taken it yet
	prompting                // Claude took the prompt; its next idle state releases it
	configuring              // control requests in flight, one at a time
	parking                  // idle too long; the process is being terminated
	exited
)

// Subscriber receives an agent's updates.
type Subscriber interface {
	// Publish delivers an update without blocking. Returning false detaches
	// the subscriber.
	Publish(Update) bool
}

type Agent struct {
	proc   *claude.Process
	input  *claude.Input
	events chan any
	done   chan struct{} // closed once the process has exited and its state is settled

	// Owned by the run goroutine.
	phase       phase
	claudeState string
	prompt      *submission
	config      *configuration
	subscribers map[Subscriber]struct{}
	parkAfter   time.Duration // zero never parks
	background  int           // Claude's live background tasks
	schedules   int           // Claude's pending schedules, from the last Stop hook
	scheduling  bool          // a scheduling tool ran since the last Stop hook
	parkTimer   int           // generation of the armed park timer; zero when unarmed
	timers      int           // timer generations issued
}

// Snapshot is an agent's current state, returned on attach.
type Snapshot struct {
	State  string          `json:"state"`
	Prompt *PromptSnapshot `json:"prompt"`
}

type PromptSnapshot struct {
	MessageID string     `json:"message_id"`
	Approvals []Approval `json:"approvals"`
}

type Approval struct {
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// Events from clients. Each carries a reply that the run goroutine always
// answers.
type (
	attachRequest struct {
		subscriber Subscriber
		attached   func(Snapshot)
		reply      chan struct{}
	}
	sendRequest struct {
		subscriber Subscriber
		text       string
		reply      chan sendReply
	}
	sendReply struct {
		id  string
		err error
	}
	approveRequest struct {
		requestID string
		response  json.RawMessage
		reply     chan error
	}
	configureRequest struct {
		config Configuration
		reply  chan error
	}
	controlTimeout struct{ requestID string }
	parkTimeout    struct{ timer int }
)

// Start starts and initializes a Claude process for a new agent. The agent
// terminates the process once it has been parkable for parkAfter; zero never
// parks.
func Start(options claude.Options, parkAfter time.Duration) (*Agent, error) {
	proc, err := claude.Start(options)
	if err != nil {
		return nil, err
	}
	a := newAgent(proc.Input)
	a.proc = proc
	a.parkAfter = parkAfter
	go a.run()
	return a, nil
}

func newAgent(input *claude.Input) *Agent {
	return &Agent{
		input: input, events: make(chan any), done: make(chan struct{}),
		phase: available, claudeState: "idle", subscribers: make(map[Subscriber]struct{}),
	}
}

func (a *Agent) run() {
	defer close(a.done)
	output := a.proc.Output()
	for a.phase != exited {
		select {
		case message, ok := <-output:
			if !ok {
				a.exit(a.proc.Err())
				continue
			}
			a.handleOutput(message)
		case event := <-a.events:
			a.handle(event)
		}
		a.armPark()
	}
}

func (a *Agent) handle(event any) {
	switch e := event.(type) {
	case attachRequest:
		e.attached(a.snapshot())
		a.subscribers[e.subscriber] = struct{}{}
		e.reply <- struct{}{}
	case sendRequest:
		id, err := a.submit(e.subscriber, e.text)
		e.reply <- sendReply{id, err}
	case approveRequest:
		e.reply <- a.answer(e.requestID, e.response)
	case configureRequest:
		a.reconfigure(e.config, e.reply)
	case controlTimeout:
		a.controlTimedOut(e.requestID)
	case parkTimeout:
		a.parkTimedOut(e.timer)
	}
}

func (a *Agent) handleOutput(message claude.Message) {
	switch message.Type {
	case "control_response":
		a.controlResponse(message.Response)
	case "control_request":
		a.controlRequest(message.RequestID, message.Request)
	case "control_cancel_request":
		a.cancelApproval(message.RequestID)
	case "command_lifecycle":
		if message.State == "started" {
			a.commandStarted(message.CommandUUID)
		}
	case "system":
		switch message.Subtype {
		case "session_state_changed":
			switch message.State {
			case "running", "requires_action", "idle":
				a.sessionState(message.State)
			}
		case "background_tasks_changed":
			a.background = len(message.Tasks)
		}
	case "assistant":
		for _, name := range message.ToolNames() {
			switch name {
			case "CronCreate", "CronDelete", "ScheduleWakeup":
				a.scheduling = true
			}
		}
	case "stream_event":
		delta := message.Event.Delta
		if message.ParentToolUseID == "" && message.Event.Type == "content_block_delta" && delta.Type == "text_delta" && delta.Text != "" {
			a.publish(Update{Type: "text_delta", Text: delta.Text})
		}
	case "result":
		result := Result{Text: message.Result, IsError: message.IsError || message.Subtype != "success"}
		if result.IsError && result.Text == "" {
			result.Text = strings.Join(message.Errors, "\n")
			if result.Text == "" {
				result.Text = message.Subtype
			}
		}
		a.publish(Update{Type: "result", Result: &result})
	}
}

// idle reports whether the agent can take a prompt or configuration.
func (a *Agent) idle() bool {
	return a.phase == available && a.claudeState == "idle"
}

func (a *Agent) snapshot() Snapshot {
	snapshot := Snapshot{State: a.claudeState}
	if a.prompt != nil {
		prompt := &PromptSnapshot{MessageID: a.prompt.id, Approvals: []Approval{}}
		for id, request := range a.prompt.approvals {
			prompt.Approvals = append(prompt.Approvals, Approval{id, request})
		}
		snapshot.Prompt = prompt
	}
	return snapshot
}

func (a *Agent) exit(err error) {
	reason := "Claude exited"
	if err != nil {
		reason += ": " + err.Error()
	}
	switch a.phase {
	case queued, prompting:
		a.endPrompt(Update{Type: "exited", Error: reason})
	case configuring:
		a.endConfiguration(a.config.fail(ErrExited))
	}
	a.phase = exited
}

// post delivers an event to the run goroutine. It reports false once the
// agent has exited.
func (a *Agent) post(event any) bool {
	select {
	case a.events <- event:
		return true
	case <-a.done:
		return false
	}
}

// Attach subscribes s to the agent's updates. attached receives the agent's
// current state before s receives any update.
func (a *Agent) Attach(s Subscriber, attached func(Snapshot)) error {
	reply := make(chan struct{}, 1)
	if !a.post(attachRequest{s, attached, reply}) {
		return ErrExited
	}
	<-reply
	return nil
}

// Send writes a prompt to an idle agent and attaches s to its updates.
func (a *Agent) Send(s Subscriber, text string) (string, error) {
	reply := make(chan sendReply, 1)
	if !a.post(sendRequest{s, text, reply}) {
		return "", ErrExited
	}
	r := <-reply
	return r.id, r.err
}

func (a *Agent) Approve(requestID string, response json.RawMessage) error {
	reply := make(chan error, 1)
	if !a.post(approveRequest{requestID, response, reply}) {
		return ErrExited
	}
	return <-reply
}

func (a *Agent) Configure(config Configuration) error {
	reply := make(chan error, 1)
	if !a.post(configureRequest{config, reply}) {
		return ErrExited
	}
	return <-reply
}

// Stop terminates Claude and waits for the agent to settle.
func (a *Agent) Stop() error {
	if err := a.proc.Terminate(); err != nil {
		return err
	}
	<-a.done
	return nil
}

// Done is closed once the agent has exited.
func (a *Agent) Done() <-chan struct{} {
	return a.done
}
