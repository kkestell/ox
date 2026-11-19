package claude

import (
	"encoding/json"
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
	proc   *Process
	input  *Input
	events chan agent.Event
	done   chan struct{} // closed on exit, so late timers deliver nothing

	phase       phase
	claudeState string
	prompt      *submission
	approvals   map[string]json.RawMessage // pending, by Claude's request ID
	config      *configuration
	settings    agent.Settings // the settings the process is using
	onUpdate    func(agent.Update)
	onResumable func(string) // set until the new conversation's first result
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
		sessionID = NewUUID()
	}
	events := make(chan agent.Event)
	proc, err := startProcess(options.Workspace, sessionID, resume, options.Settings, events)
	if err != nil {
		return nil, err
	}
	a := newAgent(proc.Input, options.OnUpdate)
	a.proc = proc
	a.events = events
	a.sessionID = sessionID
	a.settings = options.Settings
	if !resume {
		a.onResumable = options.OnResumable
	}
	return a, nil
}

func newAgent(input *Input, onUpdate func(agent.Update)) *Agent {
	return &Agent{
		input: input, events: make(chan agent.Event), done: make(chan struct{}),
		phase: available, claudeState: "idle", approvals: make(map[string]json.RawMessage),
		onUpdate: onUpdate,
	}
}

func (a *Agent) Events() <-chan agent.Event {
	return a.events
}

func (a *Agent) Handle(event agent.Event) {
	switch e := event.(type) {
	case Message:
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

func (a *Agent) idle() bool {
	return a.phase == available && a.claudeState == "idle"
}

func (a *Agent) Snapshot() agent.Snapshot {
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
	close(a.done)
}

func (a *Agent) Stop() error {
	return a.proc.Terminate()
}

func (a *Agent) Exited() bool {
	return a.phase == exited
}
