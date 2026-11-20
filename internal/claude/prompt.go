package claude

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

var Harness = agent.Harness{Start: Start, CheckPrompt: checkPrompt}

func checkPrompt(text string) error {
	for first := range strings.FieldsSeq(text) {
		if first == "/model" || first == "/effort" {
			return errors.New("change the model and effort in the session's settings")
		}
		break
	}
	return nil
}

// Send writes an input, which Claude takes into the running turn after its
// current tool call, or runs next if the turn ends first.
func (a *Agent) Send(input agent.Input) error {
	var p *prompting
	switch phase := a.phase.(type) {
	case available:
	case *prompting:
		if phase.queued {
			return agent.ErrBusy
		}
		p = phase
	default:
		return agent.ErrBusy
	}
	id := input.MessageID
	if err := a.input.Prompt(id, input.Content); err != nil {
		return fmt.Errorf("send prompt to Claude: %w", err)
	}
	if p == nil {
		p = &prompting{}
		a.phase = p
	}
	p.input, p.queued = input, true
	a.publish(input.Update(agent.UpdatePromptQueued))
	if a.state == agent.StateIdle {
		a.state = agent.StateRunning
		a.publish(agent.Update{MessageID: id, Type: agent.UpdateState, State: a.state})
	}
	return nil
}

// Interrupt asks Claude to stop the running turn and to drop a prompt it has
// not taken yet. Claude ends the turn with an error result and an idle state,
// which end the prompts as usual. Its control response carries nothing Ox
// uses.
func (a *Agent) Interrupt() error {
	if a.state == agent.StateIdle {
		return nil
	}
	request := map[string]any{"subtype": "interrupt"}
	if a.queued() != nil {
		request["cancel_queued"] = true
	}
	if err := a.input.Control(newUUID(), request); err != nil {
		return fmt.Errorf("interrupt Claude: %w", err)
	}
	return nil
}

// queued returns a copy of the input Claude has not taken yet, or nil.
func (a *Agent) queued() *agent.Input {
	if p, ok := a.phase.(*prompting); ok && p.queued {
		input := p.input
		return &input
	}
	return nil
}

// waiting returns the prompting phase whose queued input is id, or nil.
func (a *Agent) waiting(id string) *prompting {
	if p, ok := a.phase.(*prompting); ok && p.queued && id != "" && id == p.input.MessageID {
		return p
	}
	return nil
}

func (a *Agent) commandStarted(id string) {
	p := a.waiting(id)
	if p == nil {
		return
	}
	p.queued = false
	p.turn = id
	a.publish(p.input.Update(agent.UpdatePrompt))
}

// commandCancelled handles Claude dropping a prompt. Claude also reports a
// taken prompt whose turn it interrupted as cancelled, which changes nothing.
func (a *Agent) commandCancelled(id string) {
	if p := a.waiting(id); p != nil {
		a.dropQueued(p)
	}
}

// dropQueued ends the queued prompt before Claude took it.
func (a *Agent) dropQueued(p *prompting) {
	p.queued = false
	a.publish(p.input.Update(agent.UpdatePromptCancelled))
}

func (a *Agent) sessionState(state string) {
	if state != agent.StateIdle {
		a.setState(state)
		return
	}
	a.withdrawApprovals()
	p, ok := a.phase.(*prompting)
	switch {
	case ok && p.queued:
		// Claude can report idle before it takes a prompt; keep waiting.
	case ok:
		a.state = state
		a.endPrompt(p, agent.Update{Type: agent.UpdateState, State: state})
	default:
		a.setState(state)
	}
}

// setState publishes a change to the state clients see.
func (a *Agent) setState(state string) {
	if state != a.state {
		a.state = state
		a.publish(agent.Update{Type: agent.UpdateState, State: state})
	}
}

// endPrompt releases the agent with the final update of the prompts that
// held it, which names the latest of them.
func (a *Agent) endPrompt(p *prompting, final agent.Update) {
	a.withdrawApprovals()
	final.MessageID = p.input.MessageID
	a.publish(final)
	a.phase = available{}
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
