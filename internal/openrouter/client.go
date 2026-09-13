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
	"sync"
	"time"
)

const (
	defaultBaseURL      = "https://openrouter.ai/api/v1"
	maxErrorBodySize    = 1024 * 1024
	maxRetryAttempts    = 5
	chatCompletionsPath = "/chat/completions"
	keyPath             = "/auth/key"
)

var ErrCredentialRejected = errors.New("OpenRouter rejected the credential")

type Client struct {
	APIKey  func() string
	BaseURL string
	HTTP    *http.Client
	Logger  *slog.Logger

	retryBudgetOverride time.Duration
	retryWait           func(context.Context, time.Duration) error
	// CachePath is where the model catalog is cached. Empty disables caching.
	CachePath string

	catalogMu      sync.Mutex
	catalogLoading bool
	catalogReady   chan struct{}
	catalog        *Catalog
	catalogExpires time.Time
}

type streamRequest struct {
	Request
	Stream bool `json:"stream"`
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

func (c *Client) Stream(
	ctx context.Context,
	request Request,
	onDelta func(Delta),
) (*Completion, error) {
	request.Provider = request.Provider.resolved(len(request.Tools) > 0)
	if len(request.Tools) > 0 && request.ToolChoice == "" {
		request.ToolChoice = "auto"
	}
	body, err := json.Marshal(streamRequest{Request: request, Stream: true})
	if err != nil {
		return &Completion{}, fmt.Errorf("encode OpenRouter request: %w", err)
	}

	c.logger().Info(
		"starting OpenRouter stream",
		"model", request.Model,
		"messages", len(request.Messages),
		"tools", len(request.Tools),
		"reasoning", request.Reasoning,
	)

	started := time.Now()
	lastCompletion := &Completion{}
	var lastErr error
	for number := 0; number < maxRetryAttempts; number++ {
		attempt, attemptErr := c.streamAttempt(ctx, body, onDelta)
		completion := attempt.completion
		lastCompletion = completion
		if attemptErr == nil {
			c.logCompletion(completion)
			return completion, nil
		}
		lastErr = attemptErr
		if ctxErr := ctx.Err(); ctxErr != nil {
			return completion, ctxErr
		}
		if attempt.observedContent {
			return completion, attemptErr
		}

		classification := classify(attempt.status, attemptErr)
		var streamError *streamAPIError
		if errors.As(attemptErr, &streamError) {
			classification = classify(streamError.code, attemptErr)
		} else if errors.Is(attemptErr, errStreamEnded) ||
			errors.Is(attemptErr, errStreamRead) {
			classification = retry
		}
		if classification != retry {
			return completion, attemptErr
		}
		if number == maxRetryAttempts-1 {
			break
		}

		delay := retryDelay(number, attempt.retryAfter)
		if time.Since(started)+delay > c.retryBudget() {
			break
		}
		c.logger().Warn(
			"retrying OpenRouter stream",
			"attempt", number+1,
			"status", attempt.status,
			"delay", delay,
			"error", attemptErr,
		)
		if err := c.wait(ctx, delay); err != nil {
			return completion, err
		}
	}
	return lastCompletion, fmt.Errorf(
		"OpenRouter stream failed after retries: %w",
		lastErr,
	)
}

// streamAttemptResult carries what the retry decision needs from one request:
// its status, the server's Retry-After, and whether any content already reached
// the caller.
type streamAttemptResult struct {
	completion      *Completion
	status          int
	retryAfter      string
	observedContent bool
}

func (c *Client) streamAttempt(
	ctx context.Context,
	body []byte,
	onDelta func(Delta),
) (streamAttemptResult, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL()+chatCompletionsPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return streamAttemptResult{completion: &Completion{}},
			fmt.Errorf("build OpenRouter request: %w", err)
	}
	c.setHeaders(request)

	response, err := c.httpClient().Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return streamAttemptResult{completion: &Completion{}}, ctxErr
		}
		return streamAttemptResult{completion: &Completion{}},
			fmt.Errorf("send OpenRouter request: %w", err)
	}

	failed := streamAttemptResult{
		completion: &Completion{},
		status:     response.StatusCode,
		retryAfter: response.Header.Get("Retry-After"),
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodySize))
		closeErr := response.Body.Close()
		if readErr != nil {
			return failed,
				errors.Join(fmt.Errorf("read OpenRouter error response: %w", readErr), closeErr)
		}
		if closeErr != nil {
			return failed, fmt.Errorf("close OpenRouter error response: %w", closeErr)
		}
		return failed,
			fmt.Errorf("OpenRouter returned %s: %s", response.Status, strings.TrimSpace(string(raw)))
	}

	var assembler streamAssembler
	streamErr := readSSE(response.Body, func(data []byte) error {
		return assembler.push(data, onDelta)
	})
	closeErr := response.Body.Close()
	result := streamAttemptResult{
		completion:      assembler.finish(),
		status:          failed.status,
		retryAfter:      failed.retryAfter,
		observedContent: assembler.observedContent,
	}
	if streamErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		return result, streamErr
	}
	if closeErr != nil {
		return result, fmt.Errorf("close OpenRouter response: %w", closeErr)
	}
	if !assembler.sawChoice {
		return result, errors.New("OpenRouter stream contained no completion choices")
	}
	return result, nil
}

func (c *Client) setHeaders(request *http.Request) {
	if key := c.apiKey(); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
}

func (c *Client) apiKey() string {
	if c.APIKey == nil {
		return ""
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

func (c *Client) logger() *slog.Logger {
	if c.Logger == nil {
		return slog.Default()
	}
	return c.Logger
}

func (c *Client) retryBudget() time.Duration {
	if c.retryBudgetOverride > 0 {
		return c.retryBudgetOverride
	}
	return defaultRetryBudget
}

func (c *Client) wait(ctx context.Context, delay time.Duration) error {
	if c.retryWait != nil {
		return c.retryWait(ctx, delay)
	}
	return sleepContext(ctx, delay)
}

func (c *Client) logCompletion(completion *Completion) {
	var promptTokens, completionTokens, cachedTokens int
	var cost float64
	if completion.Usage != nil {
		promptTokens = completion.Usage.PromptTokens
		completionTokens = completion.Usage.CompletionTokens
		cost = completion.Usage.Cost
		if completion.Usage.PromptTokensDetails != nil {
			cachedTokens = completion.Usage.PromptTokensDetails.CachedTokens
		}
	}
	c.logger().Info(
		"OpenRouter stream completed",
		"finish_reason", completion.FinishReason,
		"prompt_tokens", promptTokens,
		"completion_tokens", completionTokens,
		"cached_tokens", cachedTokens,
		"cost", cost,
	)
}
