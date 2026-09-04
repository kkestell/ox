// Package openrouter implements Ox's OpenRouter chat and model-catalog client.
package openrouter

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Request struct {
	Model        string        `json:"model"`
	Messages     []Message     `json:"messages"`
	Tools        []Tool        `json:"tools,omitempty"`
	ToolChoice   string        `json:"tool_choice,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
	SessionID    string        `json:"session_id,omitempty"`
	MaxTokens    *int          `json:"max_tokens,omitempty"`
	Temperature  *float64      `json:"temperature,omitempty"`
	Reasoning    *Reasoning    `json:"reasoning,omitempty"`
	Provider     *Provider     `json:"provider,omitempty"`
}

type Message struct {
	Role             Role              `json:"role"`
	Content          []ContentBlock    `json:"content,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ReasoningDetails []json.RawMessage `json:"reasoning_details,omitempty"`
}

type ContentBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	ImageURL     string        `json:"image_url,omitempty"`
	AudioData    string        `json:"audio_data,omitempty"`
	AudioFormat  string        `json:"audio_format,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// MarshalJSON emits OpenRouter's nested shape for multimodal content.
func (c ContentBlock) MarshalJSON() ([]byte, error) {
	switch c.Type {
	case "image_url":
		return json.Marshal(struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
			CacheControl *CacheControl `json:"cache_control,omitempty"`
		}{Type: c.Type, ImageURL: struct {
			URL string `json:"url"`
		}{URL: c.ImageURL}, CacheControl: c.CacheControl})
	case "input_audio":
		return json.Marshal(struct {
			Type       string `json:"type"`
			InputAudio struct {
				Data   string `json:"data"`
				Format string `json:"format"`
			} `json:"input_audio"`
			CacheControl *CacheControl `json:"cache_control,omitempty"`
		}{Type: c.Type, InputAudio: struct {
			Data   string `json:"data"`
			Format string `json:"format"`
		}{Data: c.AudioData, Format: c.AudioFormat}, CacheControl: c.CacheControl})
	default:
		type raw ContentBlock
		return json.Marshal(raw(c))
	}
}

type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Reasoning struct {
	Effort  string `json:"effort,omitempty"`
	Exclude *bool  `json:"exclude,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type Provider struct {
	Order             []string  `json:"order,omitempty"`
	Only              []string  `json:"only,omitempty"`
	Ignore            []string  `json:"ignore,omitempty"`
	Quantizations     []string  `json:"quantizations,omitempty"`
	Sort              string    `json:"sort,omitempty"`
	DataCollection    string    `json:"data_collection,omitempty"`
	AllowFallbacks    *bool     `json:"allow_fallbacks,omitempty"`
	RequireParameters *bool     `json:"require_parameters,omitempty"`
	MaxPrice          *MaxPrice `json:"max_price,omitempty"`
}

type MaxPrice struct {
	Prompt     *float64 `json:"prompt,omitempty"`
	Completion *float64 `json:"completion,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	Cost                    float64                  `json:"cost"`
	CostDetails             *CostDetails             `json:"cost_details,omitempty"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

type CostDetails struct {
	UpstreamInferenceCost float64 `json:"upstream_inference_cost"`
}

type PromptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type Completion struct {
	ID               string
	Model            string
	Provider         string
	Text             string
	Reasoning        string
	ReasoningDetails []json.RawMessage
	ToolCalls        []ToolCall
	FinishReason     string
	Usage            *Usage
}

type DeltaKind string

const (
	DeltaText      DeltaKind = "text"
	DeltaReasoning DeltaKind = "reasoning"
)

type Delta struct {
	Kind DeltaKind
	Text string
}
