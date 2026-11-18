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

type submission struct {
	id           string
	notify       func(Update)
	started      bool // Claude took this prompt into its conversation.
	clientID     string
	disconnected bool
	approvals    map[string]struct{}
}

func (s *submission) publish(update Update) {
	if s != nil {
		update.MessageID = s.id
		s.notify(update)
	}
}

func (p *process) send(clientID, text string, notify func(Update)) (string, error) {
	p.mu.Lock()
	if p.state == "exited" {
		p.mu.Unlock()
		return "", ErrNotFound
	}
	if p.state != "idle" || p.prompt != nil || p.configuring {
		p.mu.Unlock()
		return "", ErrBusy
	}
	prompt := &submission{
		id: promptID(), notify: notify, clientID: clientID,
		approvals: make(map[string]struct{}),
	}
	p.prompt = prompt
	p.mu.Unlock()

	input := map[string]any{
		"type": "user", "uuid": prompt.id, "session_id": "",
		"message": map[string]string{"role": "user", "content": text},
	}
	if err := p.write(input); err != nil {
		p.mu.Lock()
		if p.prompt == prompt {
			p.prompt = nil
		}
		p.mu.Unlock()
		return "", fmt.Errorf("send prompt to Claude: %w", err)
	}
	return prompt.id, nil
}

func (p *process) noteStart(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.prompt != nil && p.prompt.id == id {
		p.prompt.started = true
	}
}

func (p *process) setState(state string) {
	p.mu.Lock()
	p.state = state
	prompt := p.prompt
	var cancelled []string
	if state == "idle" && prompt != nil {
		if !prompt.started {
			// Only release this submission after Claude has taken the prompt.
			p.mu.Unlock()
			return
		}
		p.prompt = nil
		cancelled = prompt.takeApprovals()
	}
	p.mu.Unlock()
	prompt.publishCancellations(cancelled)
	prompt.publish(Update{Type: "state", State: state})
}

func (p *process) publish(update Update) {
	p.mu.Lock()
	prompt := p.prompt
	p.mu.Unlock()
	prompt.publish(update)
}

func (p *process) finish(err error) {
	p.mu.Lock()
	p.state = "exited"
	prompt := p.prompt
	cancelled := prompt.takeApprovals()
	p.prompt = nil
	p.mu.Unlock()

	reason := "Claude exited"
	if err != nil {
		reason += ": " + err.Error()
	}
	prompt.publishCancellations(cancelled)
	prompt.publish(Update{Type: "exited", Error: reason})
}

func promptID() string {
	var id [16]byte
	rand.Read(id[:])
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}
