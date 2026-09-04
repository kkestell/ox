// Package settings owns Ox's settings files: the global
// $XDG_CONFIG_HOME/ox/settings.json, the per-workspace
// <workspace>/.ox/settings.json, and the resolution of the two into the
// model configuration one session sends with every request.
//
// The vocabulary is a deliberate allowlist of OpenRouter wire keys, so there is
// no translation layer and a settings file cannot name a base URL, a header, or
// a credential.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	directory    = "ox"
	file         = "settings.json"
	workspaceDir = ".ox"
)

// ModelSource names where the resolved model came from. The values read as
// error-message fragments as well as log values.
type ModelSource string

const (
	SourceEnvironment ModelSource = "OX_MODEL"
	SourceGlobal      ModelSource = "global settings"
	SourceWorkspace   ModelSource = "workspace settings"
)

// Config is one settings layer. Every field is a pointer or a slice so an
// omitted key is distinguishable from a zero value, which is what lets the
// workspace layer override one field without discarding the global one.
type Config struct {
	Model       *string    `json:"model,omitempty"`
	MaxTokens   *int       `json:"max_tokens,omitempty"`
	Temperature *float64   `json:"temperature,omitempty"`
	Reasoning   *Reasoning `json:"reasoning,omitempty"`
	Provider    *Provider  `json:"provider,omitempty"`

	// modelSource records which layer supplied Model. Merge sets it; a layer
	// straight from Load leaves it empty, so Resolve expects a merged Config.
	modelSource ModelSource
}

type Reasoning struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Effort  *string `json:"effort,omitempty"`
	Exclude *bool   `json:"exclude,omitempty"`
}

// Provider is the OpenRouter routing policy a settings file may set.
// require_parameters is deliberately absent: openrouter.Provider owns that.
type Provider struct {
	Order          []string  `json:"order,omitempty"`
	Only           []string  `json:"only,omitempty"`
	Ignore         []string  `json:"ignore,omitempty"`
	Quantizations  []string  `json:"quantizations,omitempty"`
	Sort           *string   `json:"sort,omitempty"`
	DataCollection *string   `json:"data_collection,omitempty"`
	AllowFallbacks *bool     `json:"allow_fallbacks,omitempty"`
	MaxPrice       *MaxPrice `json:"max_price,omitempty"`
}

type MaxPrice struct {
	Prompt     *float64 `json:"prompt,omitempty"`
	Completion *float64 `json:"completion,omitempty"`
}

// GlobalPath is the global settings path, $XDG_CONFIG_HOME/ox/settings.json
// falling back to $HOME/.config/ox/settings.json, or "" when neither yields
// an absolute base — in which case there is no global layer to load.
func GlobalPath(xdgConfigHome, home string) string {
	var base string
	if filepath.IsAbs(xdgConfigHome) {
		base = xdgConfigHome
	} else if home != "" {
		base = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(base) {
		return ""
	}
	return filepath.Join(base, directory, file)
}

// WorkspacePath is the workspace settings path, <dir>/.ox/settings.json.
// There is no upward walk: a parent's .ox belongs to another workspace.
func WorkspacePath(dir string) string {
	return filepath.Join(dir, workspaceDir, file)
}

// Load decodes one settings layer. A missing file — or an empty path — is not a
// layer at all and yields nil; a whitespace-only file is an empty layer. An
// unknown key is a hard error rather than a silently dropped setting, and every
// failure names the file.
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read settings file %s: %w", path, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return &Config{}, nil
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("parse settings file %s: %w", path, err)
	}
	return &config, nil
}
