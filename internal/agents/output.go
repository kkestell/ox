package agents

import (
	"encoding/json"
	"strings"
)

type claudeOutput struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	State           string          `json:"state"`
	CommandUUID     string          `json:"command_uuid"`
	ParentToolUseID string          `json:"parent_tool_use_id"`
	Result          string          `json:"result"`
	IsError         bool            `json:"is_error"`
	Errors          []string        `json:"errors"`
	RequestID       string          `json:"request_id"`
	Request         json.RawMessage `json:"request"`
	Response        controlResponse `json:"response"`
	Event           struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
}

func (p *process) handleOutput(message claudeOutput) {
	switch message.Type {
	case "control_response":
		p.controlResponse(message.Response)
	case "control_request":
		p.controlRequest(message.RequestID, message.Request)
	case "control_cancel_request":
		p.cancelApproval(message.RequestID)
	case "command_lifecycle":
		if message.State == "started" {
			p.commandStarted(message.CommandUUID)
		}
	case "system":
		if message.Subtype == "session_state_changed" {
			switch message.State {
			case "running", "requires_action", "idle":
				p.sessionState(message.State)
			}
		}
	case "stream_event":
		delta := message.Event.Delta
		if message.ParentToolUseID == "" && message.Event.Type == "content_block_delta" && delta.Type == "text_delta" && delta.Text != "" {
			p.publish(Update{Type: "text_delta", Text: delta.Text})
		}
	case "result":
		result := Result{Text: message.Result, IsError: message.IsError || message.Subtype != "success"}
		if result.IsError && result.Text == "" {
			result.Text = strings.Join(message.Errors, "\n")
			if result.Text == "" {
				result.Text = message.Subtype
			}
		}
		p.publish(Update{Type: "result", Result: &result})
	}
}
