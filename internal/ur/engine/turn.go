package engine

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/ur/wire"
)

// turn is the running turn. Canceling its context ends its request or command.
type turn struct {
	start  int // the conversation's length when the turn began; later entries are the turn's
	ctx    context.Context
	cancel context.CancelFunc
	step   step
}

// step is what the turn waits on.
//
//sumtype:decl
type step interface{ isStep() }

// pending is the response's calls not yet answered: call, the one the step
// waits on, then rest, oldest first.
type pending struct {
	call toolCall
	rest []toolCall
}

// streaming waits on the request.
type streaming struct{}

// asking waits on the approval of call, whose arguments are command, the
// value the approval card shows and that runs.
type asking struct {
	pending
	command   shellCommand
	requestID string
}

// executing waits on call's command.
type executing struct{ pending }

// calling waits on the answer to call, a call of one of Ox's tools written as
// request id.
type calling struct {
	pending
	id int
}

func (streaming) isStep()  {}
func (*asking) isStep()    {}
func (*executing) isStep() {}
func (*calling) isStep()   {}

// delta is a chunk's reply text and reasoning fragments streamed for a turn.
type delta struct {
	turn      *turn
	text      string
	reasoning []reasoningDetail
}

// streamed ends a turn's request, with the response's tool calls or the error
// that ended it early.
type streamed struct {
	turn  *turn
	calls []toolCall
	err   error
}

// ran ends a turn's command, whose process group, group, has been killed and
// reaped.
type ran struct {
	turn    *turn
	group   int
	outcome outcome
}

// current reports whether t is the running turn. A turn runs one goroutine at
// a time and leaves the step that started it only once the goroutine's last
// event arrives or the turn ends, so output for the running turn is for its
// current step.
func (e *engine) current(t *turn) bool {
	return e.turn == t
}

// begin starts a turn with the queued inputs. An input read during a turn is
// taken before the turn's next request, after the commands of the response
// before it, or waits for the turn to end if the response was its last.
func (e *engine) begin() error {
	ctx, cancel := context.WithCancel(context.Background())
	e.turn = &turn{start: len(e.conv.entries), ctx: ctx, cancel: cancel}
	return e.request()
}

// request takes every queued input into the turn and posts the next request,
// built from Ur's instructions, read now, and the conversation, with the
// current settings. The instructions are not part of the saved conversation.
func (e *engine) request() error {
	for len(e.queued) > 0 {
		in := e.queued[0]
		e.queued = e.queued[1:]
		e.emit(wire.Event{Type: wire.EventTaken, MessageID: in.MessageID})
		var images []string
		for _, image := range in.Images {
			images = append(images, "data:"+image.MimeType+";base64,"+base64.StdEncoding.EncodeToString(image.Data))
		}
		if err := e.save(message{Role: "user", Content: content{Text: in.Content, Images: images}}); err != nil {
			return fmt.Errorf("Ur could not save the prompt: %w", err)
		}
	}
	var oxSession string
	if e.ox != nil {
		oxSession = e.ox.sessionID
	}
	system, err := instructions(e.workspace, oxSession, time.Now())
	if err != nil {
		return fmt.Errorf("Ur could not read AGENTS.md: %w", err)
	}
	messages := append([]message{{Role: "system", Content: content{Text: system}}}, e.conv.messages(e.turn.start)...)
	body := e.client.body(e.settings, messages, e.tools())
	t := e.turn
	t.step = streaming{}
	go func() {
		calls, err := e.client.post(t.ctx, body, func(text string, reasoning []reasoningDetail) { e.deliver(delta{t, text, reasoning}) })
		e.deliver(streamed{t, calls, err})
	}()
	return nil
}

// onDelta saves a delta, then publishes it.
func (e *engine) onDelta(ev delta) error {
	if !e.current(ev.turn) {
		return nil
	}
	if err := e.save(message{Role: "assistant", Content: content{Text: ev.text}, ReasoningDetails: ev.reasoning}); err != nil {
		return fmt.Errorf("Ur could not save the reply: %w", err)
	}
	var reasoning []byte
	for _, d := range ev.reasoning {
		reasoning = append(reasoning, d.display()...)
	}
	if len(reasoning) > 0 {
		e.publish(agent.Update{Type: agent.UpdateReasoningDelta, Text: string(reasoning)})
	}
	if ev.text != "" {
		e.publish(agent.Update{Type: agent.UpdateTextDelta, Text: ev.text})
	}
	return nil
}

// onStreamed ends the turn with the reply when the response made no calls,
// and otherwise saves its calls and takes them up.
func (e *engine) onStreamed(ev streamed) error {
	if !e.current(ev.turn) {
		return nil
	}
	switch {
	case ev.err != nil:
		return ev.err
	case len(ev.calls) == 0:
		e.finish(agent.Result{Text: e.conv.reply()})
		return nil
	}
	if err := e.save(message{Role: "assistant", ToolCalls: ev.calls}); err != nil {
		return fmt.Errorf("Ur could not save the reply: %w", err)
	}
	return e.next(ev.calls)
}

// onRan reports that a command's process group has ended, then answers its
// call when its turn is running.
func (e *engine) onRan(ev ran) error {
	e.emit(wire.Event{Type: wire.EventCommandEnded, Group: ev.group})
	if !e.current(ev.turn) {
		return nil
	}
	return e.answer(e.turn.step.(*executing).pending, ev.outcome)
}

// onAnswered answers the call the turn waits on with the forwarder's answer
// to it. Any other answer is to a call whose turn an interrupt or stop ended.
func (e *engine) onAnswered(ev answered) error {
	if e.turn == nil {
		return nil
	}
	if s, ok := e.turn.step.(*calling); ok && s.id == ev.id {
		return e.answer(s.pending, ev.outcome)
	}
	return nil
}

// next takes up calls, the response's calls not yet answered, oldest first,
// and posts the next request once every call is answered. A call whose
// arguments are invalid is answered at once. A call of one of Ox's tools runs
// without asking; a shell call runs at once when the auto permission mode
// lets it, and asks for approval otherwise.
func (e *engine) next(calls []toolCall) error {
	if len(calls) == 0 {
		return e.request()
	}
	p := pending{call: calls[0], rest: calls[1:]}
	if e.ox.offers(p.call.Function.Name) {
		return e.callOx(p)
	}
	command, err := parseCall(p.call)
	var shownInput json.RawMessage
	if err == nil {
		shownInput, _ = json.Marshal(command) // strings and integers always marshal
	} else {
		shownInput = shown(p.call.Function.Arguments)
	}
	e.publish(agent.Update{Type: agent.UpdateToolUse, ToolUseID: p.call.ID, ToolName: p.call.Function.Name, Input: shownInput})
	if err != nil {
		return e.answer(p, outcome{err.Error(), true})
	}
	if e.settings.PermissionMode == agent.Auto && agent.AutoRuns(command.Command) {
		return e.execute(p, command)
	}
	s := &asking{pending: p, command: command, requestID: rand.Text()}
	e.turn.step = s
	e.state(agent.StateRequiresAction)
	request, _ := json.Marshal(map[string]any{"tool_name": "shell", "input": s.command, "tool_use_id": s.call.ID}) // strings and valid JSON always marshal
	e.publish(agent.Update{Type: agent.UpdateApproval, RequestID: s.requestID, Request: request})
	return nil
}

// shown is a call's arguments as the transcript shows them: the arguments
// when they are one JSON object, and an empty object otherwise.
func shown(arguments string) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(arguments), &object) != nil || object == nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(arguments)
}

// callOx writes p's call, a call of one of Ox's tools, to the forwarder, and
// waits on its answer. Arguments that are not one JSON object are answered at
// once.
func (e *engine) callOx(p pending) error {
	arguments := shown(p.call.Function.Arguments)
	e.publish(agent.Update{Type: agent.UpdateToolUse, ToolUseID: p.call.ID, ToolName: p.call.Function.Name, Input: arguments})
	if string(arguments) != p.call.Function.Arguments {
		return e.answer(p, outcome{"arguments: must be one JSON object", true})
	}
	id, err := e.ox.call(p.call.Function.Name, arguments, p.call.ID)
	if err != nil {
		return e.answer(p, outcome{"Could not reach ox-mcp: " + err.Error(), true})
	}
	e.turn.step = &calling{pending: p, id: id}
	return nil
}

// parseCall checks that call is a shell call with valid arguments.
func parseCall(call toolCall) (shellCommand, error) {
	if call.Function.Name != "shell" {
		return shellCommand{}, fmt.Errorf("Ur has no tool %q", call.Function.Name)
	}
	return parseShell(call.Function.Arguments)
}

// execute runs command, p's call's arguments, on its own goroutine, which
// delivers its outcome.
func (e *engine) execute(p pending, command shellCommand) error {
	sh, err := startShell(e.turn.ctx, e.workspace, command)
	if err != nil {
		return e.answer(p, outcome{"Could not start bash: " + err.Error(), true})
	}
	t := e.turn
	t.step = &executing{p}
	group := sh.cmd.Process.Pid
	e.emit(wire.Event{Type: wire.EventCommandStarted, Group: group})
	e.shells.Add(1)
	go func() {
		o := sh.wait()
		e.shells.Done()
		e.deliver(ran{t, group, o})
	}()
	return nil
}

// answer publishes and saves p's call's outcome and takes up the rest. An
// outcome that cannot be saved is published, then ends the turn.
func (e *engine) answer(p pending, o outcome) error {
	e.publish(agent.Update{Type: agent.UpdateToolResult, ToolUseID: p.call.ID, Result: &agent.Result{Text: o.text, IsError: o.isError}})
	if err := e.save(message{Role: "tool", ToolCallID: p.call.ID, Content: content{Text: o.text}}); err != nil {
		return fmt.Errorf("Ur could not save the command's result: %w", err)
	}
	return e.next(p.rest)
}

// approve answers the pending shell call's approval when it names it. Allow
// runs the command the card showed; deny tells the model the user's message.
func (e *engine) approve(a approval) error {
	var s *asking
	if e.turn != nil {
		s, _ = e.turn.step.(*asking)
	}
	if s == nil || s.requestID != a.requestID {
		return nil
	}
	e.state(agent.StateRunning)
	if a.allow {
		return e.execute(s.pending, s.command)
	}
	return e.answer(s.pending, outcome{"The user denied this command: " + a.message, true})
}

// finish ends the running turn with result.
func (e *engine) finish(result agent.Result) {
	e.publish(agent.Update{Type: agent.UpdateResult, Result: &result})
	e.state(agent.StateIdle)
	e.turn.cancel()
	e.turn = nil
}

// fail ends the running turn with err, a step's error.
func (e *engine) fail(err error) {
	e.finish(agent.Result{Text: err.Error(), IsError: true})
}

// interrupt drops every queued input and ends the turn at once. A call it
// leaves without a result is answered with stopped in the next request, and
// the page shows it stopped.
func (e *engine) interrupt() {
	for _, in := range e.queued {
		e.emit(wire.Event{Type: wire.EventDropped, MessageID: in.MessageID})
	}
	e.queued = nil
	if e.turn != nil {
		if s, ok := e.turn.step.(*asking); ok {
			e.publish(agent.Update{Type: agent.UpdateApprovalCancelled, RequestID: s.requestID})
		}
		e.finish(agent.Result{Text: "Interrupted", IsError: true})
	}
	e.emit(wire.Event{Type: wire.EventInterrupted})
}
