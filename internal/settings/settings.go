// Package settings owns Ox's settings files: the global
// $XDG_CONFIG_HOME/ox/settings.json, the per-workspace
// <workspace>/.ox/settings.json, and the resolution of the two into the model
// profiles a session may select and the request configuration each one sends.
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
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kkestell/ox/internal/lsp"
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
	SourceSession   ModelSource = "session model option"
)

// Config is one settings layer: the model profiles it defines and the profile a
// session starts on.
type Config struct {
	DefaultModel *string                `json:"default_model,omitempty"`
	Models       map[string]ModelConfig `json:"models,omitempty"`

	// defaultSource records which layer supplied DefaultModel. Merge sets it; a
	// layer straight from a loader leaves it empty, so Resolve expects a merged
	// Config.
	defaultSource ModelSource
}

// ModelConfig is one model profile, keyed elsewhere by its exact OpenRouter
// model ID. Every field is a pointer or a slice so an omitted key is
// distinguishable from a zero value, which is what lets the workspace layer
// override one field without discarding the global one.
type ModelConfig struct {
	MaxTokens   *int       `json:"max_tokens,omitempty"`
	Temperature *float64   `json:"temperature,omitempty"`
	Reasoning   *Reasoning `json:"reasoning,omitempty"`
	Provider    *Provider  `json:"provider,omitempty"`
}

// Process contains settings that apply to the whole Ox process. It is accepted
// only in the global settings file; a workspace cannot choose process behavior.
type Process struct {
	LogLevel          *string                   `json:"log_level,omitempty"`
	OpenRouterBaseURL *string                   `json:"openrouter_base_url,omitempty"`
	Trace             *string                   `json:"trace,omitempty"`
	LanguageServers   map[string]LanguageServer `json:"language_servers,omitempty"`
}

// LanguageServer is one entry of the global language_servers object, keyed by
// the stable name the server is reported under.
type LanguageServer struct {
	Command    *string  `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
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
	// LanguageServers is ordered by name so a startup log and every session
	// activation see the same sequence.
	LanguageServers []lsp.Definition
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

	resolved, err = validateProcess(resolved, config.Trace != nil || overrides.Trace != nil)
	if err != nil {
		return ResolvedProcess{}, err
	}
	resolved.LanguageServers, err = resolveLanguageServers(config.LanguageServers)
	if err != nil {
		return ResolvedProcess{}, err
	}
	return resolved, nil
}

// resolveLanguageServers validates the global language_servers object and
// returns it as an ordered slice. Two servers cannot claim the same extension,
// because the file being queried is the only thing that selects a server.
func resolveLanguageServers(configured map[string]LanguageServer) ([]lsp.Definition, error) {
	if len(configured) == 0 {
		return nil, nil
	}
	names := slices.Sorted(maps.Keys(configured))
	owner := make(map[string]string, len(configured))
	resolved := make([]lsp.Definition, 0, len(configured))
	for _, name := range names {
		server := configured[name]
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return nil, errors.New(`"language_servers" must not contain a blank server name`)
		}
		command := ""
		if server.Command != nil {
			command = strings.TrimSpace(*server.Command)
		}
		if command == "" {
			return nil, fmt.Errorf(`"language_servers.%s.command" must not be blank`, trimmed)
		}
		for _, argument := range server.Args {
			if strings.TrimSpace(argument) == "" {
				return nil, fmt.Errorf(`"language_servers.%s.args" must not contain a blank entry`, trimmed)
			}
		}
		if len(server.Extensions) == 0 {
			return nil, fmt.Errorf(`"language_servers.%s.extensions" must name at least one extension`, trimmed)
		}
		extensions := make([]string, 0, len(server.Extensions))
		for _, extension := range server.Extensions {
			normalized := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(extension), "."))
			if normalized == "" || strings.ContainsAny(normalized, `/\`) {
				return nil, fmt.Errorf(
					`"language_servers.%s.extensions" contains invalid extension %q`,
					trimmed, extension,
				)
			}
			if previous, taken := owner[normalized]; taken {
				return nil, fmt.Errorf(
					`language servers %q and %q both claim extension %q`,
					previous, trimmed, normalized,
				)
			}
			owner[normalized] = trimmed
			extensions = append(extensions, normalized)
		}
		resolved = append(resolved, lsp.Definition{
			Name:       trimmed,
			Command:    command,
			Args:       slices.Clone(server.Args),
			Extensions: extensions,
		})
	}
	return resolved, nil
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
