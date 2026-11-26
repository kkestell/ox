package engine

import (
	"bufio"
	"bytes"
	"cmp"
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
	Role             string            `json:"role"`
	Content          content           `json:"content,omitzero"`
	ReasoningDetails []reasoningDetail `json:"reasoning_details,omitempty"`
	ToolCalls        []toolCall        `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
}

// content is a message's text and a user message's images, as data URLs. It
// is a JSON string without images, and OpenRouter's content parts with them:
// the images, then the text when it is not blank.
type content struct {
	Text   string
	Images []string
}

type contentPart struct {
	Type     string    `json:"type"` // text or image_url
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

func (c content) MarshalJSON() ([]byte, error) {
	if len(c.Images) == 0 {
		return json.Marshal(c.Text)
	}
	parts := []contentPart{}
	for _, url := range c.Images {
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{url}})
	}
	if strings.TrimSpace(c.Text) != "" {
		parts = append(parts, contentPart{Type: "text", Text: c.Text})
	}
	return json.Marshal(parts)
}

func (c *content) UnmarshalJSON(data []byte) error {
	if json.Unmarshal(data, &c.Text) == nil {
		return nil
	}
	var parts []contentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	for _, p := range parts {
		switch {
		case p.Type == "image_url" && p.ImageURL != nil:
			c.Images = append(c.Images, p.ImageURL.URL)
		case p.Type == "text":
			c.Text += p.Text
		}
	}
	return nil
}

// reasoningDetail is one block of a model's reasoning, or a fragment of one
// as streamed, in OpenRouter's reasoning_details shape. Fragments of one block
// share its index.
type reasoningDetail struct {
	Type      string `json:"type"` // reasoning.text, reasoning.summary, or reasoning.encrypted
	ID        string `json:"id,omitempty"`
	Format    string `json:"format,omitempty"`
	Index     int    `json:"index"`
	Text      string `json:"text,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Data      string `json:"data,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// display is the detail's readable text; encrypted reasoning has none.
func (d reasoningDetail) display() string {
	switch d.Type {
	case "reasoning.text":
		return d.Text
	case "reasoning.summary":
		return d.Summary
	}
	return ""
}

// appendReasoning appends fragments to details, merging each into the last
// detail when they share an index: text, summary, and data concatenate, the
// first type, id, and format stay, and the last signature wins.
func appendReasoning(details []reasoningDetail, fragments []reasoningDetail) []reasoningDetail {
	for _, f := range fragments {
		last := len(details) - 1
		if last < 0 || details[last].Index != f.Index {
			details = append(details, f)
			continue
		}
		d := &details[last]
		d.Text += f.Text
		d.Summary += f.Summary
		d.Data += f.Data
		d.Type = cmp.Or(d.Type, f.Type)
		d.ID = cmp.Or(d.ID, f.ID)
		d.Format = cmp.Or(d.Format, f.Format)
		d.Signature = cmp.Or(f.Signature, d.Signature)
	}
	return details
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
	Tools     []json.RawMessage `json:"tools,omitempty"`
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
			Content          string            `json:"content"`
			ReasoningDetails []reasoningDetail `json:"reasoning_details"`
			ToolCalls        []struct {
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

// stream posts body for t and delivers its reply text and reasoning and its
// end. It runs on its own goroutine and reads only the engine's fields set at
// start.
func (e *engine) stream(ctx context.Context, t *turn, body []byte) {
	calls, err := post(ctx, e.endpoint, e.key, body, func(text string, reasoning []reasoningDetail) { e.deliver(delta{t, text, reasoning}) })
	e.deliver(ended{t, calls, err})
}

// ask posts one request to endpoint with instructions as the system message
// and text as the user's, without tools, and returns the whole reply.
func ask(ctx context.Context, endpoint, key string, providers map[string][]string, model, effort, instructions, text string) (string, error) {
	messages := []message{{Role: "system", Content: content{Text: instructions}}, {Role: "user", Content: content{Text: text}}}
	body := request{Model: model, Messages: messages, Stream: true}
	if effort != "" {
		body.Reasoning = &reasoning{Effort: effort}
	}
	if order := providers[model]; len(order) > 0 {
		body.Provider = &provider{Order: order}
	}
	data, _ := json.Marshal(body) // strings and slices of them always marshal
	var reply strings.Builder
	if _, err := post(ctx, endpoint, key, data, func(text string, _ []reasoningDetail) { reply.WriteString(text) }); err != nil {
		return "", err
	}
	return reply.String(), nil
}

func post(ctx context.Context, endpoint, key string, body []byte, onDelta func(string, []reasoningDetail)) ([]toolCall, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	return read(resp.Body, onDelta)
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

// read reads a completion's server-sent events, passing each chunk's reply text
// and reasoning fragments to onDelta, until data: [DONE] or the end of the
// stream, and returns the response's tool calls in index order. A call arrives
// as fragments keyed by its index: the first carries its ID and name, and each
// adds to its arguments. OpenRouter reports an error after the response has
// started as a chunk with an error.
func read(r io.Reader, onDelta func(string, []reasoningDetail)) ([]toolCall, error) {
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
				if d := c.Choices[0].Delta; d.Content != "" || len(d.ReasoningDetails) > 0 {
					onDelta(d.Content, d.ReasoningDetails)
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
