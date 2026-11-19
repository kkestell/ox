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
	if err := config.Defaults.check(config.Harnesses); err != nil {
		return nil, fmt.Errorf("%s: defaults: %w", path, err)
	}
	return &config, nil
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
