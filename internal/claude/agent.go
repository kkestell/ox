package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// phase is what Ox is doing with the process, tracked separately from
// Claude's reported state. Each phase's fields exist only while the agent is
// in it.
//
//sumtype:decl
type phase interface{ isPhase() }

type available struct{}
type exited struct{}

// starting holds the agent from spawn until Claude answers initialize, the
// start fails, or Ox stops the agent.
type starting struct {
	reason         string // Claude's last failure output: a failed result or a plain-text line
	noConversation bool   // reason is Claude's report that the conversation to resume is gone
}

// prompting holds the agent from the first client input written until Claude
// reports idle with nothing untaken. The first untaken input, if any, has
// been written.
type prompting struct {
	messageID string // the latest input written, which the releasing update names
	turn      string // the client input whose turn is running, until its result
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
func (*starting) isPhase()    {}
func (*prompting) isPhase()   {}
func (*configuring) isPhase() {}
func (timedOut) isPhase()     {}

type Agent struct {
	proc   *process
	input  *writer
	events chan agent.Event
	done   chan struct{} // closed when the agent ends, so late timers and output deliver nothing

	phase phase
	// tracker holds every input Claude has not taken, oldest first, and
	// Claude's reported state. Only the first input is ever written, and only
	// in prompting.
	tracker          *agent.Tracker
	approvals        []agent.Approval // pending, in arrival order
	settings         agent.Settings   // the settings the process is using
	onUpdate         func(agent.Update)
	onResumable      func(string)
	onConfigured     func(agent.Settings)
	onError          func(error)
	conversationID   string // most recently reported on stdout
	resumable        string // token last passed to OnResumable, or supplied at startup
	streamedText     bool   // the current turn published a text delta
	mayHaveSchedules bool   // true until Stop reports an empty schedule list
	plainText        string // Claude's last plain-text line on stdout after startup
}

type processExit struct {
	err    error
	stderr string
}

type controlTimeout struct{ requestID string }

// startupTimeout fires when Claude has had startupLimit to answer initialize.
type startupTimeout struct{}

// initializeRequest is Claude's handshake. It declares Stop to receive
// pending schedules at the end of each completed turn.
const initializeRequest = `{"subtype":"initialize","hooks":{"Stop":[{"hookCallbackIds":["stop"]}]}}`

// lostConversation tells the user that the next prompt starts a new
// conversation. The prompt that found the conversation gone is dropped, so
// the user decides whether to send it without the earlier context.
const lostConversation = "Claude could not resume the earlier conversation; the next prompt starts a new one without its context"

// Start launches Claude and returns the agent starting, before Claude has
// answered initialize.
func Start(options agent.Options) (agent.Agent, error) {
	sessionID, resume := options.Resume, options.Resume != ""
	if !resume {
		sessionID = newUUID()
	}
	events, done := make(chan agent.Event), make(chan struct{})
	proc, input, err := startProcess(options.Workspace, options.SessionID, sessionID, resume, options.Settings, options.MCP, events, done)
	if err != nil {
		return nil, err
	}
	a := newAgent(input, events, done, proc, options)
	// A failed write means Claude has exited or is exiting; its exit ends
	// the start.
	if err := input.Control("initialize", json.RawMessage(initializeRequest)); err != nil {
		a.onError(fmt.Errorf("initialize Claude: %w", err))
	}
	a.after(startupLimit, startupTimeout{})
	return a, nil
}

// newAgent runs the process proc, starting, whose output arrives on events
// until done is closed and whose input is input.
func newAgent(input *writer, events chan agent.Event, done chan struct{}, proc *process, options agent.Options) *Agent {
	a := &Agent{
		proc: proc, input: input, events: events, done: done,
		phase:    &starting{},
		settings: options.Settings, onUpdate: options.OnUpdate, onConfigured: options.OnConfigured, onError: options.OnError,
		onResumable: options.OnResumable, resumable: options.Resume, mayHaveSchedules: options.Resume != "",
	}
	a.tracker = agent.NewTracker(a.publish)
	return a
}

func (a *Agent) Events() <-chan agent.Event {
	return a.events
}

func (a *Agent) Handle(event agent.Event) {
	switch e := event.(type) {
	case message:
		a.handleOutput(e)
	case plainText:
		if s, ok := a.phase.(*starting); ok {
			s.reason, s.noConversation = string(e), false
		} else {
			a.plainText = string(e)
		}
	case processExit:
		a.exit(e)
	case controlTimeout:
		a.controlTimedOut(e.requestID)
	case startupTimeout:
		if s, ok := a.phase.(*starting); ok {
			reason := "no startup response within one minute"
			if s.reason != "" {
				reason += "\n" + s.reason
			}
			a.giveUp(s, reason)
		}
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
	if s, ok := a.phase.(*starting); ok && a.startupOutput(s, m) {
		return
	}
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
			a.tracker.SetTasks(len(m.Tasks))
		}
	case "assistant":
		for _, use := range m.toolUses() {
			switch use.Name {
			case "CronCreate", "ScheduleWakeup":
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
		if m.ParentToolUseID != "" || m.Event.Type != "content_block_delta" {
			break
		}
		switch {
		case delta.Type == "text_delta" && delta.Text != "":
			a.streamedText = true
			a.publish(agent.Update{Type: agent.UpdateTextDelta, Text: delta.Text})
		case delta.Type == "thinking_delta" && delta.Thinking != "":
			a.publish(agent.Update{Type: agent.UpdateReasoningDelta, Text: delta.Thinking})
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
	return available && a.tracker.Idle()
}

func (a *Agent) Snapshot() agent.Snapshot {
	return agent.Snapshot{State: a.tracker.State(), Approvals: append([]agent.Approval{}, a.approvals...), Queued: a.tracker.Queued()}
}

// startupOutput handles the output that ends or explains a start, and
// reports whether it did. Claude can fail at startup with an error result or
// plain text and exit, sometimes before it reads initialize, and can report
// a missing conversation before answering initialize.
func (a *Agent) startupOutput(s *starting, m message) bool {
	switch {
	case m.Type == "result" && m.failed():
		s.reason = m.errorText()
		s.noConversation = m.Subtype == "error_during_execution" && len(m.Errors) > 0 &&
			strings.HasPrefix(m.Errors[0], "No conversation found with session ID")
	case m.Type == "control_response" && m.Response.RequestID == "initialize":
		switch {
		case m.Response.Subtype != "success":
			a.giveUp(s, fmt.Sprintf("Claude refused initialization (%s): %s", m.Response.Subtype, m.Response.Error))
		case s.noConversation:
			a.giveUp(s, s.reason)
		default:
			a.ready()
		}
	default:
		return false
	}
	return true
}

// giveUp asks the process to exit and ends a start that failed with reason.
func (a *Agent) giveUp(s *starting, reason string) {
	a.proc.Terminate()
	a.startFailed(s, reason)
}

// startFailed ends the start s with reason, or with the lost conversation
// notice when Claude reported that the conversation to resume is gone.
func (a *Agent) startFailed(s *starting, reason string) {
	if s.noConversation {
		a.onError(fmt.Errorf("resume conversation: %s", s.reason))
		if a.onResumable != nil {
			a.onResumable("")
		}
		reason = lostConversation
	}
	a.end(reason)
}

func (a *Agent) exit(exit processExit) {
	switch p := a.phase.(type) {
	case *starting:
		reason := p.reason
		if reason == "" {
			reason = exit.stderr
		}
		if reason == "" {
			reason = exitReason(exit)
		}
		a.startFailed(p, reason)
	case timedOut:
		// Ox asked the process to exit.
		a.end("")
	default:
		// Claude exits on its own only when something has gone wrong, which
		// can cut short a turn it started itself.
		reason := exitReason(exit)
		if a.plainText != "" {
			reason += "\n" + a.plainText
		}
		if exit.stderr != "" {
			reason += "\n" + exit.stderr
		}
		a.end(reason)
	}
}

func exitReason(exit processExit) string {
	if exit.err != nil {
		return "Claude exited: " + exit.err.Error()
	}
	return "Claude exited"
}

// Stop asks Claude to exit and ends the agent at once. The process exits,
// or is killed, in the background.
func (a *Agent) Stop() {
	a.proc.Terminate()
	reason := ""
	if _, prompting := a.phase.(*prompting); prompting {
		reason = "Claude was stopped"
	}
	a.end(reason)
}

// end ends the agent, for its process's exit, Stop, or a start Ox gave up
// on. It drops every input Claude has not taken, ends a configuration, and
// withdraws approvals. A nonempty reason is published as exited, naming the
// latest input written of an active prompt, or the latest input of a failed
// start.
func (a *Agent) end(reason string) {
	final := agent.Update{Type: agent.UpdateExited, Error: reason}
	switch p := a.phase.(type) {
	case *starting:
		if queued := a.tracker.Queued(); len(queued) > 0 {
			final.MessageID = queued[len(queued)-1].MessageID
		}
	case *prompting:
		final.MessageID = p.messageID
	case available, *configuring, timedOut, exited:
		// No input names the end.
	}
	a.tracker.DropFrom(0)
	// Ending writes nothing, so a configuration ends here rather than
	// through endConfiguration.
	switch p := a.phase.(type) {
	case *configuring:
		p.done(p.fail(agent.ErrExited))
	case timedOut:
		p.done(p.err)
	case available, *starting, *prompting, exited:
		// No configuration waits.
	}
	a.withdrawApprovals()
	if reason != "" {
		a.publish(final)
	}
	a.phase = exited{}
	close(a.done)
}

func (a *Agent) Exited() bool {
	_, exited := a.phase.(exited)
	return exited
}
