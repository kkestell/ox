// Package catalog holds the OpenRouter models Ox can use, their qualified IDs,
// and the effort levels a model accepts.
package catalog

import (
	"slices"
	"strings"
)

// Provider is the provider prefix of every qualified model ID.
const Provider = "openrouter"

// Qualify returns the qualified ID of an OpenRouter model ID.
func Qualify(id string) string {
	return Provider + ":" + id
}

// Split returns the OpenRouter model ID of a qualified ID, which has the form
// `openrouter:<model-id>`.
func Split(qualified string) (string, bool) {
	provider, id, found := strings.Cut(qualified, ":")
	if !found || provider != Provider || id == "" {
		return "", false
	}
	return id, true
}

// Effort is how much reasoning Ox asks a model to do. EffortDefault leaves the
// choice to the model; every other level is the OpenRouter effort of the same
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

// Model is one usable OpenRouter model.
type Model struct {
	ID           string
	Name         string
	ContextLimit int
	// InputPrice and OutputPrice are USD per million tokens.
	InputPrice    float64
	OutputPrice   float64
	AcceptsImages bool
	// Efforts is EffortDefault followed by the listed efforts, ascending.
	Efforts []Effort
	// Providers pins requests to these OpenRouter provider slugs, tried in
	// order. Empty lets OpenRouter choose.
	Providers []string
}

// QualifiedID returns the model's ID with its provider prefix.
func (m *Model) QualifiedID() string {
	return Qualify(m.ID)
}

// Supports reports whether the model accepts the effort level.
func (m *Model) Supports(effort Effort) bool {
	return slices.Contains(m.Efforts, effort)
}

// Catalog is the fetched model catalog in display order.
type Catalog []*Model

// Lookup returns the model with the qualified ID, or nil.
func (c Catalog) Lookup(qualified string) *Model {
	id, ok := Split(qualified)
	if !ok {
		return nil
	}
	for _, model := range c {
		if model.ID == id {
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
