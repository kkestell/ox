// Package openrouter implements Ox's OpenRouter chat completion client.
package openrouter

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
	Type string `json:"type"`
	Text string `json:"text"`
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
