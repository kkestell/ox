package fake

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
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

	tracker  *agent.Tracker // inputs not taken, the reported state, and background tasks
	settings agent.Settings
	prompt   string // the ID of the input whose turn is running
	reply    string
	thoughts []string        // the reasoning still to stream before the reply
	pieces   []string        // the reply still to stream
	tool     string          // the tool call the prompt asked to approve
	approval *agent.Approval // pending until answered, interrupted, or stopped
	config   *configured     // the configuration awaiting its tick
	exited   bool
}

// step is the delay between a turn's steps.
const step = 50 * time.Millisecond

// maxPieces is the most steps a reply or its reasoning streams in, so a turn's
// length does not grow with its input.
const maxPieces = 4

// tick streams the next piece of prompt's reply.
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
		onUpdate: options.OnUpdate, onConfigured: options.OnConfigured, settings: options.Settings,
	}
	a.tracker = agent.NewTracker(a.publish)
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
		if a.tracker.Len() > 0 {
			a.begin()
		}
	case taskEnded:
		a.tracker.SetTasks(a.tracker.Tasks() - 1)
	case tick:
		if e.prompt != a.prompt {
			return // its prompt was interrupted
		}
		if a.tracker.Len() > 0 {
			a.begin()
			return
		}
		if len(a.thoughts) > 0 {
			a.publish(agent.Update{Type: agent.UpdateReasoningDelta, Text: a.thoughts[0]})
			a.thoughts = a.thoughts[1:]
			a.after(step, tick{a.prompt})
			return
		}
		if len(a.pieces) > 0 {
			a.publish(agent.Update{Type: agent.UpdateTextDelta, Text: a.pieces[0]})
			a.pieces = a.pieces[1:]
			a.after(step, tick{a.prompt})
			return
		}
		a.finish(agent.Result{Text: a.reply})
	}
}

func (a *Agent) finish(result agent.Result) {
	a.thoughts, a.pieces = nil, nil
	a.publish(agent.Update{Type: agent.UpdateResult, Result: &result})
	if a.onResumable != nil {
		a.onResumable(rand.Text())
		a.onResumable = nil
	}
	a.tracker.Report(agent.StateIdle, "")
	a.prompt = ""
}

func (a *Agent) Snapshot() agent.Snapshot {
	snapshot := agent.Snapshot{State: a.tracker.State(), Approvals: []agent.Approval{}, Queued: a.tracker.Queued()}
	if a.approval != nil {
		snapshot.Approvals = append(snapshot.Approvals, *a.approval)
	}
	return snapshot
}

func (a *Agent) idle() bool {
	return a.tracker.Idle() && a.config == nil
}

// Send queues the input, and starts a turn with it when the agent is idle.
// The reply echoes the input's Content, which the model would receive, and
// the count of its images, streamed in at most a few pieces. An input
// "background <milliseconds>" also starts a background task that runs that
// long, an input "wait <milliseconds>" keeps the turn running that long before
// the reply, and an input "think <text>" streams text as reasoning before the
// reply.
func (a *Agent) Send(input agent.Input) error {
	idle := a.idle()
	a.tracker.Accept(input)
	if idle {
		a.begin()
	}
	return nil
}

// begin starts a turn with the first queued input.
func (a *Agent) begin() {
	input := a.tracker.Take()
	a.prompt = input.MessageID
	a.reply = "You said: " + input.Content
	if len(input.Images) > 0 {
		a.reply += fmt.Sprintf(" [images: %d]", len(input.Images))
	}
	a.pieces = split(a.reply)
	if thought, ok := strings.CutPrefix(input.Content, "think "); ok {
		a.thoughts = split(thought)
	}
	if d, ok := durationRequest("background", input.Content); ok {
		a.tracker.SetTasks(a.tracker.Tasks() + 1)
		a.after(d, taskEnded{})
	}
	if tool, toolInput, ok := approvalRequest(input.Content); ok {
		a.ask(tool, toolInput)
		return
	}
	delay := step
	if d, ok := durationRequest("wait", input.Content); ok {
		delay = d
	}
	a.after(delay, tick{a.prompt})
}

// split cuts text into at most maxPieces runs of whole words.
func split(text string) []string {
	words := strings.SplitAfter(text, " ")
	size := (len(words) + maxPieces - 1) / maxPieces
	var runs []string
	for len(words) > 0 {
		n := min(size, len(words))
		runs = append(runs, strings.Join(words[:n], ""))
		words = words[n:]
	}
	return runs
}

// durationRequest reads a prompt of the form "<verb> <milliseconds>".
func durationRequest(verb, text string) (time.Duration, bool) {
	rest, ok := strings.CutPrefix(text, verb+" ")
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
	a.approval = &agent.Approval{RequestID: rand.Text(), Request: request}
	a.publish(agent.Update{Type: agent.UpdateApproval, RequestID: a.approval.RequestID, Request: request})
	a.tracker.Report(agent.StateRequiresAction, "")
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
	a.tracker.Report(agent.StateRunning, "")
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
	a.tracker.DropFrom(0)
	if !a.tracker.Idle() {
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
	return a.idle() && a.tracker.Tasks() == 0
}

func (a *Agent) Stop() {
	a.withdraw()
	a.tracker.DropFrom(0)
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
