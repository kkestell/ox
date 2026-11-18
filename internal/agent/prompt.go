package agent

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)

type Update struct {
	MessageID string          `json:"message_id"`
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	State     string          `json:"state,omitempty"`
	Result    *Result         `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Request   json.RawMessage `json:"request,omitempty"`
}

type Result struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error"`
}

// submission is the prompt that owns the agent in the queued and prompting
// phases.
type submission struct {
	id        string
	approvals map[string]json.RawMessage // pending, by Claude's request ID
}

func (a *Agent) submit(s Subscriber, text string) (string, error) {
	if !a.idle() {
		return "", ErrBusy
	}
	prompt := &submission{id: newID(), approvals: make(map[string]json.RawMessage)}
	if err := a.input.Prompt(prompt.id, text); err != nil {
		return "", fmt.Errorf("send prompt to Claude: %w", err)
	}
	a.prompt = prompt
	a.phase = queued
	a.subscribers[s] = struct{}{}
	return prompt.id, nil
}

func (a *Agent) commandStarted(id string) {
	if a.phase == queued && a.prompt.id == id {
		a.phase = prompting
	}
}

func (a *Agent) sessionState(state string) {
	a.claudeState = state
	switch {
	case a.prompt == nil:
	case state != "idle":
		a.publish(Update{Type: "state", State: state})
	case a.phase == queued:
		// Claude can report idle before it takes the prompt; keep waiting.
	case a.phase == prompting:
		a.endPrompt(Update{Type: "state", State: state})
	}
}

// endPrompt withdraws the prompt's unanswered approvals, publishes its final
// update, and releases the agent.
func (a *Agent) endPrompt(final Update) {
	for id := range a.prompt.approvals {
		a.publish(Update{Type: "approval_cancelled", RequestID: id})
	}
	a.publish(final)
	a.prompt = nil
	a.phase = available
}

// publish sends an update about the current prompt to every subscriber.
func (a *Agent) publish(update Update) {
	if a.prompt == nil {
		return
	}
	update.MessageID = a.prompt.id
	for s := range a.subscribers {
		if !s.Publish(update) {
			delete(a.subscribers, s)
		}
	}
}

func newID() string {
	var id [16]byte
	rand.Read(id[:])
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}
