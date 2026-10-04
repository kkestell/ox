// Package openrouter fetches OpenRouter's model catalog, sends model requests,
// and assembles their streamed responses.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tmaxmax/go-sse"

	"ox/internal/catalog"
)

const defaultEndpoint = "https://openrouter.ai/api/v1"

// stallTimeout is how long a model request may wait for response headers or
// for the next SSE data event. SSE comments, which OpenRouter sends as
// keep-alives, do not count, so a provider that holds the request open without
// answering still stalls.
const stallTimeout = 120 * time.Second

// ErrContextOverflow means the provider rejected the input as too large for the
// model context. Every later request in the session fails the same way.
var ErrContextOverflow = errors.New("the model provider rejected the input as too large for the model context")

// temporaryError is a request failure a later attempt may not repeat: a stall,
// or an HTTP 429 or 5xx status.
type temporaryError struct{ message string }

func (e *temporaryError) Error() string { return e.message }

// IsTemporary reports whether retrying the request may succeed.
func IsTemporary(err error) bool {
	var temporary *temporaryError
	return errors.As(err, &temporary)
}

// Client holds OpenRouter credentials and a reusable connection pool.
type Client struct {
	http         *http.Client
	apiKey       string
	endpoint     string
	StallTimeout time.Duration
}

// New returns a client for OpenRouter, or for `OX_OPENROUTER_ENDPOINT` when set.
func New(apiKey string) *Client {
	endpoint := os.Getenv("OX_OPENROUTER_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	return NewForEndpoint(apiKey, endpoint)
}

// NewForEndpoint returns a client for an OpenRouter-compatible endpoint.
func NewForEndpoint(apiKey, endpoint string) *Client {
	return &Client{http: &http.Client{}, apiKey: apiKey, endpoint: endpoint, StallTimeout: stallTimeout}
}

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, transport(err)
	}
	return response, nil
}

// FetchCatalog downloads and filters OpenRouter's model catalog.
func (c *Client) FetchCatalog(ctx context.Context) (catalog.Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := c.get(ctx, "/models")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("OpenRouter model catalog returned %s", response.Status)
	}
	text, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, transport(err)
	}
	return ParseCatalog(text, time.Now().Unix())
}

// Verify checks the key against OpenRouter's key endpoint without generating.
func (c *Client) Verify(ctx context.Context) error {
	response, err := c.get(ctx, "/key")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode/100 == 2:
		return nil
	case response.StatusCode == http.StatusUnauthorized:
		return errors.New("OpenRouter rejected the API key")
	}
	return fmt.Errorf("OpenRouter key check returned %s", response.Status)
}

// Stream starts one streamed completion. The caller closes the stream.
func (c *Client) Stream(ctx context.Context, r Request) (*Stream, error) {
	body, err := json.Marshal(r.body())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	stalled := &temporaryError{fmt.Sprintf("OpenRouter sent no response data for %v", c.StallTimeout)}
	timer := time.AfterFunc(c.StallTimeout, func() { cancel(stalled) })
	stream := &Stream{cancel: cancel, timer: timer, stallTimeout: c.StallTimeout, ctx: ctx}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		stream.Close()
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		defer stream.Close()
		return nil, stream.readFailure(err)
	}
	if response.StatusCode/100 != 2 {
		defer stream.Close()
		defer response.Body.Close()
		detail, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, stream.readFailure(err)
		}
		return nil, statusError(response, string(detail))
	}
	stream.body = response.Body
	stream.nextEvent, stream.stopEvents = iter.Pull2(sse.Read(response.Body, &sse.ReadConfig{MaxEventSize: 16 * 1024 * 1024}))
	return stream, nil
}

func statusError(response *http.Response, detail string) error {
	switch response.StatusCode {
	case 400, 413, 422:
		if explicitContextOverflow(detail) {
			return ErrContextOverflow
		}
	}
	detail = strings.TrimSpace(detail)
	message := fmt.Sprintf("OpenRouter returned %s: %s", response.Status, detail)
	var parsed struct {
		Error struct {
			Metadata struct {
				ProviderName string `json:"provider_name"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(detail), &parsed) == nil && parsed.Error.Metadata.ProviderName != "" {
		message = fmt.Sprintf("%s, the provider OpenRouter routed this request to, returned %s: %s",
			parsed.Error.Metadata.ProviderName, response.Status, detail)
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return &temporaryError{message}
	}
	return errors.New(message)
}

func explicitContextOverflow(detail string) bool {
	lower := strings.ToLower(detail)
	if !strings.Contains(lower, "context") && !strings.Contains(lower, "prompt tokens") {
		return false
	}
	for _, term := range []string{"exceed", "too long", "too large", "maximum", "max context", "length"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func transport(err error) error {
	return fmt.Errorf("OpenRouter request failed: %w", err)
}
