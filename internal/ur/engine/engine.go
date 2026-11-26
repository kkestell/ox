// Package engine is the ur program, Ox's built-in harness, which chats through
// OpenRouter's chat completions API and runs the model's shell commands. oxd
// drives it over stdin and stdout with the wire protocol. Each turn is a
// streamed request carrying Ur's instructions and then the conversation so
// far, which ur keeps in a file of its own, then a request after each
// response's commands have run.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/ur/wire"
)

// Endpoint is OpenRouter's chat completions API.
const Endpoint = "https://openrouter.ai/api/v1/chat/completions"

// Main runs ur with args, the command line without the program name, and
// returns its exit status.
//
// Agent mode, run in the session's workspace:
//
//	ur -dir <conversations> [-resume <token>] -model <id> [-effort <e>] -permission-mode <m> [-providers <json>] [-endpoint <url>]
//
// reads Commands on stdin and writes Events on stdout until stdin ends or
// SIGTERM. Ask mode:
//
//	ur -ask -model <id> [-effort <e>] -instructions <text> [-providers <json>] [-endpoint <url>]
//
// sends the text on stdin and prints the reply.
func Main(args []string) int {
	// A Ctrl-C in oxd's terminal reaches ur only as oxd's orderly stop. A
	// write to an oxd that has gone fails instead of killing ur before it has
	// killed its commands. The signals are handled rather than ignored, so
	// each command starts with their defaults.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGINT, syscall.SIGPIPE)
	return run(args, os.Stdin, os.Stdout, os.Stderr)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ur", flag.ContinueOnError)
	flags.SetOutput(stderr)
	askMode := flags.Bool("ask", false, "send the text on stdin once and print the reply")
	dir := flags.String("dir", "", "the directory of conversation files")
	resume := flags.String("resume", "", "the token of the conversation to resume")
	model := flags.String("model", "", "the model's ID")
	effort := flags.String("effort", "", "the reasoning effort")
	mode := flags.String("permission-mode", "manual", "manual or auto")
	system := flags.String("instructions", "", "ask mode's system message")
	providersJSON := flags.String("providers", "", "a JSON object from model ID to provider order")
	endpoint := flags.String("endpoint", Endpoint, "the chat completions endpoint")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	var providers map[string][]string
	if *providersJSON != "" {
		if err := json.Unmarshal([]byte(*providersJSON), &providers); err != nil {
			fmt.Fprintf(stderr, "-providers: %v\n", err)
			return 2
		}
	}
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		fmt.Fprintln(stderr, "OPENROUTER_API_KEY is not set")
		return 1
	}
	if *askMode {
		text, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		reply, err := ask(context.Background(), *endpoint, key, providers, *model, *effort, *system, string(text))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprint(stdout, reply)
		return 0
	}
	if *dir == "" {
		fmt.Fprintln(stderr, "-dir is required")
		return 2
	}
	workspace, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	e := &engine{
		endpoint: *endpoint, key: key, providers: providers, dir: *dir, id: *resume, workspace: workspace,
		out: json.NewEncoder(stdout), stderr: stderr,
		events: make(chan any), stopped: make(chan struct{}),
		settings: agent.Settings{Model: *model, Effort: *effort, PermissionMode: *mode},
	}
	return e.serve(stdin)
}

// engine holds one conversation, which it appends to the file <dir>/<id>.jsonl
// and announces by its ID as the resume token. Every field is owned by the
// loop goroutine, except those set at start, which the request goroutines
// read.
type engine struct {
	endpoint  string
	key       string
	providers map[string][]string
	dir       string
	id        string
	workspace string
	out       *json.Encoder // ur's stdout
	stderr    io.Writer
	events    chan any      // the turn's own goroutines' output
	stopped   chan struct{} // closed once the loop has stopped, so a goroutine delivers nothing after it

	settings agent.Settings
	history  []message    // exactly the file's entries; only save adds to it
	queued   []wire.Input // read and not yet taken, oldest first
	turn     *turn        // the running turn; nil while idle
	// shells counts the commands whose process groups have not been killed
	// and reaped. Only the loop goroutine adds to it, before it stops.
	shells sync.WaitGroup
}

// serve runs the conversation until stdin ends or SIGTERM arrives, then ends
// the turn, which kills each command's process group, and returns once every
// command is reaped.
func (e *engine) serve(stdin io.Reader) int {
	terminate := make(chan os.Signal, 1)
	signal.Notify(terminate, syscall.SIGTERM)
	defer signal.Stop(terminate)
	commands, readErr := e.read(stdin)

	status := 0
	if e.id == "" {
		e.id = rand.Text()
	} else if err := e.load(); err != nil {
		// oxd ends the session with the lost conversation, and ur waits to
		// be stopped, reading its input meanwhile so that no write to it
		// blocks or fails.
		fmt.Fprintf(e.stderr, "resume conversation: %v\n", err)
		e.emit(wire.Event{Type: wire.EventResumable})
		defer close(e.stopped)
		for {
			select {
			case _, ok := <-commands:
				if !ok {
					return 1
				}
			case <-terminate:
				return 1
			}
		}
	}
	for {
		select {
		case c, ok := <-commands:
			if !ok {
				if err := *readErr; err != nil {
					fmt.Fprintf(e.stderr, "read command: %v\n", err)
					status = 1
				}
				e.stop()
				return status
			}
			if err := e.command(c); err != nil {
				fmt.Fprintln(e.stderr, err)
				e.stop()
				return 1
			}
		case event := <-e.events:
			e.handle(event)
		case <-terminate:
			e.stop()
			return status
		}
	}
}

// read reads commands from stdin on its own goroutine, so that oxd's writes
// never wait on the loop, until stdin ends or the loop stops. Once commands is
// closed, readErr holds the error that ended a stdin that did not reach EOF.
func (e *engine) read(stdin io.Reader) (commands <-chan wire.Command, readErr *error) {
	c := make(chan wire.Command)
	readErr = new(error)
	go func() {
		defer close(c)
		decoder := json.NewDecoder(stdin)
		for {
			var command wire.Command
			if err := decoder.Decode(&command); err != nil {
				if !errors.Is(err, io.EOF) {
					*readErr = err
				}
				return
			}
			select {
			case c <- command:
			case <-e.stopped:
				return
			}
		}
	}()
	return c, readErr
}

// stop ends the running turn and waits for its commands' process groups to be
// killed and reaped.
func (e *engine) stop() {
	if e.turn != nil {
		e.turn.cancel()
	}
	close(e.stopped)
	e.shells.Wait()
}

// command carries out a command from oxd.
func (e *engine) command(c wire.Command) error {
	switch c.Type {
	case wire.CommandInput:
		if c.Input == nil {
			return errors.New("input command without an input")
		}
		e.queued = append(e.queued, *c.Input)
		if e.turn == nil {
			e.begin()
		}
	case wire.CommandApprove:
		e.approve(c.RequestID, c.Behavior, c.Message)
	case wire.CommandInterrupt:
		e.interrupt()
	case wire.CommandConfigure:
		if c.Settings == nil {
			return errors.New("configure command without settings")
		}
		e.settings = *c.Settings
	default:
		return fmt.Errorf("unknown command %q", c.Type)
	}
	return nil
}

// emit writes event to oxd. A write that fails means oxd has gone, and stdin
// ends next.
func (e *engine) emit(event wire.Event) {
	e.out.Encode(event)
}

func (e *engine) publish(update agent.Update) {
	e.emit(wire.Event{Type: wire.EventUpdate, Update: &update})
}

func (e *engine) state(state string) {
	e.emit(wire.Event{Type: wire.EventState, State: state})
}

// deliver passes event to the loop unless it has stopped.
func (e *engine) deliver(event any) {
	select {
	case e.events <- event:
	case <-e.stopped:
	}
}

func (e *engine) path() string {
	return filepath.Join(e.dir, e.id+".jsonl")
}

// load reads the conversation file into history.
func (e *engine) load() error {
	f, err := os.Open(e.path())
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
		e.history = append(e.history, m)
	}
}

// save appends m to the conversation file, and to history once it is
// written. Saving the first entry makes the conversation resumable.
func (e *engine) save(m message) error {
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(e.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
	e.history = append(e.history, m)
	if len(e.history) == 1 {
		e.emit(wire.Event{Type: wire.EventResumable, Token: e.id})
	}
	return nil
}

// turn is the running turn. Canceling its context ends its request or command.
type turn struct {
	start  int // the length of history when the turn began; later entries are the turn's
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
}

// executing waits on calls[0]'s command, the first of the response's calls not
// yet answered.
type executing struct {
	calls []toolCall
}

func (streaming) isStep()  {}
func (*asking) isStep()    {}
func (*executing) isStep() {}

// delta is a chunk's reply text and reasoning fragments streamed for a turn.
type delta struct {
	turn      *turn
	text      string
	reasoning []reasoningDetail
}

// ended ends a turn's request, with the response's tool calls or the error
// that ended it early.
type ended struct {
	turn  *turn
	calls []toolCall
	err   error
}

// ran ends a step's command, whose process group, group, has been killed and
// reaped.
type ran struct {
	step    *executing
	group   int
	outcome outcome
}

// handle takes a request's or command's output. Output for a turn that is no
// longer running is ignored.
func (e *engine) handle(event any) {
	switch ev := event.(type) {
	case delta:
		if ev.turn != e.turn {
			return
		}
		if err := e.save(message{Role: "assistant", Content: content{Text: ev.text}, ReasoningDetails: ev.reasoning}); err != nil {
			e.finish(agent.Result{Text: "Ur could not save the reply: " + err.Error(), IsError: true})
			return
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
	case ended:
		if ev.turn != e.turn {
			return
		}
		switch {
		case ev.err != nil:
			e.finish(agent.Result{Text: ev.err.Error(), IsError: true})
		case len(ev.calls) == 0:
			var reply string
			if messages := conversation(e.history); messages[len(messages)-1].Role == "assistant" {
				reply = messages[len(messages)-1].Content.Text
			}
			e.finish(agent.Result{Text: reply})
		default:
			if err := e.save(message{Role: "assistant", ToolCalls: ev.calls}); err != nil {
				e.finish(agent.Result{Text: "Ur could not save the reply: " + err.Error(), IsError: true})
				return
			}
			e.next(ev.calls)
		}
	case ran:
		e.emit(wire.Event{Type: wire.EventCommandEnded, Group: ev.group})
		if e.turn == nil || e.turn.step != ev.step {
			return
		}
		e.answer(ev.step.calls, ev.outcome)
	}
}

// begin starts a turn with the first queued input. An input read during a turn
// is taken before the turn's next request, after the commands of the response
// before it, or waits for the turn to end if the response was its last.
func (e *engine) begin() {
	ctx, cancel := context.WithCancel(context.Background())
	e.turn = &turn{start: len(e.history), ctx: ctx, cancel: cancel}
	e.request()
}

// request takes every queued input into the turn and posts the next request.
func (e *engine) request() {
	for len(e.queued) > 0 {
		input := e.queued[0]
		e.queued = e.queued[1:]
		e.emit(wire.Event{Type: wire.EventTaken, MessageID: input.MessageID})
		var images []string
		for _, image := range input.Images {
			images = append(images, "data:"+image.MimeType+";base64,"+base64.StdEncoding.EncodeToString(image.Data))
		}
		if err := e.save(message{Role: "user", Content: content{Text: input.Content, Images: images}}); err != nil {
			e.finish(agent.Result{Text: "Ur could not save the prompt: " + err.Error(), IsError: true})
			return
		}
	}
	body, err := e.body()
	if err != nil {
		e.finish(agent.Result{Text: "Ur could not read AGENTS.md: " + err.Error(), IsError: true})
		return
	}
	e.turn.step = streaming{}
	go e.stream(e.turn.ctx, e.turn, body)
}

// next takes up calls, the response's calls not yet answered, oldest first,
// and posts the next request once every call is answered. A call whose
// arguments are invalid is answered at once; a valid one runs in auto mode and
// asks for approval otherwise.
func (e *engine) next(calls []toolCall) {
	if len(calls) == 0 {
		e.request()
		return
	}
	call := calls[0]
	command, err := parseCall(call)
	var input json.RawMessage
	if err == nil {
		input, _ = json.Marshal(command) // strings and integers always marshal
	} else {
		// A call that does not run shows its arguments if they are an object.
		input = json.RawMessage(call.Function.Arguments)
		var object map[string]json.RawMessage
		if json.Unmarshal(input, &object) != nil || object == nil {
			input = json.RawMessage("{}")
		}
	}
	e.publish(agent.Update{Type: agent.UpdateToolUse, ToolUseID: call.ID, ToolName: call.Function.Name, Input: input})
	if err != nil {
		e.answer(calls, outcome{err.Error(), true})
		return
	}
	if e.settings.PermissionMode == "auto" {
		e.execute(calls, command)
		return
	}
	s := &asking{calls: calls, command: command, requestID: rand.Text()}
	e.turn.step = s
	e.state(agent.StateRequiresAction)
	request, _ := json.Marshal(map[string]any{"tool_name": "shell", "input": s.command, "tool_use_id": s.calls[0].ID}) // strings and valid JSON always marshal
	e.publish(agent.Update{Type: agent.UpdateApproval, RequestID: s.requestID, Request: request})
}

// parseCall checks that call is a shell call with valid arguments.
func parseCall(call toolCall) (shellCommand, error) {
	if call.Function.Name != "shell" {
		return shellCommand{}, fmt.Errorf("Ur has no tool %q", call.Function.Name)
	}
	return parseShell(call.Function.Arguments)
}

// execute runs calls[0]'s command on its own goroutine, which delivers its
// outcome.
func (e *engine) execute(calls []toolCall, command shellCommand) {
	sh, err := startShell(e.turn.ctx, e.workspace, command)
	if err != nil {
		e.answer(calls, outcome{"Could not start bash: " + err.Error(), true})
		return
	}
	s := &executing{calls: calls}
	e.turn.step = s
	e.emit(wire.Event{Type: wire.EventCommandStarted, Group: sh.cmd.Process.Pid})
	e.shells.Add(1)
	go func() {
		o := sh.wait()
		e.shells.Done()
		e.deliver(ran{s, sh.cmd.Process.Pid, o})
	}()
}

// answer publishes and saves calls[0]'s outcome and takes up the rest. An
// outcome that cannot be saved is published, then ends the turn.
func (e *engine) answer(calls []toolCall, o outcome) {
	e.publish(agent.Update{Type: agent.UpdateToolResult, ToolUseID: calls[0].ID, Result: &agent.Result{Text: o.text, IsError: o.isError}})
	if err := e.save(message{Role: "tool", ToolCallID: calls[0].ID, Content: content{Text: o.text}}); err != nil {
		e.finish(agent.Result{Text: "Ur could not save the command's result: " + err.Error(), IsError: true})
		return
	}
	e.next(calls[1:])
}

// body builds the next request from Ur's instructions, read now, and the
// conversation, with the current settings. The instructions are not part of
// the saved conversation.
func (e *engine) body() ([]byte, error) {
	text, err := instructions(e.workspace, time.Now())
	if err != nil {
		return nil, err
	}
	// Reasoning models need the current turn's reasoning back in its tool
	// loop. Earlier turns' reasoning stays out.
	history := slices.Clone(e.history)
	for i := range e.turn.start {
		history[i].ReasoningDetails = nil
	}
	messages := append([]message{{Role: "system", Content: content{Text: text}}}, conversation(history)...)
	body := request{Model: e.settings.Model, Messages: messages, Tools: []json.RawMessage{shellTool}, Stream: true}
	if e.settings.Effort != "" {
		body.Reasoning = &reasoning{Effort: e.settings.Effort}
	}
	if order := e.providers[e.settings.Model]; len(order) > 0 {
		body.Provider = &provider{Order: order}
	}
	data, _ := json.Marshal(body) // strings, slices of them, and valid JSON always marshal
	return data, nil
}

// stopped answers a call whose command did not finish: an interrupt, a stop,
// or oxd exiting during it.
const stopped = "The user stopped this command before it finished."

// conversation joins each run of assistant entries in history, the deltas,
// reasoning, and tool calls of one response, into one message, and answers
// each call left without a tool entry before the next user entry with stopped.
func conversation(history []message) []message {
	var messages []message
	var open []string // IDs of the latest response's calls without a tool entry
	for _, m := range history {
		last := len(messages) - 1
		// The merge writes into the message's details, which must not be
		// history's.
		m.ReasoningDetails = appendReasoning(nil, m.ReasoningDetails)
		switch {
		case m.Role == "assistant" && last >= 0 && messages[last].Role == "assistant":
			messages[last].Content.Text += m.Content.Text
			messages[last].ReasoningDetails = appendReasoning(messages[last].ReasoningDetails, m.ReasoningDetails)
			messages[last].ToolCalls = append(messages[last].ToolCalls, m.ToolCalls...)
		case m.Role == "tool":
			open = slices.DeleteFunc(open, func(id string) bool { return id == m.ToolCallID })
			messages = append(messages, m)
		default:
			if m.Role == "user" {
				for _, id := range open {
					messages = append(messages, message{Role: "tool", ToolCallID: id, Content: content{Text: stopped}})
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
func (e *engine) finish(result agent.Result) {
	e.publish(agent.Update{Type: agent.UpdateResult, Result: &result})
	e.state(agent.StateIdle)
	e.turn.cancel()
	e.turn = nil
	if len(e.queued) > 0 {
		e.begin()
	}
}

// approve answers the pending shell call's approval when requestID names it.
// Allow runs the command as the model wrote it; deny tells the model the
// user's message.
func (e *engine) approve(requestID, behavior, msg string) {
	var s *asking
	if e.turn != nil {
		s, _ = e.turn.step.(*asking)
	}
	if s == nil || s.requestID != requestID {
		return
	}
	e.state(agent.StateRunning)
	if behavior == "allow" {
		e.execute(s.calls, s.command)
	} else {
		e.answer(s.calls, outcome{"The user denied this command: " + msg, true})
	}
}

// interrupt drops every queued input and ends the turn at once. A call it
// leaves without a result is answered with stopped in the next request, and
// the page shows it stopped.
func (e *engine) interrupt() {
	for _, input := range e.queued {
		e.emit(wire.Event{Type: wire.EventDropped, MessageID: input.MessageID})
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
