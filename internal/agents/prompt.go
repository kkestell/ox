package agents

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

// submission is the prompt that owns the process in the queued and
// prompting phases.
type submission struct {
	id        string
	notify    func(Update)
	clientID  string
	approvals map[string]struct{}
}

func (s *submission) publish(update Update) {
	update.MessageID = s.id
	s.notify(update)
}

func (p *process) submit(clientID, text string, notify func(Update)) (string, error) {
	if !p.idle() {
		return "", ErrBusy
	}
	prompt := &submission{
		id: promptID(), notify: notify, clientID: clientID,
		approvals: make(map[string]struct{}),
	}
	input := map[string]any{
		"type": "user", "uuid": prompt.id, "session_id": "",
		"message": map[string]string{"role": "user", "content": text},
	}
	if err := p.write(input); err != nil {
		return "", fmt.Errorf("send prompt to Claude: %w", err)
	}
	p.prompt = prompt
	p.phase = queued
	return prompt.id, nil
}

func (p *process) commandStarted(id string) {
	if p.phase == queued && p.prompt.id == id {
		p.phase = prompting
	}
}

func (p *process) sessionState(state string) {
	p.claudeState = state
	switch {
	case p.prompt == nil:
	case state != "idle":
		p.prompt.publish(Update{Type: "state", State: state})
	case p.phase == queued:
		// Claude can report idle before it takes the prompt; keep waiting.
	case p.phase == prompting:
		p.endPrompt(Update{Type: "state", State: state})
	}
}

// endPrompt withdraws the prompt's unanswered approvals, sends its final
// update, and releases the process.
func (p *process) endPrompt(final Update) {
	for _, id := range p.prompt.takeApprovals() {
		p.prompt.publish(Update{Type: "approval_cancelled", RequestID: id})
	}
	p.prompt.publish(final)
	p.prompt = nil
	p.phase = available
}

func (p *process) publish(update Update) {
	if p.prompt != nil {
		p.prompt.publish(update)
	}
}

func promptID() string {
	var id [16]byte
	rand.Read(id[:])
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}
