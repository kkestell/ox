package claude

import (
	"encoding/json"
	"strings"
)

type message struct {
	Type            string            `json:"type"`
	Subtype         string            `json:"subtype"`
	State           string            `json:"state"`
	CommandUUID     string            `json:"command_uuid"`
	ParentToolUseID string            `json:"parent_tool_use_id"`
	Result          string            `json:"result"`
	IsError         bool              `json:"is_error"`
	TerminalReason  string            `json:"terminal_reason"`
	Errors          []string          `json:"errors"`
	RequestID       string            `json:"request_id"`
	Request         json.RawMessage   `json:"request"`
	Response        controlReply      `json:"response"`
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

type controlReply struct {
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Error     string `json:"error"`
}

type toolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type toolResult struct {
	ToolUseID string
	Text      string
	IsError   bool
}

func (m message) toolUses() []toolUse {
	var blocks []struct {
		Type string `json:"type"`
		toolUse
	}
	json.Unmarshal(m.Message.Content, &blocks)
	var uses []toolUse
	for _, b := range blocks {
		if b.Type == "tool_use" {
			uses = append(uses, b.toolUse)
		}
	}
	return uses
}

func (m message) toolResults() []toolResult {
	var blocks []struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"` // a string or an array of blocks
		IsError   bool            `json:"is_error"`
	}
	json.Unmarshal(m.Message.Content, &blocks)
	var results []toolResult
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		result := toolResult{ToolUseID: b.ToolUseID, IsError: b.IsError}
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
