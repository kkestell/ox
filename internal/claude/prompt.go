package claude

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

var Harness = agent.Harness{
	Start:               Start,
	CheckPrompt:         checkPrompt,
	CheckEffort:         checkEffort,
	CheckPermissionMode: checkPermissionMode,
	Ask:                 Ask,
}

func checkPrompt(text string) error {
	for first := range strings.FieldsSeq(text) {
		if first == "/model" || first == "/effort" {
			return errors.New("change the model and effort in the session's settings")
		}
		break
	}
	return nil
}

// Send queues an input behind every input Claude has not taken. Claude takes
// it into the running turn after its current tool call, or runs it next if
// the turn ends first. The agent writes it at once when nothing is untaken
// and Claude can take input; otherwise it writes it once Claude has answered
// initialize, a configuration has ended, or Claude has taken or dropped the
// input ahead of it.
func (a *Agent) Send(input agent.Input) error {
	switch a.phase.(type) {
	case available, *prompting:
		if a.tracker.Len() == 0 {
			if err := a.write(input); err != nil {
				return err
			}
		}
	case *starting, *configuring, timedOut:
		// Held until ready, or dropped by the end.
	case exited:
		return agent.ErrExited
	}
	a.tracker.Accept(input)
	return nil
}

// write writes input to Claude, which holds the agent in prompting until
// Claude reports idle with nothing untaken.
func (a *Agent) write(input agent.Input) error {
	if err := a.input.Prompt(input.MessageID, input.Content, input.Images); err != nil {
		return fmt.Errorf("send prompt to Claude: %w", err)
	}
	p, ok := a.phase.(*prompting)
	if !ok {
		p = &prompting{}
		a.phase = p
	}
	p.messageID = input.MessageID
	return nil
}

// writeNext writes the first untaken input. A failed write means Claude is
// exiting, and its exit ends the agent, so every untaken input is dropped.
func (a *Agent) writeNext() {
	next, ok := a.tracker.First()
	if !ok {
		return
	}
	if err := a.write(next); err != nil {
		a.onError(err)
		a.tracker.DropFrom(0)
	}
}

// ready makes the agent available, once Claude has answered initialize, a
// configuration has ended, or a prompt has been released, and writes the
// first untaken input.
func (a *Agent) ready() {
	a.phase = available{}
	a.writeNext()
}

// Interrupt drops every input not yet written to Claude, then asks Claude to
// stop the running turn and to drop the written input it has not taken.
// Claude ends the turn with an error result and an idle state, which end the
// prompts as usual. Its control response carries nothing Ox uses. While a
// configuration runs, Interrupt writes nothing.
func (a *Agent) Interrupt() error {
	written := 0
	if _, prompting := a.phase.(*prompting); prompting && a.tracker.Len() > 0 {
		written = 1
	}
	a.tracker.DropFrom(written)
	switch a.phase.(type) {
	case *starting, available, *prompting:
		if a.tracker.Idle() {
			return nil
		}
	case *configuring, timedOut, exited:
		return nil
	}
	request := map[string]any{"subtype": "interrupt"}
	if written > 0 {
		request["cancel_queued"] = true
	}
	if err := a.input.Control(newUUID(), request); err != nil {
		return fmt.Errorf("interrupt Claude: %w", err)
	}
	return nil
}

// waiting returns the prompting phase whose written input is id, or nil.
func (a *Agent) waiting(id string) *prompting {
	p, ok := a.phase.(*prompting)
	if first, _ := a.tracker.First(); ok && id != "" && id == first.MessageID {
		return p
	}
	return nil
}

func (a *Agent) commandStarted(id string) {
	p := a.waiting(id)
	if p == nil {
		return
	}
	p.turn = id
	a.tracker.Take()
	a.writeNext()
}

// commandCancelled handles Claude dropping a prompt. Claude also reports a
// taken prompt whose turn it interrupted as cancelled, which changes nothing.
func (a *Agent) commandCancelled(id string) {
	if a.waiting(id) == nil {
		return
	}
	a.tracker.DropFirst()
	a.writeNext()
}

// sessionState records Claude's state. Idle with nothing untaken releases a
// prompt, naming the latest input written.
func (a *Agent) sessionState(state string) {
	if state == agent.StateIdle {
		a.withdrawApprovals()
		if p, ok := a.phase.(*prompting); ok && a.tracker.Len() == 0 {
			a.tracker.Report(state, p.messageID)
			a.ready()
			return
		}
	}
	a.tracker.Report(state, "")
}

// publish reports an update, tagged with the client prompt whose turn
// produced it unless it names its prompt itself.
func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	if p, ok := a.phase.(*prompting); ok && update.MessageID == "" {
		update.MessageID = p.turn
	}
	a.onUpdate(update)
}
