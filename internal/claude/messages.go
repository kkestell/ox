package claude

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

type message struct {
	Type            string            `json:"type"`
	SessionID       string            `json:"session_id"`
	Subtype         string            `json:"subtype"`
	State           string            `json:"state"`
	PermissionMode  string            `json:"permissionMode"` // system status and init lines
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
	RateLimitInfo   struct {
		UnifiedWindows map[string]struct {
			Utilization float64 `json:"utilization"` // a fraction
			ResetsAt    int64   `json:"resetsAt"`    // Unix seconds
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
	Message struct {
		Content json.RawMessage `json:"content"` // a string or an array of blocks
	} `json:"message"`
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	} `json:"event"`
}

func (m message) failed() bool {
	return m.IsError || m.Subtype != "success"
}

// errorText is a failed result's error: its text, else its errors, else its
// subtype.
func (m message) errorText() string {
	if m.Result != "" {
		return m.Result
	}
	if len(m.Errors) > 0 {
		return strings.Join(m.Errors, "\n")
	}
	return m.Subtype
}

// windowSeconds is the length of each rate_limit_event window Claude Code
// names.
var windowSeconds = map[string]int64{"five_hour": 5 * 3600, "seven_day": 7 * 24 * 3600}

// usageWindows is a rate_limit_event's windows whose length is known.
func (m message) usageWindows() []agent.UsageWindow {
	var windows []agent.UsageWindow
	for key, w := range m.RateLimitInfo.UnifiedWindows {
		if seconds, ok := windowSeconds[key]; ok {
			windows = append(windows, agent.UsageWindow{IntervalSeconds: seconds, UsedPercent: w.Utilization * 100, ResetsAt: time.Unix(w.ResetsAt, 0)})
		}
	}
	return windows
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
