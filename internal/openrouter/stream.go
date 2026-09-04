package openrouter

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type streamChunk struct {
	ID       string         `json:"id"`
	Model    string         `json:"model"`
	Provider string         `json:"provider"`
	Choices  []streamChoice `json:"choices"`
	Usage    *Usage         `json:"usage"`
	Error    *apiError      `json:"error"`
}

type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type streamDelta struct {
	Content          string             `json:"content"`
	Reasoning        string             `json:"reasoning"`
	ReasoningDetails []json.RawMessage  `json:"reasoning_details"`
	ToolCalls        []toolCallFragment `json:"tool_calls"`
}

type toolCallFragment struct {
	Index    int                      `json:"index"`
	ID       string                   `json:"id"`
	Type     string                   `json:"type"`
	Function toolCallFunctionFragment `json:"function"`
}

type toolCallFunctionFragment struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type apiError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Meta    json.RawMessage `json:"metadata"`
}

type streamAPIError struct {
	code    int
	message string
}

func (e *streamAPIError) Error() string {
	if e.code == 0 {
		return fmt.Sprintf("OpenRouter streaming error: %s", e.message)
	}
	return fmt.Sprintf("OpenRouter streaming error %d: %s", e.code, e.message)
}

type partialToolCall struct {
	id        string
	kind      string
	name      string
	arguments strings.Builder
}

type streamAssembler struct {
	completion            Completion
	text                  strings.Builder
	reasoning             strings.Builder
	toolCalls             map[int]*partialToolCall
	reasoningDetailBlocks map[reasoningDetailKey]int
	sawChoice             bool
}

type reasoningDetailKey struct {
	kind  string
	index int
}

type reasoningDetailHeader struct {
	Type  string `json:"type"`
	Index *int   `json:"index"`
}

func (a *streamAssembler) push(data []byte, onDelta func(Delta)) error {
	var chunk streamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return fmt.Errorf("decode OpenRouter stream event: %w", err)
	}
	if chunk.Error != nil {
		return &streamAPIError{code: chunk.Error.Code, message: chunk.Error.Message}
	}

	if chunk.ID != "" {
		a.completion.ID = chunk.ID
	}
	if chunk.Model != "" {
		a.completion.Model = chunk.Model
	}
	if chunk.Provider != "" {
		a.completion.Provider = chunk.Provider
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
		for _, detail := range choice.Delta.ReasoningDetails {
			if err := a.appendReasoningDetail(detail); err != nil {
				return err
			}
		}
		for _, fragment := range choice.Delta.ToolCalls {
			call := a.toolCall(fragment.Index)
			if fragment.ID != "" {
				call.id = fragment.ID
			}
			if fragment.Type != "" {
				call.kind = fragment.Type
			}
			if fragment.Function.Name != "" {
				call.name = fragment.Function.Name
			}
			call.arguments.WriteString(fragment.Function.Arguments)
		}
		if choice.FinishReason != "" {
			a.completion.FinishReason = choice.FinishReason
		}
	}
	return nil
}

func (a *streamAssembler) appendReasoningDetail(detail json.RawMessage) error {
	var header reasoningDetailHeader
	if err := json.Unmarshal(detail, &header); err != nil {
		return fmt.Errorf("decode OpenRouter reasoning detail: %w", err)
	}
	if header.Type == "" || header.Index == nil {
		a.completion.ReasoningDetails = append(a.completion.ReasoningDetails, detail)
		return nil
	}

	key := reasoningDetailKey{kind: header.Type, index: *header.Index}
	if a.reasoningDetailBlocks == nil {
		a.reasoningDetailBlocks = make(map[reasoningDetailKey]int)
	}
	position, ok := a.reasoningDetailBlocks[key]
	if !ok {
		a.reasoningDetailBlocks[key] = len(a.completion.ReasoningDetails)
		a.completion.ReasoningDetails = append(a.completion.ReasoningDetails, detail)
		return nil
	}

	merged, err := mergeReasoningDetail(
		a.completion.ReasoningDetails[position],
		detail,
		header.Type,
	)
	if err != nil {
		return err
	}
	a.completion.ReasoningDetails[position] = merged
	return nil
}

func mergeReasoningDetail(
	current json.RawMessage,
	fragment json.RawMessage,
	kind string,
) (json.RawMessage, error) {
	var currentFields, fragmentFields map[string]json.RawMessage
	if err := json.Unmarshal(current, &currentFields); err != nil {
		return nil, fmt.Errorf("decode assembled OpenRouter reasoning detail: %w", err)
	}
	if err := json.Unmarshal(fragment, &fragmentFields); err != nil {
		return nil, fmt.Errorf("decode OpenRouter reasoning detail fragment: %w", err)
	}

	contentField := ""
	switch kind {
	case "reasoning.text":
		contentField = "text"
	case "reasoning.summary":
		contentField = "summary"
	case "reasoning.encrypted":
		contentField = "data"
	}
	var currentContent, fragmentContent string
	if contentField != "" {
		_ = json.Unmarshal(currentFields[contentField], &currentContent)
		_ = json.Unmarshal(fragmentFields[contentField], &fragmentContent)
	}
	for field, value := range fragmentFields {
		currentFields[field] = value
	}
	if currentContent != "" || fragmentContent != "" {
		combined, err := json.Marshal(currentContent + fragmentContent)
		if err != nil {
			return nil, fmt.Errorf("encode OpenRouter reasoning detail content: %w", err)
		}
		currentFields[contentField] = combined
	}
	merged, err := json.Marshal(currentFields)
	if err != nil {
		return nil, fmt.Errorf("encode assembled OpenRouter reasoning detail: %w", err)
	}
	return merged, nil
}

func (a *streamAssembler) finish() *Completion {
	a.completion.Text = a.text.String()
	a.completion.Reasoning = a.reasoning.String()

	indices := make([]int, 0, len(a.toolCalls))
	for index := range a.toolCalls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	a.completion.ToolCalls = make([]ToolCall, 0, len(indices))
	for _, index := range indices {
		call := a.toolCalls[index]
		kind := call.kind
		if kind == "" {
			kind = "function"
		}
		a.completion.ToolCalls = append(a.completion.ToolCalls, ToolCall{
			ID:   call.id,
			Type: kind,
			Function: ToolCallFunction{
				Name:      call.name,
				Arguments: call.arguments.String(),
			},
		})
	}
	return &a.completion
}

func (a *streamAssembler) toolCall(index int) *partialToolCall {
	if a.toolCalls == nil {
		a.toolCalls = make(map[int]*partialToolCall)
	}
	call := a.toolCalls[index]
	if call == nil {
		call = &partialToolCall{}
		a.toolCalls[index] = call
	}
	return call
}
