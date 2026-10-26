package openrouter

import (
	"ox/internal/catalog"
	"ox/internal/provider"
	"ox/internal/transcript"
)

// body is the chat-completions request body.
func body(r provider.Request) map[string]any {
	messages := append([]any{map[string]any{"role": "system", "content": r.SystemPrompt}}, chatMessages(r.Transcript, r.Model.QualifiedID())...)
	body := map[string]any{
		"model":    r.Model.ID,
		"messages": messages,
		"tools":    r.Tools,
		"stream":   true,
		"usage":    map[string]any{"include": true},
	}
	if r.Effort != catalog.EffortDefault {
		body["reasoning"] = map[string]any{"effort": r.Effort}
	}
	// Without fallbacks, OpenRouter fails a pinned request instead of trying
	// another provider.
	if len(r.Model.Providers) > 0 {
		body["provider"] = map[string]any{"order": r.Model.Providers, "allow_fallbacks": false}
	}
	return body
}

// chatMessages encodes the transcript's request entries as chat messages for
// model, the qualified ID of the requested model. Continuation metadata is
// sent only for turns that used model, since another model cannot read it.
// Visible reasoning is sent only when no continuation metadata carries it.
func chatMessages(entries []transcript.Entry, model string) []any {
	var messages []any
	var turnModel string
	for _, entry := range transcript.RequestEntries(entries) {
		switch entry := entry.(type) {
		case *transcript.TurnStart:
			turnModel = entry.Model
			messages = append(messages, map[string]any{"role": "user", "content": userContent(entry.Input.ModelMessage())})
			if len(entry.Context) > 0 {
				messages = append(messages, map[string]any{"role": "user", "content": transcript.ContextMessage(entry.Context)})
			}
		case *transcript.Compaction:
			messages = append(messages, map[string]any{"role": "user", "content": userContent(entry.Message())})
		case *transcript.AssistantBatch:
			message := entry.Message
			assistant := map[string]any{"role": "assistant", "content": nil}
			if message.Text != "" {
				assistant["content"] = message.Text
			}
			if len(message.ToolCalls) > 0 {
				calls := make([]any, len(message.ToolCalls))
				for i, call := range message.ToolCalls {
					calls[i] = map[string]any{
						"id":       call.CallID,
						"type":     "function",
						"function": map[string]any{"name": call.Name, "arguments": call.Arguments},
					}
				}
				assistant["tool_calls"] = calls
			}
			if len(message.ContinuationMetadata) > 0 && turnModel == model {
				assistant["reasoning_details"] = message.ContinuationMetadata
			} else if message.Reasoning != "" {
				assistant["reasoning"] = message.Reasoning
			}
			messages = append(messages, assistant)
			for i, call := range message.ToolCalls {
				messages = append(messages, map[string]any{
					"role":         "tool",
					"tool_call_id": call.CallID,
					"content":      entry.Outcomes[i].Text,
				})
			}
			if len(entry.Context) > 0 {
				messages = append(messages, map[string]any{"role": "user", "content": transcript.ContextMessage(entry.Context)})
			}
		}
	}
	return messages
}

func userContent(message transcript.UserMessage) any {
	if !message.HasImages() {
		return message.Text()
	}
	parts := make([]any, len(message.Parts))
	for i, part := range message.Parts {
		if part.Image != nil {
			parts[i] = map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": "data:" + part.Image.MimeType + ";base64," + part.Image.Data},
			}
		} else {
			parts[i] = map[string]any{"type": "text", "text": part.Text}
		}
	}
	return parts
}
