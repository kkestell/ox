package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/kkestell/ox/internal/agent"
)

// tailscaleRange is the CGNAT range Tailscale assigns IPv4 addresses from.
var tailscaleRange = netip.MustParsePrefix("100.64.0.0/10")

//go:embed default.json
var defaultConfig []byte

type Config struct {
	Harnesses map[string]Harness `json:"harnesses"`
	Defaults  Defaults           `json:"defaults"`
	Titling   Titling            `json:"titling"`
	// MaxChildDepth is how many levels of child sessions a top-level session
	// can have beneath it.
	MaxChildDepth int `json:"max_child_depth"`
	// TailscaleIP, when valid, is this machine's Tailscale IPv4 address, where
	// ox also serves the web UI.
	TailscaleIP netip.Addr `json:"tailscale_ip,omitzero"`
}

type Defaults struct {
	Harness        string               `json:"harness"`
	Model          string               `json:"model"`
	Effort         string               `json:"effort"`
	PermissionMode agent.PermissionMode `json:"permission_mode"`
}

// Titling is the harness, model, and effort that title top-level sessions.
type Titling struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

type Harness struct {
	Models []Model `json:"models"`
	// ThinkingSummaries is whether Claude streams a summary of its thinking
	// to show in the transcript.
	ThinkingSummaries bool `json:"thinking_summaries,omitempty"`
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
		if err := writeDefault(path); err != nil {
			return nil, fmt.Errorf("write default configuration to %s: %w", path, err)
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
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
	if err := config.Titling.check(config.Harnesses); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if config.MaxChildDepth < 1 {
		return nil, fmt.Errorf("%s: max_child_depth must be at least 1", path)
	}
	if ip := config.TailscaleIP; ip.IsValid() && !tailscaleRange.Contains(ip) {
		return nil, fmt.Errorf("%s: tailscale_ip %s is not an IPv4 address in %s", path, ip, tailscaleRange)
	}
	return &config, nil
}

// writeDefault creates path with the built-in configuration. The file is
// published by linking a finished temporary file, so no reader sees a partial
// file, and a file that appears meanwhile is kept.
func writeDefault(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(defaultConfig); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(temp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// check requires models, and an ID and a name for every choice. The harness receives the ID as a flag. A model without effort levels is served an
// empty list.
func (h Harness) check() error {
	if len(h.Models) == 0 {
		return errors.New("needs models")
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
	return nil
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
	if d.PermissionMode == "" {
		return errors.New("permission_mode is required")
	}
	return nil
}

func (t Titling) check(harnesses map[string]Harness) error {
	if t.Harness == "" {
		return errors.New("titling is required: name the harness, model, and effort that title sessions")
	}
	harness, ok := harnesses[t.Harness]
	if !ok {
		return fmt.Errorf("titling: harness %q is not configured", t.Harness)
	}
	if err := harness.CheckModel(t.Model, t.Effort); err != nil {
		return fmt.Errorf("titling: %w", err)
	}
	return nil
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

func has(options []Option, id string) bool {
	for _, o := range options {
		if o.ID == id {
			return true
		}
	}
	return false
}
