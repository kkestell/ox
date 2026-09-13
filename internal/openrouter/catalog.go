package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"
)

const (
	modelsPath = "/models"
	// catalogMaxAge is how long a cached catalog is served before Ox refetches
	// it. Model entries change over days, so a day old is still worth trusting;
	// past that a session should see current context windows and parameters.
	catalogMaxAge = 24 * time.Hour
	// maxCatalogBytes bounds both the cached file and the models response. The
	// published catalog is a few megabytes.
	maxCatalogBytes = 16 << 20
)

var ErrUnknownModel = errors.New("model is not in the OpenRouter catalog")

type Model struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	ContextLength       int             `json:"context_length"`
	TopProvider         TopProvider     `json:"top_provider"`
	Pricing             Pricing         `json:"pricing"`
	Architecture        Architecture    `json:"architecture"`
	SupportedParameters []string        `json:"supported_parameters"`
	Reasoning           *ModelReasoning `json:"reasoning,omitempty"`
}

type Architecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

type ModelReasoning struct {
	Mandatory         bool     `json:"mandatory"`
	DefaultEnabled    bool     `json:"default_enabled"`
	SupportedEfforts  []string `json:"supported_efforts"`
	DefaultEffort     string   `json:"default_effort"`
	SupportsMaxTokens bool     `json:"supports_max_tokens"`
}

type Pricing struct {
	Prompt            string `json:"prompt"`
	Completion        string `json:"completion"`
	Request           string `json:"request"`
	Image             string `json:"image"`
	WebSearch         string `json:"web_search"`
	InternalReasoning string `json:"internal_reasoning"`
	InputCacheRead    string `json:"input_cache_read"`
	InputCacheWrite   string `json:"input_cache_write"`
	InputCacheWrite1H string `json:"input_cache_write_1h"`
}

type TopProvider struct {
	ContextLength       int `json:"context_length"`
	MaxCompletionTokens int `json:"max_completion_tokens"`
}

type Catalog struct {
	models []Model
	byID   map[string]int
}

type modelEnvelope struct {
	Data []Model `json:"data"`
}

func newCatalog(models []Model) *Catalog {
	catalog := &Catalog{
		models: models,
		byID:   make(map[string]int, len(models)),
	}
	for index := range models {
		catalog.byID[models[index].ID] = index
	}
	return catalog
}

func (c *Catalog) Models() []Model {
	models := make([]Model, len(c.models))
	for index := range c.models {
		models[index] = cloneModel(c.models[index])
	}
	slices.SortFunc(models, func(left, right Model) int {
		return bytes.Compare([]byte(left.ID), []byte(right.ID))
	})
	return models
}

func (c *Catalog) Model(id string) (*Model, bool) {
	index, ok := c.byID[id]
	if !ok {
		return nil, false
	}
	model := cloneModel(c.models[index])
	return &model, true
}

// ContextWindow is the model's usable context length, preferring the model's
// own value and falling back to the top provider's.
func (m *Model) ContextWindow() int {
	if m.ContextLength > 0 {
		return m.ContextLength
	}
	return m.TopProvider.ContextLength
}

func (c *Catalog) ContextWindow(id string) int {
	model, ok := c.Model(id)
	if !ok {
		return 0
	}
	return model.ContextWindow()
}

func (c *Catalog) Reasoning(id string) *ModelReasoning {
	model, ok := c.Model(id)
	if !ok {
		return nil
	}
	return model.Reasoning
}

func (c *Client) Catalog(ctx context.Context) (*Catalog, error) {
	for {
		c.catalogMu.Lock()
		if c.catalog != nil {
			catalog := c.catalog
			c.catalogMu.Unlock()
			return catalog, nil
		}
		if c.catalogLoading {
			ready := c.catalogReady
			c.catalogMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ready:
				continue
			}
		}
		c.catalogLoading = true
		c.catalogReady = make(chan struct{})
		ready := c.catalogReady
		c.catalogMu.Unlock()

		catalog, err := c.loadCatalog(ctx)

		c.catalogMu.Lock()
		if err == nil {
			c.catalog = catalog
		}
		c.catalogLoading = false
		close(ready)
		c.catalogMu.Unlock()
		return catalog, err
	}
}

// ModelInfo is the catalog entry for one model id, so one lookup yields the
// context window, the reasoning metadata, and whether the model exists at all.
func (c *Client) ModelInfo(ctx context.Context, id string) (*Model, error) {
	catalog, err := c.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	model, ok := catalog.Model(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownModel, id)
	}
	return model, nil
}

// Models returns the catalog entries in model-id order without exposing the
// client's cached catalog to callers.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	catalog, err := c.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	return catalog.Models(), nil
}

func cloneModel(model Model) Model {
	model.SupportedParameters = append([]string(nil), model.SupportedParameters...)
	model.Architecture.InputModalities = append(
		[]string(nil),
		model.Architecture.InputModalities...,
	)
	model.Architecture.OutputModalities = append(
		[]string(nil),
		model.Architecture.OutputModalities...,
	)
	if model.Reasoning != nil {
		reasoning := *model.Reasoning
		reasoning.SupportedEfforts = append([]string(nil), reasoning.SupportedEfforts...)
		model.Reasoning = &reasoning
	}
	return model
}

// loadCatalog serves a fresh cache and otherwise fetches the catalog on the
// spot, so the load a caller waits for is the one that produces its answer.
// A stale cache is kept as a fallback: an outdated catalog beats no catalog
// when the provider is unreachable.
func (c *Client) loadCatalog(ctx context.Context) (*Catalog, error) {
	path := c.CachePath
	var stale *Catalog
	switch {
	case path == "":
		c.logger().Info("OpenRouter catalog cache unavailable")
	default:
		cached, age, err := readCatalogCache(path)
		switch {
		case err != nil:
			c.logger().Info("OpenRouter catalog cache miss", "path", path, "reason", err)
		case age <= catalogMaxAge:
			c.logger().Info(
				"OpenRouter catalog cache hit",
				"path", path, "models", len(cached.models), "age", age,
			)
			return cached, nil
		default:
			c.logger().Info("OpenRouter catalog cache is stale", "path", path, "age", age)
			stale = cached
		}
	}

	catalog, err := c.fetchCatalog(ctx)
	if err != nil {
		if stale != nil {
			c.logger().Warn(
				"serving stale OpenRouter catalog", "path", path, "error", err,
			)
			return stale, nil
		}
		return nil, err
	}
	if path != "" {
		if err := writeCatalogCache(path, catalog.models); err != nil {
			c.logger().Error("failed to write OpenRouter catalog cache", "path", path, "error", err)
		} else {
			c.logger().Info("wrote OpenRouter catalog cache", "path", path, "models", len(catalog.models))
		}
	}
	return catalog, nil
}

func (c *Client) fetchCatalog(ctx context.Context) (*Catalog, error) {
	started := time.Now()
	var lastErr error
	for attempt := 0; attempt < maxRetryAttempts; attempt++ {
		catalog, status, retryAfter, err := c.fetchCatalogAttempt(ctx)
		if err == nil {
			return catalog, nil
		}
		lastErr = err
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if classify(status, err) != retry || attempt == maxRetryAttempts-1 {
			return nil, err
		}
		delay := retryDelay(attempt, retryAfter)
		if time.Since(started)+delay > c.retryBudget() {
			break
		}
		c.logger().Warn(
			"retrying OpenRouter models request",
			"attempt", attempt+1,
			"status", status,
			"delay", delay,
			"error", err,
		)
		if err := c.wait(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("OpenRouter models request failed after retries: %w", lastErr)
}

func (c *Client) fetchCatalogAttempt(
	ctx context.Context,
) (*Catalog, int, string, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL()+modelsPath,
		nil,
	)
	if err != nil {
		return nil, 0, "", fmt.Errorf("build OpenRouter models request: %w", err)
	}
	if key := c.apiKey(); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient().Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, 0, "", ctxErr
		}
		return nil, 0, "", fmt.Errorf("send OpenRouter models request: %w", err)
	}

	retryAfter := response.Header.Get("Retry-After")
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxCatalogBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, 0, retryAfter,
			errors.Join(fmt.Errorf("read OpenRouter models response: %w", readErr), closeErr)
	}
	if len(raw) > maxCatalogBytes {
		return nil, response.StatusCode, retryAfter,
			fmt.Errorf("OpenRouter models response exceeds %d bytes", maxCatalogBytes)
	}
	if closeErr != nil {
		return nil, 0, retryAfter, fmt.Errorf("close OpenRouter models response: %w", closeErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode, retryAfter,
			fmt.Errorf("OpenRouter returned %s: %s", response.Status, bytes.TrimSpace(raw))
	}

	var envelope modelEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, response.StatusCode, retryAfter,
			fmt.Errorf("decode OpenRouter models response: %w", err)
	}
	return newCatalog(envelope.Data), response.StatusCode, retryAfter, nil
}
