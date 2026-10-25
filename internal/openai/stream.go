package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/tmaxmax/go-sse"

	"ox/internal/provider"
	"ox/internal/transcript"
)

// Stream is one streamed Responses response. Closing it stops reading and
// discards text received before the response completed.
type Stream struct {
	ctx          context.Context
	cancel       context.CancelCauseFunc
	timer        *time.Timer
	stallTimeout time.Duration
	body         io.Closer
	nextEvent    func() (sse.Event, error, bool)
	stopEvents   func()
	items        []provider.Item
	message      transcript.AssistantMessage
	refused      bool
}

// Close releases the request.
func (s *Stream) Close() {
	s.timer.Stop()
	s.cancel(nil)
	if s.body != nil {
		s.body.Close()
	}
	if s.stopEvents != nil {
		s.stopEvents()
	}
}

// Next returns the next delta or the one validated completion. A stream that
// ends before the response completes is an error.
func (s *Stream) Next() (provider.Item, error) {
	for len(s.items) == 0 {
		event, err, more := s.nextEvent()
		if err != nil {
			return provider.Item{}, s.readFailure(err)
		}
		if !more {
			return provider.Item{}, errors.New("OpenAI stream ended before the response finished")
		}
		if event.Data == "" {
			continue
		}
		if !utf8.ValidString(event.Data) {
			return provider.Item{}, errors.New("OpenAI stream is not UTF-8")
		}
		s.timer.Reset(s.stallTimeout)
		if err := s.event([]byte(event.Data)); err != nil {
			return provider.Item{}, err
		}
	}
	item := s.items[0]
	s.items = s.items[1:]
	return item, nil
}

// readFailure reports a stall, the caller's cancellation, or a transport error.
func (s *Stream) readFailure(err error) error {
	if s.ctx.Err() != nil {
		return context.Cause(s.ctx)
	}
	return transport(err)
}

type streamEvent struct {
	Type         string          `json:"type"`
	Delta        string          `json:"delta"`
	SummaryIndex int             `json:"summary_index"`
	Item         json.RawMessage `json:"item"`
	Code         string          `json:"code"`
	Message      string          `json:"message"`
	Response     struct {
		Usage *struct {
			InputTokens        uint64 `json:"input_tokens"`
			InputTokensDetails struct {
				CachedTokens uint64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokens        uint64 `json:"output_tokens"`
			OutputTokensDetails struct {
				ReasoningTokens uint64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

type outputItem struct {
	Type    string `json:"type"`
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content []struct {
		Type string `json:"type"`
	} `json:"content"`
	Arguments string `json:"arguments"`
}

func (s *Stream) event(data []byte) error {
	var e streamEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return fmt.Errorf("malformed OpenAI stream event: %w", err)
	}
	switch e.Type {
	case "response.output_text.delta":
		if e.Delta != "" {
			s.message.Text += e.Delta
			s.items = append(s.items, provider.Item{Text: e.Delta})
		}
	case "response.reasoning_summary_part.added":
		if e.SummaryIndex > 0 {
			s.message.Reasoning += "\n\n"
			s.items = append(s.items, provider.Item{Reasoning: "\n\n"})
		}
	case "response.reasoning_summary_text.delta":
		if e.Delta != "" {
			s.message.Reasoning += e.Delta
			s.items = append(s.items, provider.Item{Reasoning: e.Delta})
		}
	case "response.output_item.done":
		return s.outputItem(e.Item)
	case "response.completed", "response.incomplete":
		completion, err := s.finish(e)
		if err != nil {
			return err
		}
		s.items = append(s.items, provider.Item{Completion: completion})
	case "response.failed":
		if failure := e.Response.Error; failure != nil {
			return failed(failure.Type, failure.Code, failure.Message)
		}
		return errors.New("OpenAI reported that the response failed")
	case "error":
		return failed("", e.Code, e.Message)
	}
	return nil
}

// outputItem adds one finished output item to the message.
func (s *Stream) outputItem(raw json.RawMessage) error {
	var item outputItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return fmt.Errorf("malformed OpenAI output item: %w", err)
	}
	switch item.Type {
	case "function_call":
		s.message.ToolCalls = append(s.message.ToolCalls, transcript.ToolCall{CallID: item.CallID, Name: item.Name, Arguments: item.Arguments})
	case "message":
		for _, part := range item.Content {
			if part.Type == "refusal" {
				s.refused = true
			}
		}
	case "reasoning":
		// Without stored responses, an item ID refers to nothing and the
		// backend rejects it.
		var reasoning map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&reasoning); err != nil {
			return fmt.Errorf("malformed OpenAI output item: %w", err)
		}
		delete(reasoning, "id")
		encoded, err := json.Marshal(reasoning)
		if err != nil {
			return err
		}
		s.message.ContinuationMetadata = append(s.message.ContinuationMetadata, encoded)
	}
	return nil
}

func (s *Stream) finish(e streamEvent) (*provider.Completion, error) {
	if usage := e.Response.Usage; usage != nil {
		s.message.Usage = &transcript.Usage{
			InputTokens:     usage.InputTokens,
			CachedTokens:    usage.InputTokensDetails.CachedTokens,
			OutputTokens:    usage.OutputTokens,
			ReasoningTokens: usage.OutputTokensDetails.ReasoningTokens,
		}
	}
	hasCalls := len(s.message.ToolCalls) > 0
	var stop provider.Stop
	switch {
	case e.Type == "response.incomplete":
		var reason string
		if e.Response.IncompleteDetails != nil {
			reason = e.Response.IncompleteDetails.Reason
		}
		switch {
		case hasCalls:
			return nil, fmt.Errorf("OpenAI response is incomplete (%s) with tool calls", reason)
		case reason == "max_output_tokens":
			stop = provider.StopTokenLimit
		case reason == "content_filter":
			stop = provider.StopRefused
		default:
			return nil, fmt.Errorf("OpenAI response is incomplete: %s", reason)
		}
	case hasCalls:
		stop = provider.StopToolCalls
	case s.refused:
		stop = provider.StopRefused
	default:
		stop = provider.StopFinished
	}
	if err := s.message.Validate(); err != nil {
		return nil, fmt.Errorf("incomplete tool call in model response: %w", err)
	}
	return &provider.Completion{Message: s.message, Stop: stop}, nil
}

// failed describes a response that failed while streaming.
func failed(kind, code, message string) error {
	if err := classify(kind, code, message); err != nil {
		return err
	}
	err := fmt.Sprintf("OpenAI reported an error: %s (%s)", message, code)
	switch code {
	case "rate_limit_exceeded", "server_error", "server_is_overloaded", "slow_down":
		return &provider.TemporaryError{Message: err}
	}
	return errors.New(err)
}
