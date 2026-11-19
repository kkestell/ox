package claude

import (
	"encoding/json"
	"strings"
)

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

type ToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolResult struct {
	ToolUseID string
	Text      string
	IsError   bool
}

func (m Message) ToolUses() []ToolUse {
	var blocks []struct {
		Type string `json:"type"`
		ToolUse
	}
	json.Unmarshal(m.Message.Content, &blocks)
	var uses []ToolUse
	for _, b := range blocks {
		if b.Type == "tool_use" {
			uses = append(uses, b.ToolUse)
		}
	}
	return uses
}

func (m Message) ToolResults() []ToolResult {
	var blocks []struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"` // a string or an array of blocks
		IsError   bool            `json:"is_error"`
	}
	json.Unmarshal(m.Message.Content, &blocks)
	var results []ToolResult
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		result := ToolResult{ToolUseID: b.ToolUseID, IsError: b.IsError}
		if json.Unmarshal(b.Content, &result.Text) != nil {
			var content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			json.Unmarshal(b.Content, &content)
			var texts []string
			for _, c := range content {
				if c.Type == "text" {
					texts = append(texts, c.Text)
				}
			}
			result.Text = strings.Join(texts, "\n")
		}
		results = append(results, result)
	}
	return results
}
