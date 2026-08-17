package openrouter

import (
	"encoding/json"
	"fmt"
	"strings"
)

type streamChunk struct {
	Choices []streamChoice `json:"choices"`
	Usage   *Usage         `json:"usage"`
	Error   *apiError      `json:"error"`
}

type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type streamDelta struct {
	Content   string `json:"content"`
	Reasoning string `json:"reasoning"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// streamAssembler accumulates the chunks of one streamed completion, emitting
// each fragment as it arrives. sawChoice records whether the stream carried any
// choice at all, which is what tells an empty answer apart from a body that
// never contained one.
type streamAssembler struct {
	completion Completion
	text       strings.Builder
	reasoning  strings.Builder
	sawChoice  bool
}

func (a *streamAssembler) push(data []byte, onDelta func(Delta)) error {
	var chunk streamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return fmt.Errorf("decode OpenRouter stream event: %w", err)
	}
	if chunk.Error != nil {
		if chunk.Error.Code == 0 {
			return fmt.Errorf("OpenRouter streaming error: %s", chunk.Error.Message)
		}
		return fmt.Errorf(
			"OpenRouter streaming error %d: %s",
			chunk.Error.Code,
			chunk.Error.Message,
		)
	}
	if chunk.Usage != nil {
		a.completion.Usage = chunk.Usage
	}

	for _, choice := range chunk.Choices {
		a.sawChoice = true
		if choice.Delta.Content != "" {
			a.text.WriteString(choice.Delta.Content)
			if onDelta != nil {
				onDelta(Delta{Kind: DeltaText, Text: choice.Delta.Content})
			}
		}
		if choice.Delta.Reasoning != "" {
			a.reasoning.WriteString(choice.Delta.Reasoning)
			if onDelta != nil {
				onDelta(Delta{Kind: DeltaReasoning, Text: choice.Delta.Reasoning})
			}
		}
		if choice.FinishReason != "" {
			a.completion.FinishReason = choice.FinishReason
		}
	}
	return nil
}

func (a *streamAssembler) finish() Completion {
	a.completion.Text = a.text.String()
	a.completion.Reasoning = a.reasoning.String()
	return a.completion
}
