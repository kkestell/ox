package agents

import (
	"encoding/json"
	"errors"
	"log"
)

var (
	ErrApprovalNotFound = errors.New("approval request is no longer pending")
	ErrApprovalOwner    = errors.New("approval request belongs to another client")
)

func (p *process) requestApproval(requestID string, request json.RawMessage) {
	var control struct {
		Subtype string `json:"subtype"`
	}
	if json.Unmarshal(request, &control) != nil || control.Subtype != "can_use_tool" {
		err := p.write(map[string]any{
			"type": "control_response",
			"response": map[string]string{
				"subtype": "error", "request_id": requestID,
				"error": "Unsupported control request",
			},
		})
		if err != nil {
			log.Printf("reject Claude control request: %v", err)
		}
		return
	}

	p.mu.Lock()
	prompt := p.prompt
	if prompt == nil || prompt.disconnected {
		p.mu.Unlock()
		p.denyDisconnected(requestID)
		return
	}
	prompt.approvals[requestID] = struct{}{}
	p.mu.Unlock()
	prompt.publish(Update{Type: "approval", RequestID: requestID, Request: request})
}

func (p *process) approve(clientID, requestID string, response json.RawMessage) error {
	p.mu.Lock()
	prompt := p.prompt
	if prompt == nil || prompt.disconnected {
		p.mu.Unlock()
		return ErrApprovalNotFound
	}
	if _, pending := prompt.approvals[requestID]; !pending {
		p.mu.Unlock()
		return ErrApprovalNotFound
	}
	if prompt.clientID != clientID {
		p.mu.Unlock()
		return ErrApprovalOwner
	}
	delete(prompt.approvals, requestID)
	p.mu.Unlock()
	return p.respond(requestID, response)
}

func (p *process) respond(requestID string, response any) error {
	return p.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "success", "request_id": requestID, "response": response,
		},
	})
}

func (p *process) denyDisconnected(requestID string) {
	err := p.respond(requestID, map[string]any{
		"behavior": "deny", "message": "Approval client disconnected", "interrupt": true,
	})
	if err != nil {
		log.Printf("deny disconnected client's approval: %v", err)
	}
}

func (p *process) disconnect(clientID string) {
	p.mu.Lock()
	prompt := p.prompt
	if prompt == nil || prompt.clientID != clientID {
		p.mu.Unlock()
		return
	}
	prompt.disconnected = true
	ids := prompt.takeApprovals()
	p.mu.Unlock()
	for _, requestID := range ids {
		p.denyDisconnected(requestID)
	}
}

func (p *process) cancelApproval(requestID string) {
	p.mu.Lock()
	prompt := p.prompt
	if prompt == nil {
		p.mu.Unlock()
		return
	}
	_, pending := prompt.approvals[requestID]
	delete(prompt.approvals, requestID)
	p.mu.Unlock()
	if pending {
		prompt.publish(Update{Type: "approval_cancelled", RequestID: requestID})
	}
}

// takeApprovals is called with the process mutex held.
func (s *submission) takeApprovals() []string {
	if s == nil {
		return nil
	}
	ids := make([]string, 0, len(s.approvals))
	for id := range s.approvals {
		ids = append(ids, id)
	}
	clear(s.approvals)
	return ids
}

func (s *submission) publishCancellations(ids []string) {
	for _, id := range ids {
		s.publish(Update{Type: "approval_cancelled", RequestID: id})
	}
}
