package fake

import (
	"crypto/rand"
	"encoding/json"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

type Agent struct {
	events      chan agent.Event
	done        chan struct{} // closed on exit, so late ticks deliver nothing
	onUpdate    func(agent.Update)
	onResumable func(string)

	state    string
	settings agent.Settings
	prompt   string  // the ID of the input whose turn is running
	queued   *queued // sent while running; taken at the next tick
	reply    string
	words    []string
	tool     string          // the tool call the prompt asked to approve
	approval *agent.Approval // pending until answered, interrupted, or stopped
	config   *configured     // the configuration awaiting its tick
	exited   bool
}

// queued is an input written while a turn runs.
type queued struct {
	id string
	agent.Input
}

func (q *queued) prompt() agent.Prompt {
	return agent.Prompt{MessageID: q.id, Text: q.Text, From: q.From}
}

// tick streams the next word of prompt's reply.
type tick struct{ prompt string }

// configured completes a configuration a tick after it starts, as Claude
// acknowledges one after a delay.
type configured struct {
	settings agent.Settings
	done     func(agent.Settings, error)
}

func Start(options agent.Options) (agent.Agent, error) {
	a := &Agent{
		events: make(chan agent.Event), done: make(chan struct{}),
		onUpdate: options.OnUpdate, state: agent.StateIdle, settings: options.Settings,
	}
	if options.Resume == "" {
		a.onResumable = options.OnResumable
	}
	return a, nil
}

func (a *Agent) Events() <-chan agent.Event {
	return a.events
}

func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	if update.MessageID == "" {
		update.MessageID = a.prompt
	}
	a.onUpdate(update)
}

// after delivers event through Events a tick from now.
func (a *Agent) after(event agent.Event) {
	time.AfterFunc(50*time.Millisecond, func() {
		select {
		case a.events <- event:
		case <-a.done:
		}
	})
}

func (a *Agent) Handle(event agent.Event) {
	switch e := event.(type) {
	case *configured:
		a.config = nil
		a.settings = e.settings
		e.done(a.settings, nil)
	case tick:
		if e.prompt != a.prompt {
			return // its prompt was interrupted
		}
		if a.queued != nil {
			a.take()
			return
		}
		if len(a.words) > 0 {
			a.publish(agent.Update{Type: agent.UpdateTextDelta, Text: a.words[0]})
			a.words = a.words[1:]
			a.after(tick{a.prompt})
			return
		}
		a.finish(agent.Result{Text: a.reply})
	}
}

func (a *Agent) finish(result agent.Result) {
	a.words = nil
	a.publish(agent.Update{Type: agent.UpdateResult, Result: &result})
	if a.onResumable != nil {
		a.onResumable(rand.Text())
		a.onResumable = nil
	}
	a.state = agent.StateIdle
	a.publish(agent.Update{Type: agent.UpdateState, State: a.state})
	a.prompt = ""
}

func (a *Agent) Snapshot() agent.Snapshot {
	snapshot := agent.Snapshot{State: a.state, Approvals: []agent.Approval{}}
	if a.approval != nil {
		snapshot.Approvals = append(snapshot.Approvals, *a.approval)
	}
	if a.queued != nil {
		queued := a.queued.prompt()
		snapshot.Queued = &queued
	}
	return snapshot
}

func (a *Agent) idle() bool {
	return a.state == agent.StateIdle && a.config == nil
}

// Send starts a turn with the input, or queues it while a turn runs. The
// reply echoes the input's Content, which the model would receive.
func (a *Agent) Send(input agent.Input) (string, error) {
	if a.config != nil || a.queued != nil {
		return "", agent.ErrBusy
	}
	id := rand.Text()
	if a.state != agent.StateIdle {
		a.queued = &queued{id, input}
		a.publish(agent.Update{MessageID: id, Type: agent.UpdatePromptQueued, Text: input.Text, From: input.From})
		return id, nil
	}
	a.prompt = id
	a.publish(agent.Update{Type: agent.UpdatePrompt, Text: input.Text, From: input.From})
	a.state = agent.StateRunning
	a.publish(agent.Update{Type: agent.UpdateState, State: a.state})
	a.reply = "You said: " + input.Content
	a.words = strings.SplitAfter(a.reply, " ")
	if tool, toolInput, ok := approvalRequest(input.Content); ok {
		a.ask(tool, toolInput)
	} else {
		a.after(tick{a.prompt})
	}
	return a.prompt, nil
}

// take makes the queued input the turn's, whose reply replaces the rest of
// the earlier one, as Claude takes a prompt after a tool call.
func (a *Agent) take() {
	taken := a.queued
	a.queued = nil
	a.prompt = taken.id
	a.publish(agent.Update{Type: agent.UpdatePrompt, Text: taken.Text, From: taken.From})
	a.reply = "You said: " + taken.Content
	a.words = strings.SplitAfter(a.reply, " ")
	a.after(tick{a.prompt})
}

// drop cancels the queued input, if any.
func (a *Agent) drop() {
	if a.queued != nil {
		a.publish(agent.Update{MessageID: a.queued.id, Type: agent.UpdatePromptCancelled, Text: a.queued.Text, From: a.queued.From})
		a.queued = nil
	}
}

// approvalRequest reads a prompt of the form "approve <tool> <input JSON>",
// which asks for approval of that tool call before the reply.
func approvalRequest(text string) (string, json.RawMessage, bool) {
	rest, ok := strings.CutPrefix(text, "approve ")
	if !ok {
		return "", nil, false
	}
	tool, input, ok := strings.Cut(rest, " ")
	if !ok || !json.Valid([]byte(input)) {
		return "", nil, false
	}
	return tool, json.RawMessage(input), true
}

// ask calls tool with input and waits for its approval, as Claude does in
// manual mode.
func (a *Agent) ask(tool string, input json.RawMessage) {
	a.tool = rand.Text()
	a.publish(agent.Update{Type: agent.UpdateToolUse, ToolUseID: a.tool, ToolName: tool, Input: input})
	request, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": tool, "input": input, "tool_use_id": a.tool})
	a.approval = &agent.Approval{RequestID: rand.Text(), Request: request}
	a.publish(agent.Update{Type: agent.UpdateApproval, RequestID: a.approval.RequestID, Request: request})
	a.state = agent.StateRequiresAction
	a.publish(agent.Update{Type: agent.UpdateState, State: a.state})
}

// Approve answers the pending approval. The tool call's result is the
// response, so it shows what reached the harness.
func (a *Agent) Approve(requestID string, response json.RawMessage) error {
	if a.approval == nil || a.approval.RequestID != requestID {
		return agent.ErrApprovalNotFound
	}
	a.approval = nil
	a.publish(agent.Update{Type: agent.UpdateApprovalAnswered, RequestID: requestID})
	a.publish(agent.Update{Type: agent.UpdateToolResult, ToolUseID: a.tool, Result: &agent.Result{Text: string(response)}})
	a.state = agent.StateRunning
	a.publish(agent.Update{Type: agent.UpdateState, State: a.state})
	a.after(tick{a.prompt})
	return nil
}

// withdraw cancels the pending approval, if any, which ends its tool call.
func (a *Agent) withdraw() {
	if a.approval != nil {
		a.publish(agent.Update{Type: agent.UpdateApprovalCancelled, RequestID: a.approval.RequestID})
		a.publish(agent.Update{Type: agent.UpdateToolResult, ToolUseID: a.tool, Result: &agent.Result{Text: "Cancelled", IsError: true}})
		a.approval = nil
	}
}

func (a *Agent) Interrupt() error {
	if a.state != agent.StateIdle {
		a.withdraw()
		a.drop()
		a.finish(agent.Result{Text: "Interrupted", IsError: true})
	}
	return nil
}

func (a *Agent) Configure(settings agent.Settings, done func(agent.Settings, error)) {
	if !a.idle() {
		done(a.settings, agent.ErrBusy)
		return
	}
	a.config = &configured{settings, done}
	a.after(a.config)
}

func (a *Agent) Parkable() bool {
	return a.idle()
}

func (a *Agent) Stop() error {
	a.withdraw()
	a.drop()
	if a.prompt != "" {
		a.publish(agent.Update{Type: agent.UpdateExited, Error: "stopped"})
		a.prompt = ""
	}
	if a.config != nil {
		a.config.done(a.settings, agent.ErrExited)
		a.config = nil
	}
	a.exited = true
	close(a.done)
	return nil
}

func (a *Agent) Exited() bool {
	return a.exited
}
