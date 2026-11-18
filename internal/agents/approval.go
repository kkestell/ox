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

func (p *process) controlRequest(requestID string, request json.RawMessage) {
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
	if p.prompt == nil {
		p.denyUnowned(requestID)
		return
	}
	p.prompt.approvals[requestID] = struct{}{}
	p.prompt.publish(Update{Type: "approval", RequestID: requestID, Request: request})
}

func (p *process) answer(clientID, requestID string, response json.RawMessage) error {
	if p.prompt == nil {
		return ErrApprovalNotFound
	}
	if _, pending := p.prompt.approvals[requestID]; !pending {
		return ErrApprovalNotFound
	}
	if p.prompt.clientID != clientID {
		return ErrApprovalOwner
	}
	delete(p.prompt.approvals, requestID)
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

// denyUnowned answers an approval from a turn Claude started without a prompt.
func (p *process) denyUnowned(requestID string) {
	err := p.respond(requestID, map[string]any{
		"behavior": "deny", "message": "No prompt owns this approval", "interrupt": true,
	})
	if err != nil {
		log.Printf("deny unowned approval: %v", err)
	}
}

func (p *process) cancelApproval(requestID string) {
	if p.prompt == nil {
		return
	}
	if _, pending := p.prompt.approvals[requestID]; pending {
		delete(p.prompt.approvals, requestID)
		p.prompt.publish(Update{Type: "approval_cancelled", RequestID: requestID})
	}
}

func (s *submission) takeApprovals() []string {
	ids := make([]string, 0, len(s.approvals))
	for id := range s.approvals {
		ids = append(ids, id)
	}
	clear(s.approvals)
	return ids
}
