package claude

import "encoding/json"

// Message is one line of Claude's output, reduced to the fields Ox reads.
type Message struct {
	Type            string            `json:"type"`
	Subtype         string            `json:"subtype"`
	State           string            `json:"state"`
	CommandUUID     string            `json:"command_uuid"`
	ParentToolUseID string            `json:"parent_tool_use_id"`
	Result          string            `json:"result"`
	IsError         bool              `json:"is_error"`
	Errors          []string          `json:"errors"`
	RequestID       string            `json:"request_id"`
	Request         json.RawMessage   `json:"request"`
	Response        ControlResponse   `json:"response"`
	Tasks           []json.RawMessage `json:"tasks"` // background_tasks_changed: the live set
	Message         struct {
		Content json.RawMessage `json:"content"` // a string or an array of blocks
	} `json:"message"`
	Event struct {
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

// ToolNames returns the names of the tool_use blocks in an assistant message.
func (m Message) ToolNames() []string {
	var blocks []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	json.Unmarshal(m.Message.Content, &blocks)
	var names []string
	for _, b := range blocks {
		if b.Type == "tool_use" {
			names = append(names, b.Name)
		}
	}
	return names
}
