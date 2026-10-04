package openrouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"ox/internal/catalog"
)

// recentSeconds is about six months.
const recentSeconds = 183 * 24 * 60 * 60

// The fields Ox reads from one entry of OpenRouter's `GET /models`.
type listedModel struct {
	ID            *string `json:"id"`
	Name          *string `json:"name"`
	ContextLength *int    `json:"context_length"`
	// Created is Unix seconds when OpenRouter added the model.
	Created      *int64 `json:"created"`
	Architecture *struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	// Pricing is USD per token, listed as decimal strings.
	Pricing *struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
	SupportedParameters []string `json:"supported_parameters"`
	Reasoning           *struct {
		SupportedEfforts []string `json:"supported_efforts"`
	} `json:"reasoning"`
}

// ParseCatalog parses OpenRouter's `GET /models` response and keeps the models
// Ox can use: not a `:batch` variant, which chat completions does not serve;
// accepts tools; takes and produces text; no negative price, which OpenRouter
// lists for routers such as `openrouter/auto-beta`; and released within about
// six months of now. Unknown efforts are dropped. Models are sorted by name.
func ParseCatalog(text []byte, now int64) (catalog.Catalog, error) {
	var response struct {
		Data []listedModel `json:"data"`
	}
	malformed := func(err error) error {
		return fmt.Errorf("malformed OpenRouter model catalog: %w", err)
	}
	if err := json.Unmarshal(text, &response); err != nil {
		return nil, malformed(err)
	}
	var models catalog.Catalog
	for _, listed := range response.Data {
		if listed.ID == nil || listed.Name == nil || listed.ContextLength == nil || listed.Created == nil ||
			listed.Architecture == nil || listed.Pricing == nil || listed.SupportedParameters == nil {
			return nil, malformed(errors.New("a model lacks a required field"))
		}
		prompt, err := price(listed.Pricing.Prompt)
		if err != nil {
			return nil, malformed(err)
		}
		completion, err := price(listed.Pricing.Completion)
		if err != nil {
			return nil, malformed(err)
		}
		architecture := listed.Architecture
		if strings.HasSuffix(*listed.ID, ":batch") ||
			!slices.Contains(listed.SupportedParameters, "tools") ||
			!slices.Contains(architecture.InputModalities, "text") ||
			!slices.Contains(architecture.OutputModalities, "text") ||
			prompt < 0 || completion < 0 ||
			*listed.Created < now-recentSeconds {
			continue
		}
		var supported []string
		if listed.Reasoning != nil {
			supported = listed.Reasoning.SupportedEfforts
		}
		efforts := []catalog.Effort{catalog.EffortDefault}
		for _, effort := range catalog.Efforts[1:] {
			if slices.Contains(supported, string(effort)) {
				efforts = append(efforts, effort)
			}
		}
		models = append(models, &catalog.Model{
			ID:            *listed.ID,
			Name:          *listed.Name,
			ContextLimit:  *listed.ContextLength,
			InputPrice:    prompt * 1_000_000,
			OutputPrice:   completion * 1_000_000,
			AcceptsImages: slices.Contains(architecture.InputModalities, "image"),
			Efforts:       efforts,
		})
	}
	slices.SortStableFunc(models, func(a, b *catalog.Model) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	if len(models) == 0 {
		return nil, errors.New("the OpenRouter model catalog has no usable models")
	}
	return models, nil
}

func price(text string) (float64, error) {
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("price %q is not a decimal number", text)
	}
	return value, nil
}
