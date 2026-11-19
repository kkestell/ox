package claude

import (
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// phase tracks the current operation separately from Claude's reported state.
type phase int

const (
	available   phase = iota
	queued            // prompt written; Claude has not taken it yet
	prompting         // Claude took the prompt; its next idle state releases it
	configuring       // control requests in flight, one at a time
	exited
)

type Agent struct {
	proc   *process
	input  *writer
	events chan agent.Event
	done   chan struct{} // closed on exit, so late timers deliver nothing

	phase phase
	// state is the execution state clients see: Claude's report, except that
	// a prompt is running from the moment it is written.
	state       string
	prompt      *submission
	approvals   []agent.Approval // pending, in arrival order
	config      *configuration
	settings    agent.Settings // the settings the process is using
	onUpdate    func(agent.Update)
	onResumable func(string) // set until the new conversation's first result
	onError     func(error)
	sessionID   string
	background  int  // Claude's live background tasks
	schedules   int  // Claude's pending schedules, from the last Stop hook
	scheduling  bool // a scheduling tool ran since the last Stop hook
}

type processExit struct{ err error }

type controlTimeout struct{ requestID string }

func Start(options agent.Options) (agent.Agent, error) {
	sessionID, resume := options.Resume, options.Resume != ""
	if !resume {
		sessionID = newUUID()
	}
	events := make(chan agent.Event)
	proc, input, err := startProcess(options.Workspace, sessionID, resume, options.Settings, events)
	if err != nil {
		return nil, err
	}
	a := newAgent(input, events, sessionID, options)
	a.proc = proc
	return a, nil
}

func newAgent(input *writer, events chan agent.Event, sessionID string, options agent.Options) *Agent {
	a := &Agent{
		input: input, events: events, done: make(chan struct{}),
		phase: available, state: "idle",
		settings: options.Settings, onUpdate: options.OnUpdate, onError: options.OnError, sessionID: sessionID,
	}
	if options.Resume == "" {
		a.onResumable = options.OnResumable
	}
	return a
}

func (a *Agent) Events() <-chan agent.Event {
	return a.events
}

func (a *Agent) Handle(event agent.Event) {
	switch e := event.(type) {
	case message:
		a.handleOutput(e)
	case processExit:
		a.exit(e.err)
	case controlTimeout:
		a.controlTimedOut(e.requestID)
	}
}

func (a *Agent) after(d time.Duration, event agent.Event) {
	time.AfterFunc(d, func() {
		select {
		case a.events <- event:
		case <-a.done:
		}
	})
}

func (a *Agent) handleOutput(m message) {
	switch m.Type {
	case "control_response":
		a.controlResponse(m.Response)
	case "control_request":
		a.controlRequest(m.RequestID, m.Request)
	case "control_cancel_request":
		a.cancelApproval(m.RequestID)
	case "command_lifecycle":
		if m.State == "started" {
			a.commandStarted(m.CommandUUID)
		}
	case "system":
		switch m.Subtype {
		case "session_state_changed":
			switch m.State {
			case "running", "requires_action", "idle":
				a.sessionState(m.State)
			}
		case "background_tasks_changed":
			a.background = len(m.Tasks)
		}
	case "assistant":
		for _, use := range m.toolUses() {
			switch use.Name {
			case "CronCreate", "CronDelete", "ScheduleWakeup":
				a.scheduling = true
			}
			if m.ParentToolUseID == "" {
				a.publish(agent.Update{Type: "tool_use", ToolUseID: use.ID, ToolName: use.Name, Input: use.Input})
			}
		}
	case "user":
		if m.ParentToolUseID == "" {
			for _, r := range m.toolResults() {
				a.publish(agent.Update{Type: "tool_result", ToolUseID: r.ToolUseID, Result: &agent.Result{Text: r.Text, IsError: r.IsError}})
			}
		}
	case "stream_event":
		delta := m.Event.Delta
		if m.ParentToolUseID == "" && m.Event.Type == "content_block_delta" && delta.Type == "text_delta" && delta.Text != "" {
			a.publish(agent.Update{Type: "text_delta", Text: delta.Text})
		}
	case "result":
		result := agent.Result{Text: m.Result, IsError: m.IsError || m.Subtype != "success"}
		if strings.HasPrefix(m.TerminalReason, "aborted") {
			// errors[] holds only internal diagnostics for an interrupt.
			result.Text = "Interrupted"
		} else if result.IsError && result.Text == "" {
			result.Text = strings.Join(m.Errors, "\n")
			if result.Text == "" {
				result.Text = m.Subtype
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

func (a *Agent) idle() bool {
	return a.phase == available && a.state == "idle"
}

func (a *Agent) Snapshot() agent.Snapshot {
	snapshot := agent.Snapshot{State: a.state, Approvals: append([]agent.Approval{}, a.approvals...)}
	if a.prompt != nil {
		snapshot.Prompt = &agent.PromptSnapshot{MessageID: a.prompt.id}
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
	close(a.done)
}

func (a *Agent) Stop() error {
	return a.proc.Terminate()
}

func (a *Agent) Exited() bool {
	return a.phase == exited
}
