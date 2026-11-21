package ur

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type request struct {
	Model     string     `json:"model"`
	Messages  []message  `json:"messages"`
	Stream    bool       `json:"stream"`
	Reasoning *reasoning `json:"reasoning,omitempty"`
	Provider  *provider  `json:"provider,omitempty"`
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
			Content string `json:"content"`
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
	err := a.post(ctx, body, func(text string) { a.deliver(delta{t, text}) })
	a.deliver(ended{t, err})
}

func (a *Agent) post(ctx context.Context, body []byte, onText func(string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError(resp)
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
// until data: [DONE] or the end of the stream. OpenRouter reports an error
// after the response has started as a chunk with an error.
func read(r io.Reader, onText func(string)) error {
	lines := bufio.NewReader(r)
	finished := false
	for {
		line, readErr := lines.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
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
				return fmt.Errorf("malformed OpenRouter stream chunk: %w", err)
			}
			if c.Error != nil {
				return fmt.Errorf("OpenRouter: %s", c.Error.Message)
			}
			if len(c.Choices) > 0 {
				if text := c.Choices[0].Delta.Content; text != "" {
					onText(text)
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
		return errors.New("OpenRouter stream ended before the response finished")
	}
	return nil
}
