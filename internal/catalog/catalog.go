// Package catalog holds the models Ox can use, their qualified IDs, and the
// effort levels a model accepts.
package catalog

import (
	"slices"
	"strings"
)

// The model providers, each the prefix of its models' qualified IDs.
const (
	OpenRouter = "openrouter"
	OpenAI     = "openai"
)

// Providers lists every model provider.
var Providers = []string{OpenRouter, OpenAI}

// Split returns the provider and model ID of a qualified ID, which has the
// form `<provider>:<model-id>`. The model ID may itself contain colons.
func Split(qualified string) (provider, id string, ok bool) {
	provider, id, found := strings.Cut(qualified, ":")
	if !found || !slices.Contains(Providers, provider) || id == "" {
		return "", "", false
	}
	return provider, id, true
}

// Effort is how much reasoning Ox asks a model to do. EffortDefault leaves the
// choice to the model; every other level is the provider's effort of the same
// ID.
type Effort string

const (
	EffortDefault Effort = "default"
	EffortNone    Effort = "none"
	EffortMinimal Effort = "minimal"
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
	EffortXHigh   Effort = "xhigh"
	EffortMax     Effort = "max"
)

// Efforts lists every effort level in ascending order.
var Efforts = []Effort{
	EffortDefault, EffortNone, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax,
}

// ParseEffort returns the effort level with the given ID.
func ParseEffort(id string) (Effort, bool) {
	for _, effort := range Efforts {
		if string(effort) == id {
			return effort, true
		}
	}
	return "", false
}

// Name is the effort level's display name.
func (e Effort) Name() string {
	if e == EffortXHigh {
		return "Extra high"
	}
	return strings.ToUpper(string(e[:1])) + string(e[1:])
}

// Model is one usable model.
type Model struct {
	Provider     string
	ID           string
	Name         string
	ContextLimit int
	// InputPrice and OutputPrice are USD per million tokens, or nil when the
	// provider does not charge per token.
	InputPrice    *float64
	OutputPrice   *float64
	AcceptsImages bool
	// Efforts is EffortDefault followed by the listed efforts, ascending.
	Efforts []Effort
	// Providers pins requests to these OpenRouter provider slugs, tried in
	// order. Empty lets OpenRouter choose.
	Providers []string
}

// QualifiedID returns the model's ID with its provider prefix.
func (m *Model) QualifiedID() string {
	return m.Provider + ":" + m.ID
}

// Supports reports whether the model accepts the effort level.
func (m *Model) Supports(effort Effort) bool {
	return slices.Contains(m.Efforts, effort)
}

// Catalog is the fetched model catalog in display order.
type Catalog []*Model

// Lookup returns the model with the qualified ID, or nil.
func (c Catalog) Lookup(qualified string) *Model {
	provider, id, ok := Split(qualified)
	if !ok {
		return nil
	}
	for _, model := range c {
		if model.Provider == provider && model.ID == id {
			return model
		}
	}
	return nil
}

// IDs returns every qualified model ID.
func (c Catalog) IDs() []string {
	ids := make([]string, len(c))
	for i, model := range c {
		ids[i] = model.QualifiedID()
	}
	return ids
}
