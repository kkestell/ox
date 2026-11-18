package claude

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
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

// Agent runs one Claude process as a state machine. One goroutine owns all of
// its state and handles client requests and Claude's output as events in
// order.
type Agent struct {
	proc   *Process
	input  *Input
	events chan any
	done   chan struct{} // closed once the process has exited and its state is settled

	// Owned by the run goroutine.
	phase       phase
	claudeState string
	prompt      *submission
	approvals   map[string]json.RawMessage // pending, by Claude's request ID
	config      *configuration
	subscribers map[agent.Subscriber]struct{}
	onResumable func(string) // set until the new conversation's first result
	sessionID   string
	parkAfter   time.Duration // zero never parks
	background  int           // Claude's live background tasks
	schedules   int           // Claude's pending schedules, from the last Stop hook
	scheduling  bool          // a scheduling tool ran since the last Stop hook
	parkTimer   int           // generation of the armed park timer; zero when unarmed
	timers      int           // timer generations issued
}

// Events from clients. Each carries a reply that the run goroutine always
// answers.
type (
	inspectRequest struct {
		inspect func(agent.Snapshot)
		reply   chan struct{}
	}
	sendRequest struct {
		text  string
		reply chan sendReply
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
		config agent.Settings
		reply  chan error
	}
	controlTimeout struct{ requestID string }
	parkTimeout    struct{ timer int }
)

// Start starts and initializes a Claude process and returns its agent. The
// resume token is Claude's session ID: a new conversation gets a fresh one,
// reported to OnResumable once the first result shows Claude has written the
// transcript.
func Start(options agent.Options) (agent.Agent, error) {
	sessionID, resume := options.Resume, options.Resume != ""
	if !resume {
		sessionID = NewUUID()
	}
	proc, err := startProcess(options.Workspace, sessionID, resume, options.Settings)
	if err != nil {
		return nil, err
	}
	a := newAgent(proc.Input)
	a.proc = proc
	a.sessionID = sessionID
	a.parkAfter = options.ParkAfter
	if !resume {
		a.onResumable = options.OnResumable
	}
	for _, s := range options.Subscribers {
		a.subscribers[s] = struct{}{}
	}
	go a.run()
	return a, nil
}

func newAgent(input *Input) *Agent {
	return &Agent{
		input: input, events: make(chan any), done: make(chan struct{}),
		phase: available, claudeState: "idle", approvals: make(map[string]json.RawMessage),
		subscribers: make(map[agent.Subscriber]struct{}),
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
	case inspectRequest:
		e.inspect(a.snapshot())
		e.reply <- struct{}{}
	case sendRequest:
		id, err := a.submit(e.text)
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

func (a *Agent) handleOutput(message Message) {
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
		for _, use := range message.ToolUses() {
			switch use.Name {
			case "CronCreate", "CronDelete", "ScheduleWakeup":
				a.scheduling = true
			}
			if message.ParentToolUseID == "" {
				a.publish(agent.Update{Type: "tool_use", ToolUseID: use.ID, ToolName: use.Name, Input: use.Input})
			}
		}
	case "user":
		if message.ParentToolUseID == "" {
			for _, r := range message.ToolResults() {
				a.publish(agent.Update{Type: "tool_result", ToolUseID: r.ToolUseID, Result: &agent.Result{Text: r.Text, IsError: r.IsError}})
			}
		}
	case "stream_event":
		delta := message.Event.Delta
		if message.ParentToolUseID == "" && message.Event.Type == "content_block_delta" && delta.Type == "text_delta" && delta.Text != "" {
			a.publish(agent.Update{Type: "text_delta", Text: delta.Text})
		}
	case "result":
		result := agent.Result{Text: message.Result, IsError: message.IsError || message.Subtype != "success"}
		if result.IsError && result.Text == "" {
			result.Text = strings.Join(message.Errors, "\n")
			if result.Text == "" {
				result.Text = message.Subtype
			}
		}
		// Claude follows an automatic turn with an empty bookkeeping result.
		if result.Text != "" || result.IsError {
			a.publish(agent.Update{Type: "result", Result: &result})
			// Claude has written the transcript by its first result.
			if a.onResumable != nil {
				a.onResumable(a.sessionID)
				a.onResumable = nil
			}
		}
	}
}

// idle reports whether the agent can take a prompt or configuration.
func (a *Agent) idle() bool {
	return a.phase == available && a.claudeState == "idle"
}

func (a *Agent) snapshot() agent.Snapshot {
	snapshot := agent.Snapshot{State: a.claudeState, Approvals: []agent.Approval{}}
	if a.prompt != nil {
		snapshot.Prompt = &agent.PromptSnapshot{MessageID: a.prompt.id}
	}
	for id, request := range a.approvals {
		snapshot.Approvals = append(snapshot.Approvals, agent.Approval{RequestID: id, Request: request})
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
		a.endPrompt(agent.Update{Type: "exited", Error: reason})
	case configuring:
		a.endConfiguration(a.config.fail(agent.ErrExited))
	}
	a.withdrawApprovals()
	a.publish(agent.Update{Type: "parked"})
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

// Inspect calls inspect with the agent's current state on the agent's
// goroutine, between updates: every update published before the call is
// reflected in the state, and none after it.
func (a *Agent) Inspect(inspect func(agent.Snapshot)) error {
	reply := make(chan struct{}, 1)
	if !a.post(inspectRequest{inspect, reply}) {
		return agent.ErrExited
	}
	<-reply
	return nil
}

// Send writes a prompt to an idle agent.
func (a *Agent) Send(text string) (string, error) {
	reply := make(chan sendReply, 1)
	if !a.post(sendRequest{text, reply}) {
		return "", agent.ErrExited
	}
	r := <-reply
	return r.id, r.err
}

func (a *Agent) Approve(requestID string, response json.RawMessage) error {
	reply := make(chan error, 1)
	if !a.post(approveRequest{requestID, response, reply}) {
		return agent.ErrExited
	}
	return <-reply
}

func (a *Agent) Configure(config agent.Settings) error {
	reply := make(chan error, 1)
	if !a.post(configureRequest{config, reply}) {
		return agent.ErrExited
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
