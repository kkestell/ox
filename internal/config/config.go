package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

//go:embed default.json
var defaultConfig []byte

type Config struct {
	Harnesses map[string]Harness `json:"harnesses"`
	Defaults  Defaults           `json:"defaults"`
}

type Defaults struct {
	Harness        string `json:"harness"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
}

type Harness struct {
	Models          []Model  `json:"models"`
	PermissionModes []Option `json:"permission_modes"`
}

type Model struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Efforts []Option `json:"efforts"`
	// Providers is the order of providers that serve the model, for Ur.
	Providers []string `json:"providers,omitempty"`
}

type Option struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		data = defaultConfig
	} else if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("read %s: content after the configuration", path)
	}
	for name, harness := range config.Harnesses {
		if err := harness.check(); err != nil {
			return nil, fmt.Errorf("%s: harness %q: %w", path, name, err)
		}
	}
	if err := config.Defaults.check(config.Harnesses); err != nil {
		return nil, fmt.Errorf("%s: defaults: %w", path, err)
	}
	return &config, nil
}

// check requires every list a client reads, and an ID and a name for every
// choice. The harness receives the ID as a flag. A model without effort levels is served an
// empty list.
func (h Harness) check() error {
	if len(h.Models) == 0 || len(h.PermissionModes) == 0 {
		return errors.New("needs models and permission_modes")
	}
	models := make(map[string]bool)
	for i, m := range h.Models {
		if err := addChoice(models, "model", m.ID, m.Name); err != nil {
			return err
		}
		if m.Efforts == nil {
			h.Models[i].Efforts = []Option{}
		}
		if err := checkOptions("effort level", m.Efforts); err != nil {
			return fmt.Errorf("model %q: %w", m.ID, err)
		}
	}
	return checkOptions("permission mode", h.PermissionModes)
}

func checkOptions(kind string, options []Option) error {
	seen := make(map[string]bool)
	for _, o := range options {
		if err := addChoice(seen, kind, o.ID, o.Name); err != nil {
			return err
		}
	}
	return nil
}

func addChoice(seen map[string]bool, kind, id, name string) error {
	if id == "" {
		return fmt.Errorf("every %s needs an id", kind)
	}
	if name == "" {
		return fmt.Errorf("every %s needs a name, and %q has none", kind, id)
	}
	if seen[id] {
		return fmt.Errorf("duplicate %s id %q", kind, id)
	}
	seen[id] = true
	return nil
}

func (d Defaults) check(harnesses map[string]Harness) error {
	harness, ok := harnesses[d.Harness]
	if !ok {
		return fmt.Errorf("harness %q is not configured", d.Harness)
	}
	if err := harness.CheckModel(d.Model, d.Effort); err != nil {
		return err
	}
	return harness.CheckPermissionMode(d.PermissionMode)
}

// CheckModel reports whether model is one of h's models and effort one of its
// effort levels, empty for a model that has none.
func (h Harness) CheckModel(model, effort string) error {
	for _, m := range h.Models {
		if m.ID != model {
			continue
		}
		if len(m.Efforts) == 0 && effort == "" || has(m.Efforts, effort) {
			return nil
		}
		return fmt.Errorf("effort %q is not one of %s's", effort, model)
	}
	return fmt.Errorf("model %q is not configured", model)
}

// Effort is the effort a session takes with model, given its current effort:
// current when model offers it, else model's first effort level, or empty
// for a model that has none.
func (h Harness) Effort(model, current string) string {
	for _, m := range h.Models {
		if m.ID != model {
			continue
		}
		if has(m.Efforts, current) {
			return current
		}
		if len(m.Efforts) > 0 {
			return m.Efforts[0].ID
		}
	}
	return ""
}

// Providers maps each of h's models that sets a provider order to that order.
func (h Harness) Providers() map[string][]string {
	providers := make(map[string][]string)
	for _, m := range h.Models {
		if len(m.Providers) > 0 {
			providers[m.ID] = m.Providers
		}
	}
	return providers
}

// CheckPermissionMode reports whether mode is one of h's permission modes.
func (h Harness) CheckPermissionMode(mode string) error {
	if !has(h.PermissionModes, mode) {
		return fmt.Errorf("permission mode %q is not configured", mode)
	}
	return nil
}

func has(options []Option, id string) bool {
	for _, o := range options {
		if o.ID == id {
			return true
		}
	}
	return false
}
