// Package ur is Ox's built-in harness, which chats through OpenRouter's chat
// completions API and runs the model's shell commands. Each turn is a streamed
// request carrying the conversation so far, which Ur keeps in a file of its
// own, then a request after each response's commands have run.
package ur

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// Endpoint is OpenRouter's chat completions API.
const Endpoint = "https://openrouter.ai/api/v1/chat/completions"

// efforts are the reasoning efforts OpenRouter takes.
var efforts = []string{"max", "xhigh", "high", "medium", "low", "minimal", "none"}

// checkEffort reports whether OpenRouter takes effort; an empty effort sends
// none.
func checkEffort(effort string) error {
	if effort != "" && !slices.Contains(efforts, effort) {
		return fmt.Errorf("Ur has no effort level %q", effort)
	}
	return nil
}

// checkPermissionMode reports whether Ur takes mode: manual, where every shell
// call asks first, or auto, where every call runs without asking.
func checkPermissionMode(mode string) error {
	if mode != "manual" && mode != "auto" {
		return fmt.Errorf("Ur has no permission mode %q", mode)
	}
	return nil
}

// errLost refuses a start whose saved conversation cannot be read.
var errLost = errors.New("Ur could not resume the earlier conversation; the next prompt starts a new one without its context")

// Harness starts agents that post to endpoint and keep their conversations in
// dir. providers maps a model ID to the provider order its requests pin; a
// model without one routes freely.
func Harness(endpoint, dir string, providers map[string][]string) agent.Harness {
	return agent.Harness{
		Start: func(options agent.Options) (agent.Agent, error) {
			key := os.Getenv("OPENROUTER_API_KEY")
			if key == "" {
				return nil, errors.New("OPENROUTER_API_KEY is not set")
			}
			a := &Agent{
				endpoint: endpoint, key: key, providers: providers, dir: dir, id: options.Resume, workspace: options.Workspace,
				events: make(chan agent.Event), done: make(chan struct{}),
				onUpdate: options.OnUpdate, onResumable: options.OnResumable,
				onConfigured: options.OnConfigured, onError: options.OnError, settings: options.Settings,
			}
			if a.id == "" {
				a.id = rand.Text()
			} else if err := a.load(); err != nil {
				options.OnError(fmt.Errorf("resume conversation: %w", err))
				options.OnResumable("")
				return nil, errLost
			}
			a.tracker = agent.NewTracker(a.publish)
			return a, nil
		},
		CheckEffort:         checkEffort,
		CheckPermissionMode: checkPermissionMode,
		TakesProviders:      true,
	}
}

// Agent holds one conversation, which it appends to the file <dir>/<id>.jsonl
// and announces by its ID as the resume token. Every field is owned by the
// session goroutine, except those set at start, which the request goroutine
// reads.
type Agent struct {
	endpoint     string
	key          string
	providers    map[string][]string
	dir          string
	id           string
	workspace    string
	events       chan agent.Event
	done         chan struct{} // closed on exit, so a request delivers nothing after it
	onUpdate     func(agent.Update)
	onResumable  func(string)
	onConfigured func(agent.Settings)
	onError      func(error)

	settings agent.Settings
	history  []message      // exactly the file's entries; only save adds to it
	tracker  *agent.Tracker // inputs not taken and the reported state
	turn     *turn          // the running turn; nil while idle
	exited   bool
}

func (a *Agent) path() string {
	return filepath.Join(a.dir, a.id+".jsonl")
}

// load reads the conversation file into history.
func (a *Agent) load() error {
	f, err := os.Open(a.path())
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	for {
		var m message
		err := decoder.Decode(&m)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		a.history = append(a.history, m)
	}
}

// save appends m to the conversation file, and to history once it is
// written. Saving the first entry makes the conversation resumable.
func (a *Agent) save(m message) error {
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(a.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(m) // strings always marshal
	_, err = f.Write(append(data, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	a.history = append(a.history, m)
	if len(a.history) == 1 {
		a.onResumable(a.id)
	}
	return nil
}

// turn is the running turn. Canceling its context ends its request or command.
type turn struct {
	prompt string // the ID of the input whose turn it is; a later input taken into the turn replaces it
	ctx    context.Context
	cancel context.CancelFunc
	step   step
}

// step is what the turn waits on.
//
//sumtype:decl
type step interface{ isStep() }

// streaming waits on the request.
type streaming struct{}

// asking waits on the approval of calls[0], the first of the response's calls
// not yet answered.
type asking struct {
	calls     []toolCall
	command   shellCommand // calls[0]'s arguments
	requestID string
	request   json.RawMessage
}

// executing waits on calls[0]'s command, the first of the response's calls not
// yet answered.
type executing struct {
	calls []toolCall
}

func (streaming) isStep()  {}
func (*asking) isStep()    {}
func (*executing) isStep() {}

// delta is reply text streamed for a turn.
type delta struct {
	turn *turn
	text string
}

// ended ends a turn's request, with the response's tool calls or the error
// that ended it early.
type ended struct {
	turn  *turn
	calls []toolCall
	err   error
}

// ran ends a step's command.
type ran struct {
	step    *executing
	outcome outcome
}

func (a *Agent) Events() <-chan agent.Event {
	return a.events
}

func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	if update.MessageID == "" && a.turn != nil {
		update.MessageID = a.turn.prompt
	}
	a.onUpdate(update)
}

// deliver passes event to the session goroutine unless the agent has exited.
func (a *Agent) deliver(event agent.Event) {
	select {
	case a.events <- event:
	case <-a.done:
	}
}

// Handle takes a request's output. Output for a turn that is no longer running
// is ignored.
func (a *Agent) Handle(event agent.Event) {
	switch e := event.(type) {
	case delta:
		if e.turn != a.turn {
			return
		}
		if err := a.save(message{Role: "assistant", Content: e.text}); err != nil {
			a.finish(agent.Result{Text: "Ur could not save the reply: " + err.Error(), IsError: true})
			return
		}
		a.publish(agent.Update{Type: agent.UpdateTextDelta, Text: e.text})
	case ended:
		if e.turn != a.turn {
			return
		}
		switch {
		case e.err != nil:
			a.finish(agent.Result{Text: e.err.Error(), IsError: true})
		case len(e.calls) == 0:
			var reply string
			if messages := conversation(a.history); messages[len(messages)-1].Role == "assistant" {
				reply = messages[len(messages)-1].Content
			}
			a.finish(agent.Result{Text: reply})
		default:
			if err := a.save(message{Role: "assistant", ToolCalls: e.calls}); err != nil {
				a.finish(agent.Result{Text: "Ur could not save the reply: " + err.Error(), IsError: true})
				return
			}
			a.next(e.calls)
		}
	case ran:
		if a.turn == nil || a.turn.step != e.step {
			return
		}
		a.answer(e.step.calls, e.outcome)
	}
}

func (a *Agent) Snapshot() agent.Snapshot {
	approvals := []agent.Approval{}
	if a.turn != nil {
		if s, ok := a.turn.step.(*asking); ok {
			approvals = append(approvals, agent.Approval{RequestID: s.requestID, Request: s.request, ToolName: "shell"})
		}
	}
	return agent.Snapshot{State: a.tracker.State(), Approvals: approvals, Queued: a.tracker.Queued()}
}

// Send queues the input, and starts a turn with it when no turn is running.
func (a *Agent) Send(input agent.Input) error {
	a.tracker.Accept(input)
	if a.turn == nil {
		a.begin()
	}
	return nil
}

// begin starts a turn with the first queued input. An input sent during a turn
// is taken before the turn's next request, after the commands of the response
// before it, or waits for the turn to end if the response was its last.
func (a *Agent) begin() {
	ctx, cancel := context.WithCancel(context.Background())
	a.turn = &turn{ctx: ctx, cancel: cancel}
	a.request()
}

// request takes every queued input into the turn and posts the next request.
func (a *Agent) request() {
	for a.tracker.Len() > 0 {
		input := a.tracker.Take()
		a.turn.prompt = input.MessageID
		if err := a.save(message{Role: "user", Content: input.Content}); err != nil {
			a.finish(agent.Result{Text: "Ur could not save the prompt: " + err.Error(), IsError: true})
			return
		}
	}
	a.turn.step = streaming{}
	go a.stream(a.turn.ctx, a.turn, a.body())
}

// next takes up calls, the response's calls not yet answered, oldest first,
// and posts the next request once every call is answered. A call whose
// arguments are invalid is answered at once; a valid one runs in auto mode and
// asks for approval otherwise.
func (a *Agent) next(calls []toolCall) {
	if len(calls) == 0 {
		a.request()
		return
	}
	call := calls[0]
	input := json.RawMessage(call.Function.Arguments)
	var object map[string]json.RawMessage
	if json.Unmarshal(input, &object) != nil || object == nil {
		input = json.RawMessage("{}")
	}
	a.publish(agent.Update{Type: agent.UpdateToolUse, ToolUseID: call.ID, ToolName: call.Function.Name, Input: input})
	if call.Function.Name != "shell" {
		a.answer(calls, outcome{fmt.Sprintf("Ur has no tool %q", call.Function.Name), true})
		return
	}
	command, err := parseShell(call.Function.Arguments)
	if err != nil {
		a.answer(calls, outcome{err.Error(), true})
		return
	}
	if a.settings.PermissionMode == "auto" {
		a.execute(calls, command)
		return
	}
	request, _ := json.Marshal(map[string]any{"tool_name": "shell", "input": input, "tool_use_id": call.ID}) // strings and valid JSON always marshal
	s := &asking{calls: calls, command: command, requestID: rand.Text(), request: request}
	a.turn.step = s
	a.tracker.Report(agent.StateRequiresAction, "")
	a.publish(agent.Update{Type: agent.UpdateApproval, RequestID: s.requestID, Request: s.request})
}

// execute runs calls[0]'s command on its own goroutine, which delivers its
// outcome.
func (a *Agent) execute(calls []toolCall, command shellCommand) {
	s := &executing{calls: calls}
	a.turn.step = s
	ctx := a.turn.ctx
	commands.Add(1)
	go func() {
		o := runShell(ctx, a.workspace, command)
		commands.Done()
		a.deliver(ran{s, o})
	}()
}

// answer saves and publishes calls[0]'s outcome and takes up the rest.
func (a *Agent) answer(calls []toolCall, o outcome) {
	if err := a.save(message{Role: "tool", ToolCallID: calls[0].ID, Content: o.text}); err != nil {
		a.finish(agent.Result{Text: "Ur could not save the command's result: " + err.Error(), IsError: true})
		return
	}
	a.publish(agent.Update{Type: agent.UpdateToolResult, ToolUseID: calls[0].ID, Result: &agent.Result{Text: o.text, IsError: o.isError}})
	a.next(calls[1:])
}

// body builds the next request from the conversation with the current
// settings.
func (a *Agent) body() []byte {
	body := request{Model: a.settings.Model, Messages: conversation(a.history), Tools: []json.RawMessage{shellTool}, Stream: true}
	if a.settings.Effort != "" {
		body.Reasoning = &reasoning{Effort: a.settings.Effort}
	}
	if order := a.providers[a.settings.Model]; len(order) > 0 {
		body.Provider = &provider{Order: order}
	}
	data, _ := json.Marshal(body) // strings, slices of them, and valid JSON always marshal
	return data
}

// stopped answers a call whose command did not finish: an interrupt, Stop, or
// oxd exiting during it.
const stopped = "The user stopped this command before it finished."

// conversation joins each run of assistant entries in history, the deltas and
// tool calls of one response, into one message, and answers each call left
// without a tool entry before the next user entry with stopped.
func conversation(history []message) []message {
	var messages []message
	var open []string // IDs of the latest response's calls without a tool entry
	for _, m := range history {
		last := len(messages) - 1
		switch {
		case m.Role == "assistant" && last >= 0 && messages[last].Role == "assistant":
			messages[last].Content += m.Content
			messages[last].ToolCalls = append(messages[last].ToolCalls, m.ToolCalls...)
		case m.Role == "tool":
			open = slices.DeleteFunc(open, func(id string) bool { return id == m.ToolCallID })
			messages = append(messages, m)
		default:
			if m.Role == "user" {
				for _, id := range open {
					messages = append(messages, message{Role: "tool", ToolCallID: id, Content: stopped})
				}
				open = nil
			}
			messages = append(messages, m)
		}
		for _, call := range m.ToolCalls {
			open = append(open, call.ID)
		}
	}
	return messages
}

// finish ends the running turn with result and begins the next queued input.
func (a *Agent) finish(result agent.Result) {
	a.publish(agent.Update{Type: agent.UpdateResult, Result: &result})
	a.tracker.Report(agent.StateIdle, "")
	a.turn.cancel()
	a.turn = nil
	if a.tracker.Len() > 0 {
		a.begin()
	}
}

// Approve answers the pending shell call's approval. Allow runs the command as
// the model wrote it; deny tells the model the user's message.
func (a *Agent) Approve(requestID string, raw json.RawMessage) error {
	var response struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if json.Unmarshal(raw, &response) != nil || (response.Behavior != "allow" && response.Behavior != "deny") {
		return errors.New("response must be an allow or deny permission response")
	}
	var s *asking
	if a.turn != nil {
		s, _ = a.turn.step.(*asking)
	}
	if s == nil || s.requestID != requestID {
		return agent.ErrApprovalNotFound
	}
	a.publish(agent.Update{Type: agent.UpdateApprovalAnswered, RequestID: requestID})
	a.tracker.Report(agent.StateRunning, "")
	if response.Behavior == "allow" {
		a.execute(s.calls, s.command)
	} else {
		a.answer(s.calls, outcome{"The user denied this command: " + response.Message, true})
	}
	return nil
}

// Interrupt ends the turn, answering the call it waits on, if any, with
// stopped.
func (a *Agent) Interrupt() error {
	a.tracker.DropFrom(0)
	if a.turn == nil {
		return nil
	}
	var calls []toolCall
	switch s := a.turn.step.(type) {
	case streaming:
	case *asking:
		a.publish(agent.Update{Type: agent.UpdateApprovalCancelled, RequestID: s.requestID})
		calls = s.calls
	case *executing:
		calls = s.calls
	}
	if calls != nil {
		a.publish(agent.Update{Type: agent.UpdateToolResult, ToolUseID: calls[0].ID, Result: &agent.Result{Text: stopped, IsError: true}})
	}
	a.finish(agent.Result{Text: "Interrupted", IsError: true})
	return nil
}

// Configure applies the patch at once; the next request reads the settings.
func (a *Agent) Configure(patch agent.SettingsPatch, done func(error)) {
	if !a.tracker.Idle() {
		done(agent.ErrBusy)
		return
	}
	if settings := patch.Apply(a.settings); settings != a.settings {
		a.settings = settings
		a.onConfigured(settings)
	}
	done(nil)
}

// Parkable is true while idle: the file holds the whole conversation, and no
// command outlives its turn.
func (a *Agent) Parkable() bool {
	return a.tracker.Idle()
}

func (a *Agent) Stop() {
	a.tracker.DropFrom(0)
	if a.turn != nil {
		if s, ok := a.turn.step.(*asking); ok {
			a.publish(agent.Update{Type: agent.UpdateApprovalCancelled, RequestID: s.requestID})
		}
		a.turn.cancel()
		a.publish(agent.Update{Type: agent.UpdateExited, Error: "stopped"})
		a.turn = nil
	}
	a.exited = true
	close(a.done)
}

func (a *Agent) Exited() bool {
	return a.exited
}
