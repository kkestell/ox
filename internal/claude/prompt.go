package claude

import (
	"fmt"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// submission is the prompt that owns the agent in the queued and prompting
// phases.
type submission struct {
	id string
}

func (a *Agent) Send(text string) (string, error) {
	if !a.idle() {
		return "", agent.ErrBusy
	}
	prompt := &submission{id: newUUID()}
	if err := a.input.Prompt(prompt.id, text); err != nil {
		return "", fmt.Errorf("send prompt to Claude: %w", err)
	}
	a.prompt = prompt
	a.phase = queued
	a.publish(agent.Update{Type: "prompt", Text: text})
	a.setState("running")
	return prompt.id, nil
}

// Interrupt asks Claude to stop the running turn. Claude ends it with an
// error result and an idle state, which end a prompt as usual; a prompt it
// has not taken yet starts and stops the same way. Its control response
// carries nothing Ox uses.
func (a *Agent) Interrupt() error {
	if a.state == "idle" {
		return nil
	}
	if err := a.input.Control(newUUID(), map[string]any{"subtype": "interrupt"}); err != nil {
		return fmt.Errorf("interrupt Claude: %w", err)
	}
	return nil
}

func (a *Agent) commandStarted(id string) {
	if a.phase == queued && a.prompt.id == id {
		a.phase = prompting
	}
}

func (a *Agent) sessionState(state string) {
	if state != "idle" {
		a.setState(state)
		return
	}
	a.withdrawApprovals()
	switch a.phase {
	case queued:
		// Claude can report idle before it takes the prompt; keep waiting.
	case prompting:
		a.state = state
		a.endPrompt(agent.Update{Type: "state", State: state})
	default:
		a.setState(state)
	}
}

// setState publishes a change to the state clients see.
func (a *Agent) setState(state string) {
	if state != a.state {
		a.state = state
		a.publish(agent.Update{Type: "state", State: state})
	}
}

func (a *Agent) endPrompt(final agent.Update) {
	a.withdrawApprovals()
	a.publish(final)
	a.prompt = nil
	a.phase = available
}

// publish reports an update, tagged with the current prompt's ID when a
// client's prompt owns the turn.
func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	if a.prompt != nil {
		update.MessageID = a.prompt.id
	}
	a.onUpdate(update)
}
