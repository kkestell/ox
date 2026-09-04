package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const modelsPath = "/models"

var ErrUnknownModel = errors.New("model is not in the OpenRouter catalog")

type Model struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	ContextLength       int             `json:"context_length"`
	TopProvider         TopProvider     `json:"top_provider"`
	Pricing             Pricing         `json:"pricing"`
	SupportedParameters []string        `json:"supported_parameters"`
	Reasoning           *ModelReasoning `json:"reasoning,omitempty"`
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

func cloneModel(model Model) Model {
	model.SupportedParameters = append([]string(nil), model.SupportedParameters...)
	if model.Reasoning != nil {
		reasoning := *model.Reasoning
		reasoning.SupportedEfforts = append([]string(nil), reasoning.SupportedEfforts...)
		model.Reasoning = &reasoning
	}
	return model
}

func (c *Client) loadCatalog(ctx context.Context) (*Catalog, error) {
	path := c.resolvedCachePath()
	if path != "" {
		cached, err := readCatalogCache(path)
		if err == nil {
			c.logger().Info("OpenRouter catalog cache hit", "path", path, "models", len(cached.models))
			go c.refreshCatalog(path)
			return cached, nil
		}
		c.logger().Info("OpenRouter catalog cache miss", "path", path, "reason", err)
	} else {
		c.logger().Info("OpenRouter catalog cache unavailable")
	}

	catalog, err := c.fetchCatalog(ctx)
	if err != nil {
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

func (c *Client) refreshCatalog(path string) {
	c.logger().Info("refreshing OpenRouter catalog cache", "path", path)
	ctx, cancel := context.WithTimeout(context.Background(), c.retryBudget()+maxBackoff)
	defer cancel()

	catalog, err := c.fetchCatalog(ctx)
	if err != nil {
		c.logger().Warn("failed to refresh OpenRouter catalog cache", "path", path, "error", err)
		return
	}
	if err := writeCatalogCache(path, catalog.models); err != nil {
		c.logger().Error("failed to write refreshed OpenRouter catalog cache", "path", path, "error", err)
		return
	}
	c.logger().Info("refreshed OpenRouter catalog cache", "path", path, "models", len(catalog.models))
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
	raw, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, 0, retryAfter,
			errors.Join(fmt.Errorf("read OpenRouter models response: %w", readErr), closeErr)
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
