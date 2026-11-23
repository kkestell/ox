package ur

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// message is one entry of the conversation, in OpenRouter's request shape.
type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"` // "function"
	Function function `json:"function"`
}

type function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type request struct {
	Model     string            `json:"model"`
	Messages  []message         `json:"messages"`
	Tools     []json.RawMessage `json:"tools"`
	Stream    bool              `json:"stream"`
	Reasoning *reasoning        `json:"reasoning,omitempty"`
	Provider  *provider         `json:"provider,omitempty"`
}

type reasoning struct {
	Effort string `json:"effort"`
}

// provider pins a request to its providers in order.
type provider struct {
	Order          []string `json:"order"`
	AllowFallbacks bool     `json:"allow_fallbacks"`
}

// chunk is one server-sent event of a streamed completion.
type chunk struct {
	Error   *apiError `json:"error"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int      `json:"index"`
				ID       string   `json:"id"`
				Function function `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type apiError struct {
	Message string `json:"message"`
}

// stream posts body for t and delivers its reply text and its end. It runs on
// its own goroutine and reads only the agent's fields set at start.
func (a *Agent) stream(ctx context.Context, t *turn, body []byte) {
	calls, err := a.post(ctx, body, func(text string) { a.deliver(delta{t, text}) })
	a.deliver(ended{t, calls, err})
}

func (a *Agent) post(ctx context.Context, body []byte, onText func(string)) ([]toolCall, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	return read(resp.Body, onText)
}

// statusError reports a refused request with OpenRouter's message, if its
// body has one.
func statusError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body struct {
		Error apiError `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error.Message != "" {
		return fmt.Errorf("OpenRouter %s: %s", resp.Status, body.Error.Message)
	}
	return fmt.Errorf("OpenRouter %s", resp.Status)
}

// read reads a completion's server-sent events, passing reply text to onText,
// until data: [DONE] or the end of the stream, and returns the response's tool
// calls in index order. A call arrives as fragments keyed by its index: the
// first carries its ID and name, and each adds to its arguments. OpenRouter
// reports an error after the response has started as a chunk with an error.
func read(r io.Reader, onText func(string)) ([]toolCall, error) {
	lines := bufio.NewReader(r)
	finished := false
	calls := map[int]*toolCall{}
	for {
		line, readErr := lines.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, readErr
		}
		// Blank lines end events, and lines starting with ":" are keep-alive
		// comments.
		data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data: ")
		if ok && data == "[DONE]" {
			break
		}
		if ok {
			var c chunk
			if err := json.Unmarshal([]byte(data), &c); err != nil {
				return nil, fmt.Errorf("malformed OpenRouter stream chunk: %w", err)
			}
			if c.Error != nil {
				return nil, fmt.Errorf("OpenRouter: %s", c.Error.Message)
			}
			if len(c.Choices) > 0 {
				if text := c.Choices[0].Delta.Content; text != "" {
					onText(text)
				}
				for _, fragment := range c.Choices[0].Delta.ToolCalls {
					call := calls[fragment.Index]
					if call == nil {
						call = &toolCall{Type: "function"}
						calls[fragment.Index] = call
					}
					if fragment.ID != "" {
						call.ID = fragment.ID
					}
					if fragment.Function.Name != "" {
						call.Function.Name = fragment.Function.Name
					}
					call.Function.Arguments += fragment.Function.Arguments
				}
				if c.Choices[0].FinishReason != "" {
					finished = true
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	if !finished {
		return nil, errors.New("OpenRouter stream ended before the response finished")
	}
	var assembled []toolCall
	for _, index := range slices.Sorted(maps.Keys(calls)) {
		call := *calls[index]
		if call.ID == "" || call.Function.Name == "" {
			return nil, errors.New("OpenRouter sent a tool call without an id or name")
		}
		assembled = append(assembled, call)
	}
	return assembled, nil
}
