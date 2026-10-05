package openrouter

import (
	"encoding/json"

	"ox/internal/catalog"
	"ox/internal/transcript"
)

// Request is one model request: the model, effort, system prompt, tool
// definitions, and the saved transcript, whose request entries are sent.
type Request struct {
	Model        *catalog.Model
	Effort       catalog.Effort
	SystemPrompt string
	Tools        []json.RawMessage
	Transcript   []transcript.Entry
}

// body is the chat-completions request body.
func (r Request) body() map[string]any {
	messages := append([]any{map[string]any{"role": "system", "content": r.SystemPrompt}}, chatMessages(r.Transcript)...)
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

// chatMessages encodes the transcript's request entries as chat messages.
// Visible reasoning is sent only when no continuation metadata carries it.
func chatMessages(entries []transcript.Entry) []any {
	var messages []any
	for _, entry := range transcript.RequestEntries(entries) {
		switch entry := entry.(type) {
		case *transcript.TurnStart:
			messages = append(messages, map[string]any{"role": "user", "content": userContent(entry.Input.ModelMessage())})
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
			if len(message.ContinuationMetadata) > 0 {
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
