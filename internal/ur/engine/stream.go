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
	"iter"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/kkestell/ox/internal/agent"
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

// client posts requests to OpenRouter. Request goroutines share it.
type client struct {
	endpoint  string
	key       string
	providers map[string][]string // model ID to provider order
}

// body is a streamed request of messages and tools with settings' model and
// effort, pinned to the model's providers when it has any.
func (c client) body(s agent.Settings, messages []message, tools []json.RawMessage) []byte {
	body := request{Model: s.Model, Messages: messages, Tools: tools, Stream: true}
	if s.Effort != "" {
		body.Reasoning = &reasoning{Effort: s.Effort}
	}
	if order := c.providers[s.Model]; len(order) > 0 {
		body.Provider = &provider{Order: order}
	}
	data, _ := json.Marshal(body) // strings, slices of them, and valid JSON always marshal
	return data
}

// ask posts one request with instructions as the system message and text as
// the user's, without tools, and returns the whole reply.
func (c client) ask(ctx context.Context, s agent.Settings, instructions, text string) (string, error) {
	messages := []message{{Role: "system", Content: content{Text: instructions}}, {Role: "user", Content: content{Text: text}}}
	var reply strings.Builder
	if _, err := c.post(ctx, c.body(s, messages, nil), func(text string, _ []reasoningDetail) { reply.WriteString(text) }); err != nil {
		return "", err
	}
	return reply.String(), nil
}

// post posts body, passing each chunk's reply text and reasoning fragments to
// onDelta, and returns the response's tool calls.
func (c client) post(ctx context.Context, body []byte, onDelta func(string, []reasoningDetail)) ([]toolCall, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
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

// read reads a completion's server-sent events, passing each chunk's reply
// text and reasoning fragments to onDelta, and returns the response's tool
// calls in index order.
func read(r io.Reader, onDelta func(string, []reasoningDetail)) ([]toolCall, error) {
	c := completion{calls: map[int]*toolCall{}}
	for data, err := range sseData(r) {
		if err != nil {
			return nil, err
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return nil, fmt.Errorf("malformed OpenRouter stream chunk: %w", err)
		}
		if err := c.add(ch, onDelta); err != nil {
			return nil, err
		}
	}
	return c.end()
}

// sseData yields the payload of each data: line of a stream of server-sent
// events, until data: [DONE] or the end of the stream, counting a last line
// without a newline. Lines may end in CRLF. Blank lines, which end events,
// and lines starting with ":", which are keep-alive comments, carry no data.
func sseData(r io.Reader) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		lines := bufio.NewReader(r)
		for {
			line, err := lines.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				yield("", err)
				return
			}
			data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data: ")
			if ok && data == "[DONE]" {
				return
			}
			if ok && !yield(data, nil) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

// completion is a streamed response as its chunks arrive: its latest finish
// reason, and its tool calls by index.
type completion struct {
	reason string
	calls  map[int]*toolCall
}

// add takes a chunk. OpenRouter reports an error after the response has
// started as a chunk with an error. A call arrives as fragments keyed by its
// index: the first carries its ID and name, and each adds to its arguments.
func (c *completion) add(ch chunk, onDelta func(string, []reasoningDetail)) error {
	if ch.Error != nil {
		return fmt.Errorf("OpenRouter: %s", ch.Error.Message)
	}
	if len(ch.Choices) == 0 {
		return nil
	}
	choice := ch.Choices[0]
	if d := choice.Delta; d.Content != "" || len(d.ReasoningDetails) > 0 {
		onDelta(d.Content, d.ReasoningDetails)
	}
	for _, fragment := range choice.Delta.ToolCalls {
		call := c.calls[fragment.Index]
		if call == nil {
			call = &toolCall{Type: "function"}
			c.calls[fragment.Index] = call
		}
		if fragment.ID != "" {
			call.ID = fragment.ID
		}
		if fragment.Function.Name != "" {
			call.Function.Name = fragment.Function.Name
		}
		call.Function.Arguments += fragment.Function.Arguments
	}
	if choice.FinishReason != "" {
		c.reason = choice.FinishReason
	}
	return nil
}

// end returns the finished response's tool calls in index order.
func (c *completion) end() ([]toolCall, error) {
	if err := finishError(c.reason); err != nil {
		return nil, err
	}
	var assembled []toolCall
	for _, index := range slices.Sorted(maps.Keys(c.calls)) {
		call := *c.calls[index]
		if call.ID == "" || call.Function.Name == "" {
			return nil, errors.New("OpenRouter sent a tool call without an id or name")
		}
		assembled = append(assembled, call)
	}
	return assembled, nil
}

// finishError is the error a response's last finish reason ends it with. The
// response is complete when the reason is stop or tool_calls; any other
// reason, such as length or content_filter, is an error.
func finishError(reason string) error {
	switch reason {
	case "stop", "tool_calls":
		return nil
	case "":
		return errors.New("OpenRouter stream ended before the response finished")
	case "length":
		return errors.New("OpenRouter stopped the reply at the model's output limit")
	case "content_filter":
		return errors.New("OpenRouter's provider filtered the reply")
	case "error":
		return errors.New("OpenRouter ended the reply with an error")
	}
	return fmt.Errorf("OpenRouter ended the reply with finish reason %q", reason)
}
