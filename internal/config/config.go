package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed default.json
var defaultConfig []byte

type Config struct {
	Harnesses map[string]Harness `json:"harnesses"`
	Defaults  Defaults           `json:"defaults"`
}

type Defaults struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	Mode    string `json:"mode"`
}

type Harness struct {
	Models []Model  `json:"models"`
	Modes  []Option `json:"modes"`
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

func Path() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "ox", "config.json"), nil
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
	var model *Model
	for i := range harness.Models {
		if harness.Models[i].ID == d.Model {
			model = &harness.Models[i]
		}
	}
	switch {
	case model == nil:
		return fmt.Errorf("model %q is not one of %s's", d.Model, d.Harness)
	case len(model.Efforts) == 0 && d.Effort != "", len(model.Efforts) > 0 && !has(model.Efforts, d.Effort):
		return fmt.Errorf("effort %q is not one of %s's", d.Effort, d.Model)
	case !has(harness.Modes, d.Mode):
		return fmt.Errorf("mode %q is not one of %s's", d.Mode, d.Harness)
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
