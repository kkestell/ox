package claude

import "encoding/json"

// Message is one line of Claude's output, reduced to the fields Ox reads.
type Message struct {
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
	Response        ControlResponse `json:"response"`
	Event           struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
}

type ControlResponse struct {
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Error     string `json:"error"`
}
