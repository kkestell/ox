package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"
	"unicode/utf8"

	"ox/internal/transcript"
)

// Stop is why a completion ended.
type Stop int

const (
	StopFinished Stop = iota
	StopToolCalls
	StopTokenLimit
	StopRefused
)

// Completion is one validated assistant message and why it ended.
type Completion struct {
	Message transcript.AssistantMessage
	Stop    Stop
}

// Item is one stream item: a text delta, a reasoning delta, or the completion.
type Item struct {
	Text       string
	Reasoning  string
	Completion *Completion
}

// Stream is one streamed response. Closing it stops reading and discards text
// received before a complete message was validated.
type Stream struct {
	ctx          context.Context
	cancel       context.CancelCauseFunc
	timer        *time.Timer
	stallTimeout time.Duration
	body         io.Closer
	reader       *bufio.Reader
	data         []byte
	assembly     *assembly
	finished     bool
	items        []Item
	// usage arrives once, in the final chunk.
	usage *transcript.Usage
}

// Close releases the request.
func (s *Stream) Close() {
	s.timer.Stop()
	s.cancel(nil)
	if s.body != nil {
		s.body.Close()
	}
}

// Next returns the next delta or the one validated completion. A stream that
// ends before its completion is an error.
func (s *Stream) Next() (Item, error) {
	for len(s.items) == 0 {
		more, err := s.readLine()
		if err != nil {
			return Item{}, err
		}
		if !more {
			return Item{}, errors.New("OpenRouter stream ended before the response finished")
		}
	}
	if s.items[0].Completion != nil {
		// The finish chunk may be followed by a usage chunk and `[DONE]`. Read
		// them first, so the completion carries the usage and the connection
		// returns to the pool.
		for {
			more, err := s.readLine()
			if err != nil {
				return Item{}, err
			}
			if !more {
				break
			}
		}
		s.items[0].Completion.Message.Usage = s.usage
	}
	item := s.items[0]
	s.items = s.items[1:]
	return item, nil
}

// readLine reads one SSE line into stream items; false at the end of the body.
func (s *Stream) readLine() (bool, error) {
	line, err := s.reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, s.readFailure(err)
	}
	if len(line) == 0 {
		return false, nil
	}
	if !utf8.Valid(line) {
		return false, errors.New("OpenRouter stream is not UTF-8")
	}
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 {
		if len(s.data) > 0 {
			data := s.data
			s.data = nil
			if err := s.event(data); err != nil {
				return false, err
			}
		}
	} else if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
		s.timer.Reset(s.stallTimeout)
		if len(s.data) > 0 {
			s.data = append(s.data, '\n')
		}
		s.data = append(s.data, bytes.TrimPrefix(data, []byte(" "))...)
	}
	return true, nil
}

// readFailure reports a stall, the caller's cancellation, or a transport error.
func (s *Stream) readFailure(err error) error {
	if s.ctx.Err() != nil {
		return context.Cause(s.ctx)
	}
	return transport(err)
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content          *string           `json:"content"`
			Reasoning        *string           `json:"reasoning"`
			ToolCalls        []toolCallDelta   `json:"tool_calls"`
			ReasoningDetails []json.RawMessage `json:"reasoning_details"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	} `json:"error"`
	Usage *struct {
		PromptTokens        *uint64  `json:"prompt_tokens"`
		CompletionTokens    *uint64  `json:"completion_tokens"`
		Cost                *float64 `json:"cost"`
		PromptTokensDetails *struct {
			CachedTokens uint64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails *struct {
			ReasoningTokens uint64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

type toolCallDelta struct {
	Index    uint64  `json:"index"`
	ID       *string `json:"id"`
	Function struct {
		Name      *string `json:"name"`
		Arguments *string `json:"arguments"`
	} `json:"function"`
}

func (s *Stream) event(data []byte) error {
	if string(data) == "[DONE]" {
		return nil
	}
	var c chunk
	if err := json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("malformed OpenRouter stream chunk: %w", err)
	}
	if c.Error != nil {
		return fmt.Errorf("OpenRouter reported an error: %s (%s)", c.Error.Message, c.Error.Code)
	}
	if usage := c.Usage; usage != nil {
		if usage.PromptTokens == nil || usage.CompletionTokens == nil || usage.Cost == nil {
			return errors.New("malformed OpenRouter stream chunk: usage lacks token counts or cost")
		}
		s.usage = &transcript.Usage{InputTokens: *usage.PromptTokens, OutputTokens: *usage.CompletionTokens, Cost: usage.Cost}
		if usage.PromptTokensDetails != nil {
			s.usage.CachedTokens = usage.PromptTokensDetails.CachedTokens
		}
		if usage.CompletionTokensDetails != nil {
			s.usage.ReasoningTokens = usage.CompletionTokensDetails.ReasoningTokens
		}
	}
	// The usage chunk after the finish chunk repeats the finish reason with an
	// empty delta; it cannot change the validated message.
	if s.finished || len(c.Choices) == 0 {
		return nil
	}
	if s.assembly == nil {
		s.assembly = &assembly{calls: map[uint64]*transcript.ToolCall{}}
	}
	choice := c.Choices[0]
	delta := choice.Delta
	if delta.Content != nil && *delta.Content != "" {
		s.assembly.text += *delta.Content
		s.items = append(s.items, Item{Text: *delta.Content})
	}
	if delta.Reasoning != nil && *delta.Reasoning != "" {
		s.assembly.reasoning += *delta.Reasoning
		s.items = append(s.items, Item{Reasoning: *delta.Reasoning})
	}
	for _, call := range delta.ToolCalls {
		s.assembly.toolCall(call)
	}
	for _, detail := range delta.ReasoningDetails {
		if err := s.assembly.reasoningDetail(detail); err != nil {
			return fmt.Errorf("malformed OpenRouter stream chunk: %w", err)
		}
	}
	if choice.FinishReason != nil && *choice.FinishReason != "" {
		s.finished = true
		completion, err := s.assembly.finish(*choice.FinishReason)
		if err != nil {
			return err
		}
		s.items = append(s.items, Item{Completion: completion})
	}
	return nil
}

type assembly struct {
	text      string
	reasoning string
	calls     map[uint64]*transcript.ToolCall
	details   []map[string]any
}

func (a *assembly) toolCall(delta toolCallDelta) {
	call := a.calls[delta.Index]
	if call == nil {
		call = &transcript.ToolCall{}
		a.calls[delta.Index] = call
	}
	if delta.ID != nil && *delta.ID != "" {
		call.CallID = *delta.ID
	}
	if delta.Function.Name != nil && *delta.Function.Name != "" {
		call.Name = *delta.Function.Name
	}
	if delta.Function.Arguments != nil {
		call.Arguments += *delta.Function.Arguments
	}
}

// reasoningDetail adds one streamed reasoning detail. Streaming indices can
// repeat across distinct reasoning blocks, so only consecutive text or summary
// fragments with matching IDs and indices merge; encrypted blocks stay opaque.
func (a *assembly) reasoningDetail(raw json.RawMessage) error {
	var detail map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&detail); err != nil {
		return err
	}
	kind, _ := detail["type"].(string)
	if (kind == "reasoning.text" || kind == "reasoning.summary") && len(a.details) > 0 {
		last := a.details[len(a.details)-1]
		if last["type"] == kind && sameOrAbsent(last, detail, "id") && sameOrAbsent(last, detail, "index") {
			for field, value := range detail {
				if value == nil {
					continue
				}
				if current, ok := last[field].(string); ok && (field == "text" || field == "summary") {
					if fragment, ok := value.(string); ok {
						last[field] = current + fragment
						continue
					}
				}
				last[field] = value
			}
			return nil
		}
	}
	a.details = append(a.details, detail)
	return nil
}

// sameOrAbsent reports whether field is missing or null in either detail, or
// equal in both.
func sameOrAbsent(previous, incoming map[string]any, field string) bool {
	a, b := previous[field], incoming[field]
	if a == nil || b == nil {
		return true
	}
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func (a *assembly) finish(reason string) (*Completion, error) {
	var calls []transcript.ToolCall
	for _, index := range slices.Sorted(maps.Keys(a.calls)) {
		calls = append(calls, *a.calls[index])
	}
	hasCalls := len(calls) > 0
	var stop Stop
	switch {
	case reason == "stop" && !hasCalls:
		stop = StopFinished
	case (reason == "stop" || reason == "tool_calls") && hasCalls:
		stop = StopToolCalls
	case reason == "tool_calls":
		return nil, errors.New("finish reason tool_calls without any tool call")
	case reason == "length" && !hasCalls:
		stop = StopTokenLimit
	case reason == "content_filter" && !hasCalls:
		stop = StopRefused
	case reason == "length" || reason == "content_filter":
		return nil, fmt.Errorf("finish reason %s with tool calls is not supported", reason)
	case reason == "error":
		return nil, errors.New("OpenRouter reported an error while generating")
	default:
		return nil, fmt.Errorf("unknown finish reason %q", reason)
	}
	message := transcript.AssistantMessage{Text: a.text, Reasoning: a.reasoning, ToolCalls: calls}
	for _, detail := range a.details {
		encoded, err := json.Marshal(detail)
		if err != nil {
			return nil, err
		}
		message.ContinuationMetadata = append(message.ContinuationMetadata, encoded)
	}
	if err := message.Validate(); err != nil {
		return nil, fmt.Errorf("incomplete tool call in model response: %w", err)
	}
	return &Completion{Message: message, Stop: stop}, nil
}
