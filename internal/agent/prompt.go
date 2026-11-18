package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kkestell/ox/internal/claude"
)

type Update struct {
	MessageID string          `json:"message_id,omitempty"` // empty for turns Claude started itself
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	State     string          `json:"state,omitempty"`
	Result    *Result         `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Request   json.RawMessage `json:"request,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Time      time.Time       `json:"time,omitzero"` // when the agent published it
}

type Result struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error"`
}

// submission is the prompt that owns the agent in the queued and prompting
// phases.
type submission struct {
	id string
}

func (a *Agent) submit(text string) (string, error) {
	if a.phase == parking {
		return "", ErrExited
	}
	if !a.idle() {
		return "", ErrBusy
	}
	prompt := &submission{id: claude.NewUUID()}
	if err := a.input.Prompt(prompt.id, text); err != nil {
		return "", fmt.Errorf("send prompt to Claude: %w", err)
	}
	a.prompt = prompt
	a.phase = queued
	a.publish(Update{Type: "prompt", Text: text})
	return prompt.id, nil
}

func (a *Agent) commandStarted(id string) {
	if a.phase == queued && a.prompt.id == id {
		a.phase = prompting
	}
}

func (a *Agent) sessionState(state string) {
	a.claudeState = state
	update := Update{Type: "state", State: state}
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
func (a *Agent) endPrompt(final Update) {
	a.withdrawApprovals()
	a.publish(final)
	a.prompt = nil
	a.phase = available
}

// publish sends an update to every subscriber, tagged with the current
// prompt's ID when a client's prompt owns the turn.
func (a *Agent) publish(update Update) {
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
