package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

const (
	defaultBaseURL      = "https://openrouter.ai/api/v1"
	chatCompletionsPath = "/chat/completions"
	maxErrorBodySize    = 1024 * 1024
)

type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
	Logger  *slog.Logger
}

// Stream posts a streaming chat completion and calls onDelta once per fragment
// as it arrives. It returns the completion assembled so far alongside any
// error, so a turn cut short still knows what it streamed.
func (c *Client) Stream(
	ctx context.Context,
	request Request,
	onDelta func(Delta),
) (Completion, error) {
	request.Stream = true
	body, err := json.Marshal(request)
	if err != nil {
		return Completion{}, fmt.Errorf("encode OpenRouter request: %w", err)
	}

	c.Logger.Info(
		"starting OpenRouter stream",
		"model", request.Model,
		"messages", len(request.Messages),
	)

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL()+chatCompletionsPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return Completion{}, fmt.Errorf("build OpenRouter request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")

	response, err := c.httpClient().Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Completion{}, ctxErr
		}
		return Completion{}, fmt.Errorf("send OpenRouter request: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodySize))
		if err := errors.Join(readErr, response.Body.Close()); err != nil {
			return Completion{}, fmt.Errorf("read OpenRouter error response: %w", err)
		}
		return Completion{}, fmt.Errorf(
			"OpenRouter returned %s: %s",
			response.Status,
			strings.TrimSpace(string(raw)),
		)
	}

	var assembler streamAssembler
	streamErr := readSSE(response.Body, func(data []byte) error {
		return assembler.push(data, onDelta)
	})
	closeErr := response.Body.Close()
	completion := assembler.finish()
	if streamErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return completion, ctxErr
		}
		return completion, streamErr
	}
	if closeErr != nil {
		return completion, fmt.Errorf("close OpenRouter response: %w", closeErr)
	}
	if !assembler.sawChoice {
		return completion, errors.New("OpenRouter stream contained no completion choices")
	}

	c.logCompletion(completion)
	return completion, nil
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return defaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP == nil {
		return http.DefaultClient
	}
	return c.HTTP
}

func (c *Client) logCompletion(completion Completion) {
	var promptTokens, completionTokens, totalTokens int
	if completion.Usage != nil {
		promptTokens = completion.Usage.PromptTokens
		completionTokens = completion.Usage.CompletionTokens
		totalTokens = completion.Usage.TotalTokens
	}
	c.Logger.Info(
		"OpenRouter stream completed",
		"finish_reason", completion.FinishReason,
		"prompt_tokens", promptTokens,
		"completion_tokens", completionTokens,
		"total_tokens", totalTokens,
	)
}
