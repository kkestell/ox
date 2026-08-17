// Package openrouter implements Ox's OpenRouter chat completion client.
package openrouter

import "encoding/json"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Request is one chat completion. Ox always streams, so Stream is set by
// Client.Stream rather than by its caller.
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	AudioData   string `json:"audio_data,omitempty"`
	AudioFormat string `json:"audio_format,omitempty"`
}

// MarshalJSON emits OpenRouter's nested shape for each content-part variant.
func (c ContentBlock) MarshalJSON() ([]byte, error) {
	switch c.Type {
	case "text":
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: c.Type, Text: c.Text})
	case "image_url":
		return json.Marshal(struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}{Type: c.Type, ImageURL: struct {
			URL string `json:"url"`
		}{URL: c.ImageURL}})
	case "input_audio":
		return json.Marshal(struct {
			Type       string `json:"type"`
			InputAudio struct {
				Data   string `json:"data"`
				Format string `json:"format"`
			} `json:"input_audio"`
		}{Type: c.Type, InputAudio: struct {
			Data   string `json:"data"`
			Format string `json:"format"`
		}{Data: c.AudioData, Format: c.AudioFormat}})
	default:
		type raw ContentBlock
		return json.Marshal(raw(c))
	}
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Completion is a streamed response assembled back into one value. Usage is nil
// unless the provider sent a usage chunk.
type Completion struct {
	Text         string
	Reasoning    string
	FinishReason string
	Usage        *Usage
}

type DeltaKind string

const (
	DeltaText      DeltaKind = "text"
	DeltaReasoning DeltaKind = "reasoning"
)

// Delta is one fragment of a streamed completion.
type Delta struct {
	Kind DeltaKind
	Text string
}
