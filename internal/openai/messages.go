package openai

import (
	"encoding/json"

	"ox/internal/catalog"
	"ox/internal/provider"
	"ox/internal/transcript"
)

// body is the Responses request body. The Codex backend keeps no state, so
// every request sends the whole transcript.
func body(r provider.Request) map[string]any {
	reasoning := map[string]any{"summary": "auto"}
	if r.Effort != catalog.EffortDefault {
		reasoning["effort"] = r.Effort
	}
	return map[string]any{
		"model":               r.Model.ID,
		"instructions":        r.SystemPrompt,
		"input":               inputItems(r.Transcript, r.Model.QualifiedID()),
		"tools":               functionTools(r.Tools),
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"store":               false,
		"stream":              true,
		"include":             []string{"reasoning.encrypted_content"},
		"reasoning":           reasoning,
		"prompt_cache_key":    r.SessionID,
	}
}

// functionTools converts chat-completions tool definitions to Responses
// function tools.
func functionTools(schemas []json.RawMessage) []any {
	tools := []any{}
	for _, raw := range schemas {
		var definition struct {
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"function"`
		}
		if err := json.Unmarshal(raw, &definition); err != nil {
			panic(err)
		}
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        definition.Function.Name,
			"description": definition.Function.Description,
			"parameters":  definition.Function.Parameters,
			"strict":      false,
		})
	}
	return tools
}

// inputItems encodes the transcript's request entries as Responses input
// items for model, the qualified ID of the requested model. Saved reasoning
// items are sent only for turns that used model, since another model cannot
// read them.
func inputItems(entries []transcript.Entry, model string) []any {
	items := []any{}
	var turnModel string
	for _, entry := range transcript.RequestEntries(entries) {
		switch entry := entry.(type) {
		case *transcript.TurnStart:
			turnModel = entry.Model
			items = append(items, userItem(entry.Input.ModelMessage()))
			if len(entry.HookContext) > 0 {
				items = append(items, userItem(transcript.TextMessage(transcript.HookMessage(entry.HookContext))))
			}
		case *transcript.Compaction:
			items = append(items, userItem(entry.Message()))
		case *transcript.AssistantBatch:
			message := entry.Message
			if turnModel == model {
				for _, item := range message.ContinuationMetadata {
					items = append(items, item)
				}
			}
			if message.Text != "" {
				items = append(items, map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": message.Text}},
				})
			}
			for _, call := range message.ToolCalls {
				items = append(items, map[string]any{"type": "function_call", "call_id": call.CallID, "name": call.Name, "arguments": call.Arguments})
			}
			for i, call := range message.ToolCalls {
				items = append(items, map[string]any{"type": "function_call_output", "call_id": call.CallID, "output": entry.Outcomes[i].Text})
			}
			if len(entry.HookContext) > 0 {
				items = append(items, userItem(transcript.TextMessage(transcript.HookMessage(entry.HookContext))))
			}
		}
	}
	return items
}

func userItem(message transcript.UserMessage) map[string]any {
	content := make([]any, len(message.Parts))
	for i, part := range message.Parts {
		if part.Image != nil {
			content[i] = map[string]any{"type": "input_image", "image_url": "data:" + part.Image.MimeType + ";base64," + part.Image.Data}
		} else {
			content[i] = map[string]any{"type": "input_text", "text": part.Text}
		}
	}
	return map[string]any{"type": "message", "role": "user", "content": content}
}
