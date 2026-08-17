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
	keyPath             = "/key"
	maxErrorBodySize    = 1024 * 1024
)

var ErrCredentialRejected = errors.New("OpenRouter rejected the credential")

type Client struct {
	APIKey  func() string
	BaseURL string
	HTTP    *http.Client
	Logger  *slog.Logger
}

// VerifyCredential asks OpenRouter whether key is usable without consulting
// the client's configured API key accessor.
func (c *Client) VerifyCredential(ctx context.Context, key string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+keyPath, nil)
	if err != nil {
		return fmt.Errorf("build OpenRouter credential request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+key)

	response, err := c.httpClient().Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("send OpenRouter credential request: %w", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodySize))
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return fmt.Errorf("read OpenRouter credential response: %w", err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		message := credentialErrorMessage(raw)
		if message == "" {
			message = response.Status
		}
		return fmt.Errorf("%w: %s", ErrCredentialRejected, message)
	}
	return fmt.Errorf(
		"OpenRouter returned %s while verifying the credential: %s",
		response.Status,
		strings.TrimSpace(string(raw)),
	)
}

func credentialErrorMessage(raw []byte) string {
	var envelope struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Error == nil {
		return ""
	}
	return strings.TrimSpace(envelope.Error.Message)
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
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey())
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

func (c *Client) apiKey() string {
	if c.APIKey == nil {
		panic("openrouter.Client.APIKey is nil")
	}
	return c.APIKey()
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
