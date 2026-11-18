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

func (a *Agent) submit(text string) (string, error) {
	if a.phase == parking {
		return "", agent.ErrExited
	}
	if !a.idle() {
		return "", agent.ErrBusy
	}
	prompt := &submission{id: NewUUID()}
	if err := a.input.Prompt(prompt.id, text); err != nil {
		return "", fmt.Errorf("send prompt to Claude: %w", err)
	}
	a.prompt = prompt
	a.phase = queued
	a.publish(agent.Update{Type: "prompt", Text: text})
	return prompt.id, nil
}

func (a *Agent) commandStarted(id string) {
	if a.phase == queued && a.prompt.id == id {
		a.phase = prompting
	}
}

func (a *Agent) sessionState(state string) {
	a.claudeState = state
	update := agent.Update{Type: "state", State: state}
	if state != "idle" {
		a.publish(update)
		return
	}
	a.withdrawApprovals()
	switch a.phase {
	case queued:
		// Claude can report idle before it takes the prompt; keep waiting.
	case prompting:
		a.endPrompt(update)
	default:
		a.publish(update)
	}
}

// endPrompt withdraws unanswered approvals, publishes the prompt's final
// update, and releases the agent.
func (a *Agent) endPrompt(final agent.Update) {
	a.withdrawApprovals()
	a.publish(final)
	a.prompt = nil
	a.phase = available
}

// publish sends an update to every subscriber, tagged with the current
// prompt's ID when a client's prompt owns the turn.
func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	if a.prompt != nil {
		update.MessageID = a.prompt.id
	}
	for s := range a.subscribers {
		if !s.Publish(update) {
			delete(a.subscribers, s)
		}
	}
}
