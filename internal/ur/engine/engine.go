package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/ur/wire"
)

// engine runs one conversation. Its client, workspace, forwarder, and output
// are set before the loop starts, and request and command goroutines read
// them; loopState is the loop's own.
type engine struct {
	client    client
	workspace string
	ox        *forwarder    // nil without -mcp
	out       *json.Encoder // ur's stdout
	stderr    io.Writer
	events    chan event    // every goroutine's output, read only by the loop
	stopped   chan struct{} // closed once the loop has stopped, so a goroutine delivers nothing after it
	// shells counts the commands whose process groups have not been killed
	// and reaped. Only the loop goroutine adds to it, before it stops.
	shells sync.WaitGroup

	loopState
}

// loopState is the state only the loop goroutine touches.
type loopState struct {
	settings agent.Settings
	conv     *conversation
	queued   []wire.Input // read and not yet taken, oldest first
	turn     *turn        // the running turn; nil while idle
}

// event is what the loop takes from the other goroutines.
//
//sumtype:decl
type event interface{ isEvent() }

// input is an input command's input.
type input struct{ wire.Input }

// approval answers the pending call named by requestID. Only allow runs it.
type approval struct {
	requestID string
	allow     bool
	message   string // a denial's message for the model
}

// interruption is an interrupt command.
type interruption struct{}

// configuration is a configure command's settings, for the next request.
type configuration struct{ agent.Settings }

// inputEnded ends stdin, with the error that ended it, or nil at its end.
type inputEnded struct{ err error }

func (input) isEvent()           {}
func (approval) isEvent()        {}
func (interruption) isEvent()    {}
func (configuration) isEvent()   {}
func (inputEnded) isEvent()      {}
func (delta) isEvent()           {}
func (streamed) isEvent()        {}
func (ran) isEvent()             {}
func (answered) isEvent()        {}
func (forwarderExited) isEvent() {}

// serve runs the conversation until stdin ends, SIGTERM arrives, or the
// forwarder of mcp, when given, exits, then ends the turn, which kills each
// command's process group, and returns once every command is reaped and the
// forwarder has exited.
func (e *engine) serve(stdin io.Reader, dir, token string, mcp *agent.MCPServer, sessionID string) int {
	terminate := make(chan os.Signal, 1)
	signal.Notify(terminate, syscall.SIGTERM)
	defer signal.Stop(terminate)
	go e.read(stdin)

	conv, err := openConversation(dir, token)
	if err != nil {
		// oxd ends the session with the lost conversation, and ur waits to
		// be stopped, reading its input meanwhile so that no write to it
		// blocks or fails.
		fmt.Fprintf(e.stderr, "resume conversation: %v\n", err)
		e.emit(wire.Event{Type: wire.EventResumable})
		defer close(e.stopped)
		for {
			select {
			case ev := <-e.events:
				if _, ok := ev.(inputEnded); ok {
					return 1
				}
			case <-terminate:
				return 1
			}
		}
	}
	e.conv = conv
	if mcp != nil {
		f, err := startForwarder(*mcp, sessionID, e.stderr)
		if err != nil {
			fmt.Fprintf(e.stderr, "start ox-mcp: %v\n", err)
			close(e.stopped)
			return 1
		}
		e.ox = f
		go f.read(e.deliver)
	}
	err = e.loop(terminate)
	e.stop()
	if err != nil {
		fmt.Fprintln(e.stderr, err)
		return 1
	}
	return 0
}

// loop handles events until one stops ur, and returns the error ur stops
// with, or nil when it stops cleanly. A step's error ends the running turn.
func (e *engine) loop(terminate <-chan os.Signal) error {
	for {
		select {
		case <-terminate:
			return nil
		case ev := <-e.events:
			switch ev := ev.(type) {
			case inputEnded:
				return ev.err
			case forwarderExited:
				if ev.err != nil {
					return fmt.Errorf("ox-mcp exited: %w", ev.err)
				}
				return errors.New("ox-mcp exited")
			default:
				if err := e.handle(ev); err != nil {
					e.fail(err)
				}
				e.startNext()
			}
		}
	}
}

// handle carries out a command or takes a request's, command's, or call's
// output. An error it returns is the running turn's.
func (e *engine) handle(ev event) error {
	switch ev := ev.(type) {
	case input:
		e.queued = append(e.queued, ev.Input)
	case approval:
		return e.approve(ev)
	case interruption:
		e.interrupt()
	case configuration:
		e.settings = ev.Settings
	case delta:
		return e.onDelta(ev)
	case streamed:
		return e.onStreamed(ev)
	case ran:
		return e.onRan(ev)
	case answered:
		return e.onAnswered(ev)
	case inputEnded, forwarderExited:
		// The loop stops on these before it would handle them.
	}
	return nil
}

// startNext begins a turn with the queued inputs while none runs.
func (e *engine) startNext() {
	for e.turn == nil && len(e.queued) > 0 {
		if err := e.begin(); err != nil {
			e.fail(err)
		}
	}
}

// read reads commands from stdin on its own goroutine, so that oxd's writes
// never wait on the loop, and delivers each one until stdin ends or a line is
// not a command, which it delivers as inputEnded.
func (e *engine) read(stdin io.Reader) {
	decoder := json.NewDecoder(stdin)
	for {
		var c wire.Command
		if err := decoder.Decode(&c); err != nil {
			if errors.Is(err, io.EOF) {
				e.deliver(inputEnded{})
			} else {
				e.deliver(inputEnded{fmt.Errorf("read command: %w", err)})
			}
			return
		}
		ev, err := command(c)
		if err != nil {
			e.deliver(inputEnded{err})
			return
		}
		e.deliver(ev)
	}
}

// command checks c and converts it to its event.
func command(c wire.Command) (event, error) {
	switch c.Type {
	case wire.CommandInput:
		if c.Input == nil {
			return nil, errors.New("input command without an input")
		}
		return input{*c.Input}, nil
	case wire.CommandApprove:
		return approval{requestID: c.RequestID, allow: c.Behavior == "allow", message: c.Message}, nil
	case wire.CommandInterrupt:
		return interruption{}, nil
	case wire.CommandConfigure:
		if c.Settings == nil {
			return nil, errors.New("configure command without settings")
		}
		return configuration{*c.Settings}, nil
	}
	return nil, fmt.Errorf("unknown command %q", c.Type)
}

// stop ends the running turn, waits for its commands' process groups to be
// killed and reaped, then ends the forwarder and waits for it to exit. A call
// still waiting on the forwarder gets no answer.
func (e *engine) stop() {
	if e.turn != nil {
		e.turn.cancel()
	}
	close(e.stopped)
	e.shells.Wait()
	if e.ox != nil {
		e.ox.close()
	}
}

// deliver passes ev to the loop unless it has stopped.
func (e *engine) deliver(ev event) {
	select {
	case e.events <- ev:
	case <-e.stopped:
	}
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

// save appends m to the conversation. Saving the first entry makes the
// conversation resumable.
func (e *engine) save(m message) error {
	if err := e.conv.append(m); err != nil {
		return err
	}
	if len(e.conv.entries) == 1 {
		e.emit(wire.Event{Type: wire.EventResumable, Token: e.conv.token})
	}
	return nil
}

// tools is every request's tools: shell, then Ox's.
func (e *engine) tools() []json.RawMessage {
	tools := []json.RawMessage{shellTool}
	if e.ox != nil {
		tools = append(tools, e.ox.definitions...)
	}
	return tools
}
