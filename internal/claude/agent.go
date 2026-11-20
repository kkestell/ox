package claude

import (
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// phase is what Ox is doing with the process, tracked separately from
// Claude's reported state. Each phase's fields exist only while the agent is
// in it.
type phase interface{ isPhase() }

type available struct{}
type exited struct{}

// prompting holds the agent from the first client input written until Claude
// reports idle with none queued.
type prompting struct {
	input  agent.Input // the latest input written, which the releasing update names
	queued bool        // Claude has not taken or dropped input yet
	turn   string      // the client input whose turn is running, until its result
}

// configuring holds the agent while control requests are in flight, one at a
// time: the first of requests, under requestID.
type configuring struct {
	requests  []control
	requestID string
	done      func(error)
}

// timedOut holds the agent after a control request went unanswered and Ox
// asked the process to exit. The configuration fails with err once it has.
type timedOut struct {
	done func(error)
	err  error
}

func (available) isPhase()    {}
func (exited) isPhase()       {}
func (*prompting) isPhase()   {}
func (*configuring) isPhase() {}
func (timedOut) isPhase()     {}

type Agent struct {
	proc   *process
	input  *writer
	events chan agent.Event
	done   chan struct{} // closed on exit, so late timers deliver nothing

	phase phase
	// state is the execution state clients see: Claude's report, except that
	// a prompt is running from the moment it is written.
	state            string
	approvals        []agent.Approval // pending, in arrival order
	settings         agent.Settings   // the settings the process is using
	onUpdate         func(agent.Update)
	onResumable      func(string)
	onConfigured     func(agent.Settings)
	onError          func(error)
	conversationID   string // most recently reported on stdout
	resumable        string // token last passed to OnResumable, or supplied at startup
	streamedText     bool   // the current turn published a text delta
	stopping         bool   // Ox asked the process to exit
	background       int    // Claude's live background tasks
	mayHaveSchedules bool   // true until Stop reports an empty schedule list
}

type processExit struct {
	err    error
	stderr string
}

type controlTimeout struct{ requestID string }

func Start(options agent.Options) (agent.Agent, error) {
	sessionID, resume := options.Resume, options.Resume != ""
	if !resume {
		sessionID = newUUID()
	}
	events := make(chan agent.Event)
	proc, input, err := startProcess(options.Workspace, options.SessionID, sessionID, resume, options.Settings, options.MCP, events)
	if err != nil {
		return nil, err
	}
	return newAgent(input, events, proc, options), nil
}

// newAgent runs the process proc, whose output arrives on events and whose
// input is input.
func newAgent(input *writer, events chan agent.Event, proc *process, options agent.Options) *Agent {
	return &Agent{
		proc: proc, input: input, events: events, done: make(chan struct{}),
		phase: available{}, state: agent.StateIdle,
		settings: options.Settings, onUpdate: options.OnUpdate, onConfigured: options.OnConfigured, onError: options.OnError,
		onResumable: options.OnResumable, resumable: options.Resume, mayHaveSchedules: options.Resume != "",
	}
}

func (a *Agent) Events() <-chan agent.Event {
	return a.events
}

func (a *Agent) Handle(event agent.Event) {
	switch e := event.(type) {
	case message:
		a.handleOutput(e)
	case processExit:
		a.exit(e)
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
	if m.SessionID != "" {
		a.conversationID = m.SessionID
	}
	switch m.Type {
	case "stream_event", "assistant", "user", "result":
		// The command lifecycle and system/init can precede a resumable
		// transcript. Model output follows the saved prompt, and even an
		// empty result completes a local command like /clear.
		if a.conversationID != "" && a.conversationID != a.resumable {
			a.resumable = a.conversationID
			if a.onResumable != nil {
				a.onResumable(a.resumable)
			}
		}
	}
	switch m.Type {
	case "control_response":
		a.controlResponse(m.Response)
	case "control_request":
		a.controlRequest(m.RequestID, m.Request)
	case "control_cancel_request":
		a.cancelApproval(m.RequestID)
	case "command_lifecycle":
		switch m.State {
		case "started":
			a.commandStarted(m.CommandUUID)
		case "cancelled":
			a.commandCancelled(m.CommandUUID)
		}
	case "system":
		if m.PermissionMode != "" {
			a.permissionMode(m.PermissionMode)
		}
		switch m.Subtype {
		case "session_state_changed":
			switch m.State {
			case agent.StateRunning, agent.StateRequiresAction, agent.StateIdle:
				a.sessionState(m.State)
			}
		case "background_tasks_changed":
			a.background = len(m.Tasks)
		}
	case "assistant":
		for _, use := range m.toolUses() {
			switch use.Name {
			case "CronCreate", "CronDelete", "ScheduleWakeup":
				a.mayHaveSchedules = true
			}
			if m.ParentToolUseID == "" {
				a.publish(agent.Update{Type: agent.UpdateToolUse, ToolUseID: use.ID, ToolName: use.Name, Input: use.Input})
			}
		}
	case "user":
		if m.ParentToolUseID == "" {
			for _, r := range m.toolResults() {
				a.publish(agent.Update{Type: agent.UpdateToolResult, ToolUseID: r.ToolUseID, Result: &agent.Result{Text: r.Text, IsError: r.IsError}})
			}
		}
	case "stream_event":
		delta := m.Event.Delta
		if m.ParentToolUseID == "" && m.Event.Type == "content_block_delta" && delta.Type == "text_delta" && delta.Text != "" {
			a.streamedText = true
			a.publish(agent.Update{Type: agent.UpdateTextDelta, Text: delta.Text})
		}
	case "result":
		result := agent.Result{Text: m.Result, IsError: m.failed()}
		if strings.HasPrefix(m.TerminalReason, "aborted") {
			// errors[] holds only internal diagnostics for an interrupt.
			result.Text = "Interrupted"
		} else if result.IsError {
			result.Text = m.errorText()
		}
		if !result.IsError && !a.streamedText && result.Text != "" {
			a.publish(agent.Update{Type: agent.UpdateTextDelta, Text: result.Text})
		}
		a.streamedText = false
		// Claude follows an automatic turn with an empty bookkeeping result.
		if result.Text != "" || result.IsError {
			a.publish(agent.Update{Type: agent.UpdateResult, Result: &result})
		}
		// A later turn before idle, such as one reporting a background
		// subagent's findings, is Claude's own.
		if p, ok := a.phase.(*prompting); ok {
			p.turn = ""
		}
	}
}

// permissionMode adopts the mode Claude reports, which it changes itself when
// a plan is approved or when it enters plan mode.
func (a *Agent) permissionMode(reported string) {
	// Claude reports manual as default.
	if reported == "default" {
		reported = "manual"
	}
	settings := a.settings
	settings.PermissionMode = reported
	a.configured(settings)
}

func (a *Agent) configured(settings agent.Settings) {
	if settings != a.settings {
		a.settings = settings
		if a.onConfigured != nil {
			a.onConfigured(settings)
		}
	}
}

func (a *Agent) idle() bool {
	_, available := a.phase.(available)
	return available && a.state == agent.StateIdle
}

func (a *Agent) Snapshot() agent.Snapshot {
	return agent.Snapshot{State: a.state, Approvals: append([]agent.Approval{}, a.approvals...), Queued: a.queued()}
}

func (a *Agent) exit(exit processExit) {
	reason := "Claude exited"
	if exit.err != nil {
		reason += ": " + exit.err.Error()
	}
	if exit.stderr != "" {
		reason += "\n" + exit.stderr
	}
	reported := false
	switch p := a.phase.(type) {
	case *prompting:
		if p.queued {
			a.dropQueued(p)
		}
		a.endPrompt(p, agent.Update{Type: agent.UpdateExited, Error: reason})
		reported = true
	case *configuring:
		a.endConfiguration(p.done, p.fail(agent.ErrExited))
	case timedOut:
		a.endConfiguration(p.done, p.err)
	}
	a.withdrawApprovals()
	// Claude exits on its own only when something has gone wrong, which can
	// cut short a turn it started itself.
	if !reported && !a.stopping {
		a.publish(agent.Update{Type: agent.UpdateExited, Error: reason})
	}
	a.phase = exited{}
	close(a.done)
}

// Stop asks Claude to exit and handles its events until it has.
func (a *Agent) Stop() error {
	if err := a.terminate(); err != nil {
		return err
	}
	for !a.Exited() {
		a.Handle(<-a.events)
	}
	return nil
}

// terminate asks Claude to exit; its exit event completes the shutdown.
func (a *Agent) terminate() error {
	a.stopping = true
	return a.proc.Terminate()
}

func (a *Agent) Exited() bool {
	_, exited := a.phase.(exited)
	return exited
}
