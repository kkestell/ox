// Package openai signs in with a ChatGPT account, fetches the models its
// Codex subscription offers, sends model requests to the Codex backend, and
// assembles their streamed responses.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tmaxmax/go-sse"

	"ox/internal/catalog"
	"ox/internal/provider"
)

const codexEndpoint = "https://chatgpt.com/backend-api/codex"

// originator names Ox to the Codex backend, which requires one.
const originator = "ox"

// clientVersion is the Codex client version the models endpoint requires. It
// lists only the models a client of at least their minimum version can use, so
// a version above any Codex release lists them all.
const clientVersion = "99.0.0"

// stallTimeout is how long a model request may wait for response headers or
// for the next SSE event.
const stallTimeout = 120 * time.Second

// refreshMargin is how long before its expiry an access token is refreshed,
// so that it does not expire during a request.
const refreshMargin = time.Minute

// Client sends requests with the saved sign-in, refreshing it when it expires.
type Client struct {
	http          *http.Client
	endpoint      string
	tokenEndpoint string
	load          func() (Credential, error)
	save          func(Credential) error
	StallTimeout  time.Duration
}

// New returns a client for the Codex backend that uses the sign-in saved in
// the keyring.
func New() *Client {
	return &Client{http: &http.Client{}, endpoint: codexEndpoint, tokenEndpoint: tokenURL, load: load, save: save, StallTimeout: stallTimeout}
}

// credential returns the saved credential, refreshed and saved first when it
// expires within refreshMargin. Reading it for each request picks up a
// refresh by another Ox process.
func (c *Client) credential(ctx context.Context) (Credential, error) {
	credential, err := c.load()
	if err != nil {
		return Credential{}, err
	}
	if time.Until(credential.Expires) > refreshMargin {
		return credential, nil
	}
	refreshed, err := requestTokens(ctx, c.tokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {credential.Refresh},
	})
	if err != nil {
		return Credential{}, fmt.Errorf("refreshing the OpenAI sign-in failed; run `ox auth login openai`: %w", err)
	}
	if refreshed.Refresh == "" {
		refreshed.Refresh = credential.Refresh
	}
	if err := c.save(refreshed); err != nil {
		return Credential{}, err
	}
	return refreshed, nil
}

// do sends a request with the sign-in. A model request has a body and the
// session ID, which lets the backend reuse the session's cached prompt.
func (c *Client) do(ctx context.Context, method, path string, body []byte, sessionID string) (*http.Response, error) {
	credential, err := c.credential(ctx)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+credential.Access)
	request.Header.Set("chatgpt-account-id", credential.AccountID)
	request.Header.Set("originator", originator)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "text/event-stream")
		request.Header.Set("OpenAI-Beta", "responses=experimental")
		request.Header.Set("session-id", sessionID)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, transport(err)
	}
	return response, nil
}

// FetchCatalog downloads the models the account's subscription offers.
func (c *Client) FetchCatalog(ctx context.Context) (catalog.Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := c.do(ctx, http.MethodGet, "/models?client_version="+clientVersion, nil, "")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	text, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, transport(err)
	}
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("OpenAI model catalog returned %s: %s", response.Status, strings.TrimSpace(string(text)))
	}
	return ParseCatalog(text)
}

// ParseCatalog parses the Codex models response and keeps the models listed
// for pickers, in the order given.
func ParseCatalog(text []byte) (catalog.Catalog, error) {
	var response struct {
		Models []struct {
			Slug                     string `json:"slug"`
			DisplayName              string `json:"display_name"`
			Visibility               string `json:"visibility"`
			ContextWindow            int    `json:"context_window"`
			SupportedReasoningLevels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
			// InputModalities is absent for models that accept text and
			// images.
			InputModalities []string `json:"input_modalities"`
		} `json:"models"`
	}
	if err := json.Unmarshal(text, &response); err != nil {
		return nil, fmt.Errorf("malformed OpenAI model catalog: %w", err)
	}
	var models catalog.Catalog
	for _, listed := range response.Models {
		if listed.Slug == "" || listed.DisplayName == "" {
			return nil, errors.New("malformed OpenAI model catalog: a model lacks a slug or display name")
		}
		if listed.Visibility != "list" {
			continue
		}
		var supported []string
		for _, level := range listed.SupportedReasoningLevels {
			supported = append(supported, level.Effort)
		}
		efforts := []catalog.Effort{catalog.EffortDefault}
		for _, effort := range catalog.Efforts[1:] {
			if slices.Contains(supported, string(effort)) {
				efforts = append(efforts, effort)
			}
		}
		models = append(models, &catalog.Model{
			Provider:      catalog.OpenAI,
			ID:            listed.Slug,
			Name:          listed.DisplayName,
			ContextLimit:  listed.ContextWindow,
			AcceptsImages: listed.InputModalities == nil || slices.Contains(listed.InputModalities, "image"),
			Efforts:       efforts,
		})
	}
	if len(models) == 0 {
		return nil, errors.New("the OpenAI model catalog has no usable models")
	}
	return models, nil
}

// Stream starts one streamed response. The caller closes the stream.
func (c *Client) Stream(ctx context.Context, r provider.Request) (provider.Stream, error) {
	body, err := json.Marshal(body(r))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	stalled := &provider.TemporaryError{Message: fmt.Sprintf("OpenAI sent no response data for %v", c.StallTimeout)}
	timer := time.AfterFunc(c.StallTimeout, func() { cancel(stalled) })
	stream := &Stream{cancel: cancel, timer: timer, stallTimeout: c.StallTimeout, ctx: ctx}
	response, err := c.do(ctx, http.MethodPost, "/responses", body, r.SessionID)
	if err != nil {
		defer stream.Close()
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, err
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

// statusError describes a response that failed before streaming.
func statusError(response *http.Response, detail string) error {
	detail = strings.TrimSpace(detail)
	var parsed struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal([]byte(detail), &parsed)
	if err := classify(parsed.Error.Type, parsed.Error.Code, detail); err != nil {
		return err
	}
	message := fmt.Sprintf("OpenAI returned %s: %s", response.Status, detail)
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return &provider.TemporaryError{Message: message}
	}
	return errors.New(message)
}

// classify returns the error for a failure the agent handles specially, or nil.
func classify(kind, code, message string) error {
	switch {
	case code == "context_length_exceeded":
		return provider.ErrContextOverflow
	case kind == "usage_limit_reached" || code == "usage_limit_reached" || kind == "usage_not_included":
		return fmt.Errorf("the ChatGPT subscription usage limit is reached; see https://chatgpt.com/codex/settings/usage: %s", message)
	}
	return nil
}

func transport(err error) error {
	return fmt.Errorf("OpenAI request failed: %w", err)
}
