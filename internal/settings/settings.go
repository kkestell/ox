// Package settings owns Ox's settings files: the global
// $XDG_CONFIG_HOME/ox/settings.json, the per-workspace
// <workspace>/.ox/settings.json, and the resolution of the two into the
// model configuration one session sends with every request.
//
// The model vocabulary is a deliberate allowlist of OpenRouter wire keys. The
// global-only process object separately owns process authority such as the
// provider endpoint; no settings layer can name a header or credential.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	directory                = "ox"
	file                     = "settings.json"
	workspaceDir             = ".ox"
	DefaultLogLevel          = "info"
	DefaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
)

// ModelSource names where the resolved model came from. The values read as
// error-message fragments as well as log values.
type ModelSource string

const (
	SourceCLI       ModelSource = "--model"
	SourceGlobal    ModelSource = "global settings"
	SourceWorkspace ModelSource = "workspace settings"
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
	// straight from a loader leaves it empty, so Resolve expects a merged Config.
	modelSource ModelSource
}

// Process contains settings that apply to the whole Ox process. It is accepted
// only in the global settings file; a workspace cannot choose process behavior.
type Process struct {
	LogLevel          *string `json:"log_level,omitempty"`
	OpenRouterBaseURL *string `json:"openrouter_base_url,omitempty"`
	Trace             *string `json:"trace,omitempty"`
}

type globalFile struct {
	Config
	Process *Process `json:"process,omitempty"`
}

// ProcessOverrides distinguishes omitted CLI flags from explicit values.
type ProcessOverrides struct {
	LogLevel          *string
	OpenRouterBaseURL *string
	Trace             *string
}

// ResolvedProcess is the startup configuration after CLI precedence and
// defaults have been applied.
type ResolvedProcess struct {
	LogLevel          string
	OpenRouterBaseURL string
	Trace             string
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

// load decodes one settings layer. A missing file — or an empty path — is not a
// layer at all and yields nil; a whitespace-only file is an empty layer. An
// unknown key is a hard error rather than a silently dropped setting, and every
// failure names the file.
func load(path string, target any) (bool, error) {
	if path == "" {
		return false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read settings file %s: %w", path, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return true, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, fmt.Errorf("parse settings file %s: %w", path, err)
	}
	return true, nil
}

// LoadGlobal decodes the global model and process settings layer.
func LoadGlobal(path string) (*Config, *Process, error) {
	var value globalFile
	present, err := load(path, &value)
	if err != nil || !present {
		return nil, nil, err
	}
	return &value.Config, value.Process, nil
}

// LoadWorkspace decodes a workspace model settings layer. The Config
// vocabulary deliberately excludes process, so DisallowUnknownFields rejects
// attempts to set process authority from a workspace.
func LoadWorkspace(path string) (*Config, error) {
	var value Config
	present, err := load(path, &value)
	if err != nil || !present {
		return nil, err
	}
	return &value, nil
}

// ResolveProcess applies CLI overrides over the global process object and then
// validates the complete startup configuration.
func ResolveProcess(config *Process, overrides ProcessOverrides) (ResolvedProcess, error) {
	if config == nil {
		config = &Process{}
	}
	resolved := ResolvedProcess{
		LogLevel:          DefaultLogLevel,
		OpenRouterBaseURL: DefaultOpenRouterBaseURL,
	}
	if config.LogLevel != nil {
		resolved.LogLevel = *config.LogLevel
	}
	if config.OpenRouterBaseURL != nil {
		resolved.OpenRouterBaseURL = *config.OpenRouterBaseURL
	}
	if config.Trace != nil {
		resolved.Trace = *config.Trace
	}
	resolved, err := validateProcess(resolved, config.Trace != nil)
	if err != nil {
		return ResolvedProcess{}, err
	}
	if overrides.LogLevel != nil {
		resolved.LogLevel = *overrides.LogLevel
	}
	if overrides.OpenRouterBaseURL != nil {
		resolved.OpenRouterBaseURL = *overrides.OpenRouterBaseURL
	}
	if overrides.Trace != nil {
		resolved.Trace = *overrides.Trace
	}

	return validateProcess(resolved, config.Trace != nil || overrides.Trace != nil)
}

func validateProcess(resolved ResolvedProcess, traceSet bool) (ResolvedProcess, error) {
	resolved.LogLevel = strings.ToLower(strings.TrimSpace(resolved.LogLevel))
	switch resolved.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return ResolvedProcess{}, fmt.Errorf(
			"log level must be debug, info, warn, or error, got %q",
			resolved.LogLevel,
		)
	}
	resolved.OpenRouterBaseURL = strings.TrimSpace(resolved.OpenRouterBaseURL)
	endpoint, err := url.Parse(resolved.OpenRouterBaseURL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return ResolvedProcess{}, fmt.Errorf(
			"OpenRouter base URL must be an absolute HTTP(S) URL without credentials, query, or fragment, got %q",
			resolved.OpenRouterBaseURL,
		)
	}
	if traceSet {
		resolved.Trace = strings.TrimSpace(resolved.Trace)
		if resolved.Trace == "" {
			return ResolvedProcess{}, errors.New("trace path must not be blank")
		}
	}
	return resolved, nil
}
