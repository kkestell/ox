package fake

import (
	"crypto/rand"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

type Agent struct {
	events       chan agent.Event
	done         chan struct{} // closed on exit, so late ticks deliver nothing
	onUpdate     func(agent.Update)
	onResumable  func(string)
	onConfigured func(agent.Settings)

	state    string
	settings agent.Settings
	prompt   string        // the ID of the input whose turn is running
	queue    []agent.Input // accepted and not taken, oldest first
	reply    string
	words    []string
	tool     string          // the tool call the prompt asked to approve
	approval *agent.Approval // pending until answered, interrupted, or stopped
	config   *configured     // the configuration awaiting its tick
	tasks    int             // background tasks running
	exited   bool
}

// step is the delay between a turn's steps.
const step = 50 * time.Millisecond

// tick streams the next word of prompt's reply.
type tick struct{ prompt string }

// taskEnded ends a background task.
type taskEnded struct{}

// configured completes a configuration a tick after it starts, as Claude
// acknowledges one after a delay.
type configured struct {
	settings agent.Settings
	done     func(error)
}

func Start(options agent.Options) (agent.Agent, error) {
	a := &Agent{
		events: make(chan agent.Event), done: make(chan struct{}),
		onUpdate: options.OnUpdate, onConfigured: options.OnConfigured, state: agent.StateIdle, settings: options.Settings,
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

// after delivers event through Events d from now.
func (a *Agent) after(d time.Duration, event agent.Event) {
	time.AfterFunc(d, func() {
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
		if a.settings != e.settings {
			a.settings = e.settings
			a.onConfigured(a.settings)
		}
		e.done(nil)
		if len(a.queue) > 0 {
			a.begin()
		}
	case taskEnded:
		was := a.published()
		a.tasks--
		a.publishState(was)
	case tick:
		if e.prompt != a.prompt {
			return // its prompt was interrupted
		}
		if len(a.queue) > 0 {
			a.take()
			return
		}
		if len(a.words) > 0 {
			a.publish(agent.Update{Type: agent.UpdateTextDelta, Text: a.words[0]})
			a.words = a.words[1:]
			a.after(step, tick{a.prompt})
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
	was := a.published()
	a.state = agent.StateIdle
	a.publishState(was)
	a.prompt = ""
}

// published is the state clients see: background while idle with a task
// running, otherwise state.
func (a *Agent) published() string {
	if a.state == agent.StateIdle && a.tasks > 0 {
		return agent.StateBackground
	}
	return a.state
}

// publishState publishes the state clients see when it differs from was.
func (a *Agent) publishState(was string) {
	if now := a.published(); now != was {
		a.publish(agent.Update{Type: agent.UpdateState, State: now})
	}
}

func (a *Agent) Snapshot() agent.Snapshot {
	snapshot := agent.Snapshot{State: a.published(), Approvals: []agent.Approval{}, Queued: append([]agent.Input{}, a.queue...)}
	if a.approval != nil {
		snapshot.Approvals = append(snapshot.Approvals, *a.approval)
	}
	return snapshot
}

func (a *Agent) idle() bool {
	return a.state == agent.StateIdle && a.config == nil
}

// Send queues the input, and starts a turn with it when the agent is idle.
// The reply echoes the input's Content, which the model would receive. An
// input "background <milliseconds>" also starts a background task that runs
// that long.
func (a *Agent) Send(input agent.Input) error {
	a.queue = append(a.queue, input)
	a.publish(input.Update(agent.UpdatePromptQueued))
	if a.idle() {
		a.begin()
	}
	return nil
}

// begin starts a turn with the first queued input.
func (a *Agent) begin() {
	input := a.queue[0]
	a.queue = a.queue[1:]
	a.prompt = input.MessageID
	a.publish(input.Update(agent.UpdatePrompt))
	a.state = agent.StateRunning
	a.publish(agent.Update{Type: agent.UpdateState, State: a.state})
	a.reply = "You said: " + input.Content
	a.words = strings.SplitAfter(a.reply, " ")
	if d, ok := backgroundRequest(input.Content); ok {
		a.tasks++
		a.after(d, taskEnded{})
	}
	if tool, toolInput, ok := approvalRequest(input.Content); ok {
		a.ask(tool, toolInput)
	} else {
		a.after(step, tick{a.prompt})
	}
}

// take makes the first queued input the turn's, whose reply replaces the rest
// of the earlier one, as Claude takes a prompt after a tool call.
func (a *Agent) take() {
	taken := a.queue[0]
	a.queue = a.queue[1:]
	a.prompt = taken.MessageID
	a.publish(taken.Update(agent.UpdatePrompt))
	a.reply = "You said: " + taken.Content
	a.words = strings.SplitAfter(a.reply, " ")
	a.after(step, tick{a.prompt})
}

// drop cancels every queued input, oldest first.
func (a *Agent) drop() {
	dropped := a.queue
	a.queue = nil
	for _, input := range dropped {
		a.publish(input.Update(agent.UpdatePromptCancelled))
	}
}

// backgroundRequest reads a prompt of the form "background <milliseconds>".
func backgroundRequest(text string) (time.Duration, bool) {
	rest, ok := strings.CutPrefix(text, "background ")
	if !ok {
		return 0, false
	}
	ms, err := strconv.Atoi(rest)
	return time.Duration(ms) * time.Millisecond, err == nil
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
	a.approval = &agent.Approval{RequestID: rand.Text(), Request: request, ToolName: tool}
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
	a.after(step, tick{a.prompt})
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
	a.withdraw()
	a.drop()
	if a.state != agent.StateIdle {
		a.finish(agent.Result{Text: "Interrupted", IsError: true})
	}
	return nil
}

func (a *Agent) Configure(patch agent.SettingsPatch, done func(error)) {
	if !a.idle() {
		done(agent.ErrBusy)
		return
	}
	a.config = &configured{patch.Apply(a.settings), done}
	a.after(step, a.config)
}

func (a *Agent) Parkable() bool {
	return a.idle() && a.tasks == 0
}

func (a *Agent) Stop() {
	a.withdraw()
	a.drop()
	if a.prompt != "" {
		a.publish(agent.Update{Type: agent.UpdateExited, Error: "stopped"})
		a.prompt = ""
	}
	if a.config != nil {
		a.config.done(agent.ErrExited)
		a.config = nil
	}
	a.exited = true
	close(a.done)
}

func (a *Agent) Exited() bool {
	return a.exited
}
