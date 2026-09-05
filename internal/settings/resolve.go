package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/kkestell/ox/internal/openrouter"
)

// ErrNoModel reports that no layer named a model and no override supplied one.
// The caller owns the message, because it knows which files and CLI override
// were consulted.
var ErrNoModel = errors.New("no model is configured")

// Resolved is the frozen model configuration one session sends with every
// request: the model, where it came from, and the request fields the settings
// files may set.
type Resolved struct {
	Model       string                `json:"model"`
	ModelSource ModelSource           `json:"modelSource,omitempty"`
	MaxTokens   *int                  `json:"maxTokens,omitempty"`
	Temperature *float64              `json:"temperature,omitempty"`
	Reasoning   *openrouter.Reasoning `json:"reasoning,omitempty"`
	Provider    *openrouter.Provider  `json:"provider,omitempty"`
}

// Compatibility describes request requirements that are resolved outside the
// settings files. The active mode decides whether tools are present, and the
// session history decides which input modalities a replacement model must
// continue to accept.
type Compatibility struct {
	Tools      bool
	Modalities []string
}

// LogValue renders the resolved settings for a log line. The wire types are full
// of pointers, which would otherwise log as addresses; reasoning and provider are
// rendered as the JSON a request carries.
func (r Resolved) LogValue() slog.Value {
	attributes := []slog.Attr{
		slog.String("model", r.Model),
		slog.String("source", string(r.ModelSource)),
	}
	if r.MaxTokens != nil {
		attributes = append(attributes, slog.Int("max_tokens", *r.MaxTokens))
	}
	if r.Temperature != nil {
		attributes = append(attributes, slog.Float64("temperature", *r.Temperature))
	}
	if r.Reasoning != nil {
		attributes = append(attributes, slog.String("reasoning", encoded(r.Reasoning)))
	}
	if r.Provider != nil {
		attributes = append(attributes, slog.String("provider", encoded(r.Provider)))
	}
	return slog.GroupValue(attributes...)
}

func encoded(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", err)
	}
	return string(raw)
}

// Resolve folds a merged Config and a model override into one Resolved value,
// performing every check that does not need the catalog. Precedence for the
// model is override, then workspace, then global.
func Resolve(merged *Config, modelOverride string) (Resolved, error) {
	if merged == nil {
		merged = &Config{}
	}
	var resolved Resolved
	switch override := strings.TrimSpace(modelOverride); {
	case override != "":
		resolved.Model = override
		resolved.ModelSource = SourceCLI
	case merged.Model != nil:
		model := strings.TrimSpace(*merged.Model)
		if model == "" {
			return Resolved{}, errors.New(`"model" must not be blank`)
		}
		resolved.Model = model
		resolved.ModelSource = merged.modelSource
	default:
		return Resolved{}, ErrNoModel
	}

	if merged.MaxTokens != nil {
		if *merged.MaxTokens < 1 {
			return Resolved{}, fmt.Errorf(
				`"max_tokens" must be at least 1, got %d`,
				*merged.MaxTokens,
			)
		}
		resolved.MaxTokens = merged.MaxTokens
	}
	if merged.Temperature != nil {
		if *merged.Temperature < 0 || *merged.Temperature > 2 {
			return Resolved{}, fmt.Errorf(
				`"temperature" must be between 0 and 2, got %v`,
				*merged.Temperature,
			)
		}
		resolved.Temperature = merged.Temperature
	}

	reasoning, err := resolveReasoning(merged.Reasoning)
	if err != nil {
		return Resolved{}, err
	}
	resolved.Reasoning = reasoning

	provider, err := resolveProvider(merged.Provider)
	if err != nil {
		return Resolved{}, err
	}
	resolved.Provider = provider
	return resolved, nil
}

// resolveReasoning maps the reasoning block onto the wire type, dropping an
// empty object so it does not reach a request as `"reasoning": {}`.
func resolveReasoning(reasoning *Reasoning) (*openrouter.Reasoning, error) {
	if reasoning == nil ||
		(reasoning.Enabled == nil && reasoning.Effort == nil && reasoning.Exclude == nil) {
		return nil, nil
	}
	var effort string
	if reasoning.Effort != nil {
		effort = strings.TrimSpace(*reasoning.Effort)
		if effort == "" {
			return nil, errors.New(`"reasoning.effort" must not be blank`)
		}
	}
	return &openrouter.Reasoning{
		Effort:  effort,
		Enabled: reasoning.Enabled,
		Exclude: reasoning.Exclude,
	}, nil
}

// resolveProvider maps the provider block onto the wire type. sort,
// data_collection, and the price caps are passed through unvalidated: OpenRouter
// owns those vocabularies. An empty block is left for openrouter.Provider to
// drop, which it already does when a request carries no tools.
func resolveProvider(provider *Provider) (*openrouter.Provider, error) {
	if provider == nil {
		return nil, nil
	}
	for _, list := range []struct {
		key    string
		values []string
	}{
		{"order", provider.Order},
		{"only", provider.Only},
		{"ignore", provider.Ignore},
		{"quantizations", provider.Quantizations},
	} {
		for _, value := range list.values {
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf(
					`"provider.%s" must not contain a blank entry`,
					list.key,
				)
			}
		}
	}

	var maxPrice *openrouter.MaxPrice
	if provider.MaxPrice != nil {
		for _, price := range []struct {
			key   string
			value *float64
		}{
			{"prompt", provider.MaxPrice.Prompt},
			{"completion", provider.MaxPrice.Completion},
		} {
			if price.value != nil && *price.value < 0 {
				return nil, fmt.Errorf(
					`"provider.max_price.%s" must not be negative, got %v`,
					price.key,
					*price.value,
				)
			}
		}
		maxPrice = &openrouter.MaxPrice{
			Prompt:     provider.MaxPrice.Prompt,
			Completion: provider.MaxPrice.Completion,
		}
	}
	return &openrouter.Provider{
		Order:          provider.Order,
		Only:           provider.Only,
		Ignore:         provider.Ignore,
		Quantizations:  provider.Quantizations,
		Sort:           deref(provider.Sort),
		DataCollection: deref(provider.DataCollection),
		AllowFallbacks: provider.AllowFallbacks,
		MaxPrice:       maxPrice,
	}, nil
}

// Validate is the half of resolution that needs catalog metadata. It checks
// configured request fields and the requirements of the effective tools and
// retained conversation before a model is selected.
func Validate(entry *openrouter.Model, resolved Resolved, compatibility Compatibility) error {
	parameters := entry.SupportedParameters
	if resolved.Temperature != nil && !slices.Contains(parameters, "temperature") {
		return fmt.Errorf(
			"model %s does not support configured \"temperature\"",
			entry.ID,
		)
	}
	if resolved.MaxTokens != nil && !slices.Contains(parameters, "max_tokens") {
		return fmt.Errorf(
			"model %s does not support configured \"max_tokens\"",
			entry.ID,
		)
	}
	if compatibility.Tools && !slices.Contains(parameters, "tools") {
		return fmt.Errorf(
			"model %s does not support the effective tool set",
			entry.ID,
		)
	}
	modalities := append([]string(nil), compatibility.Modalities...)
	slices.Sort(modalities)
	modalities = slices.Compact(modalities)
	for _, modality := range modalities {
		if !slices.Contains(entry.Architecture.InputModalities, modality) {
			return fmt.Errorf(
				"model %s does not support retained %s input",
				entry.ID,
				modality,
			)
		}
	}
	if resolved.Reasoning == nil {
		return nil
	}
	if entry.Reasoning == nil {
		return fmt.Errorf("model %s does not support reasoning", entry.ID)
	}
	if resolved.Reasoning.Enabled != nil && !*resolved.Reasoning.Enabled &&
		entry.Reasoning.Mandatory {
		return fmt.Errorf("model %s always reasons: \"reasoning.enabled\" cannot be false", entry.ID)
	}
	efforts := entry.Reasoning.SupportedEfforts
	if resolved.Reasoning.Effort != "" && len(efforts) > 0 &&
		!slices.Contains(efforts, resolved.Reasoning.Effort) {
		return fmt.Errorf(
			"model %s does not support reasoning effort %q: supported efforts are %s",
			entry.ID,
			resolved.Reasoning.Effort,
			strings.Join(efforts, ", "),
		)
	}
	return nil
}

func deref[T any](pointer *T) T {
	if pointer == nil {
		var zero T
		return zero
	}
	return *pointer
}
