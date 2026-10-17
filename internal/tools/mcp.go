package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ox/internal/mcpclient"
	"ox/internal/transcript"
)

func (t *Toolbox) mcp(ctx context.Context, call transcript.ToolCall) transcript.ToolOutcome {
	if ctx.Err() != nil {
		return transcript.Cancelled("Cancelled before this MCP tool was started.")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(call.Arguments), &object); err != nil || object == nil {
		return transcript.Failed("MCP arguments must be a JSON object.")
	}
	result, err := t.MCP.Call(ctx, call.Name, json.RawMessage(call.Arguments))
	if ctx.Err() != nil {
		return transcript.Cancelled("MCP call cancelled; remote effects may have occurred.")
	}
	if err != nil {
		return transcript.Failed(boundMCP(err.Error()))
	}
	return mcpOutcome(result)
}

func boundMCP(text string) string {
	text = strings.ToValidUTF8(text, "�")
	if len(text) > outputLimit {
		return truncate(text, bodyLimit) + "\nMCP result truncated."
	}
	return text
}

func mcpOutcome(result mcpclient.Result) transcript.ToolOutcome {
	var blocks []string
	identical := false
	for _, raw := range result.Content {
		var block struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Resource *struct {
				Text *string `json:"text"`
				Blob *string `json:"blob"`
			} `json:"resource"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			return transcript.Failed(boundMCP("Invalid MCP content: " + err.Error()))
		}
		if block.Type == "" {
			return transcript.Failed("Invalid MCP content: missing type.")
		}
		switch block.Type {
		case "text":
			blocks = append(blocks, block.Text)
			identical = identical || block.Text == string(result.Structured)
		case "resource":
			if block.Resource != nil && block.Resource.Text != nil {
				blocks = append(blocks, *block.Resource.Text)
			}
			if block.Resource == nil || block.Resource.Text == nil || block.Resource.Blob != nil {
				blocks = append(blocks, "[Binary or unsupported embedded resource omitted.]")
			}
		case "resource_link":
			blocks = append(blocks, string(raw))
		case "image", "audio":
			blocks = append(blocks, fmt.Sprintf("[MCP %s content omitted.]", block.Type))
		default:
			blocks = append(blocks, "[Unsupported MCP content omitted.]")
		}
	}
	if len(result.Structured) > 0 && !identical {
		blocks = append(blocks, string(result.Structured))
	}
	text := boundMCP(strings.Join(blocks, "\n"))
	if result.IsError {
		return transcript.Failed(text)
	}
	return transcript.Completed(text)
}
