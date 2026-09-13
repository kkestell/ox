package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/kkestell/ox/internal/openrouter"
)

// ErrNoModels reports that no layer defined a model profile, and
// ErrNoDefaultModel that profiles exist but nothing chose one. The caller owns
// both messages, because it knows which files and CLI override were consulted.
var (
	ErrNoModels       = errors.New("no model profiles are configured")
	ErrNoDefaultModel = errors.New("no model is selected")
)

// Resolved is one model profile after validation: the model, where its
// selection came from, and the request fields the settings files may set. It is
// the frozen configuration one turn sends with every request.
type Resolved struct {
	Model       string                `json:"model"`
	ModelSource ModelSource           `json:"modelSource,omitempty"`
	MaxTokens   *int                  `json:"maxTokens,omitempty"`
	Temperature *float64              `json:"temperature,omitempty"`
	Reasoning   *openrouter.Reasoning `json:"reasoning,omitempty"`
	Provider    *openrouter.Provider  `json:"provider,omitempty"`
}

// Clone returns a Resolved that shares no pointer or slice with its receiver,
// so a session override cannot write through into another profile or into a
// configuration a running turn is frozen to.
func (r Resolved) Clone() Resolved {
	if r.MaxTokens != nil {
		maxTokens := *r.MaxTokens
		r.MaxTokens = &maxTokens
	}
	if r.Temperature != nil {
		temperature := *r.Temperature
		r.Temperature = &temperature
	}
	if r.Reasoning != nil {
		reasoning := *r.Reasoning
		reasoning.Enabled = cloneBool(reasoning.Enabled)
		reasoning.Exclude = cloneBool(reasoning.Exclude)
		r.Reasoning = &reasoning
	}
	if r.Provider != nil {
		provider := *r.Provider
		provider.Order = slices.Clone(provider.Order)
		provider.Only = slices.Clone(provider.Only)
		provider.Ignore = slices.Clone(provider.Ignore)
		provider.Quantizations = slices.Clone(provider.Quantizations)
		provider.AllowFallbacks = cloneBool(provider.AllowFallbacks)
		provider.RequireParameters = cloneBool(provider.RequireParameters)
		if provider.MaxPrice != nil {
			maxPrice := *provider.MaxPrice
			provider.MaxPrice = &maxPrice
		}
		r.Provider = &provider
	}
	return r
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// Profiles is the validated set of configured model profiles and the model a
// session selects when it makes no choice of its own. A session freezes the set
// at activation, so later edits to the settings files reach only later
// sessions.
type Profiles struct {
	// Default is the model ID a new session starts on, and DefaultSource names
	// the flag or layer that chose it.
	Default       string
	DefaultSource ModelSource

	models map[string]Resolved
}

// IDs lists the configured model IDs in sorted order.
func (p Profiles) IDs() []string {
	return slices.Sorted(maps.Keys(p.models))
}

// Select returns an independent copy of one configured profile, tagged with the
// source that chose it.
func (p Profiles) Select(id string, source ModelSource) (Resolved, error) {
	profile, configured := p.models[id]
	if !configured {
		return Resolved{}, fmt.Errorf(
			"model %q is not configured: configured models are %s",
			id,
			strings.Join(p.IDs(), ", "),
		)
	}
	profile = profile.Clone()
	profile.ModelSource = source
	return profile, nil
}

// DefaultProfile returns the profile a new session starts on. Resolve validates
// that the default is configured, so its absence here is a bug rather than a
// settings problem.
func (p Profiles) DefaultProfile() Resolved {
	profile, err := p.Select(p.Default, p.DefaultSource)
	if err != nil {
		panic(err)
	}
	return profile
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

// Resolve validates every configured profile and the effective model
// selection, performing every check that does not need the catalog. The
// selection is the --model override when present, then the workspace
// default_model, then the global one.
func Resolve(merged *Config, modelOverride string) (Profiles, error) {
	if merged == nil {
		merged = &Config{}
	}
	if len(merged.Models) == 0 {
		return Profiles{}, ErrNoModels
	}
	profiles := Profiles{models: make(map[string]Resolved, len(merged.Models))}
	for _, id := range slices.Sorted(maps.Keys(merged.Models)) {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id {
			return Profiles{}, fmt.Errorf(
				`"models" key %q must be a model ID with no surrounding whitespace`,
				id,
			)
		}
		resolved, err := resolveModel(merged.Models[id])
		if err != nil {
			return Profiles{}, fmt.Errorf("model %q: %w", id, err)
		}
		resolved.Model = id
		profiles.models[id] = resolved
	}

	switch override := strings.TrimSpace(modelOverride); {
	case override != "":
		profiles.Default = override
		profiles.DefaultSource = SourceCLI
	case merged.DefaultModel != nil:
		model := strings.TrimSpace(*merged.DefaultModel)
		if model == "" {
			return Profiles{}, errors.New(`"default_model" must not be blank`)
		}
		profiles.Default = model
		profiles.DefaultSource = merged.defaultSource
	default:
		return Profiles{}, ErrNoDefaultModel
	}
	if _, configured := profiles.models[profiles.Default]; !configured {
		return Profiles{}, fmt.Errorf(
			"%s names model %q, which is not configured: configured models are %s",
			profiles.DefaultSource,
			profiles.Default,
			strings.Join(profiles.IDs(), ", "),
		)
	}
	return profiles, nil
}

// resolveModel maps one profile's request fields onto the wire types.
func resolveModel(profile ModelConfig) (Resolved, error) {
	var resolved Resolved
	if profile.MaxTokens != nil {
		if *profile.MaxTokens < 1 {
			return Resolved{}, fmt.Errorf(
				`"max_tokens" must be at least 1, got %d`,
				*profile.MaxTokens,
			)
		}
		resolved.MaxTokens = profile.MaxTokens
	}
	if profile.Temperature != nil {
		if *profile.Temperature < 0 || *profile.Temperature > 2 {
			return Resolved{}, fmt.Errorf(
				`"temperature" must be between 0 and 2, got %v`,
				*profile.Temperature,
			)
		}
		resolved.Temperature = profile.Temperature
	}

	reasoning, err := resolveReasoning(profile.Reasoning)
	if err != nil {
		return Resolved{}, err
	}
	resolved.Reasoning = reasoning

	provider, err := resolveProvider(profile.Provider)
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
