// Package config owns Ox's configuration files and resolves the model for one
// session. The process environment overrides the workspace file, which
// overrides the global file.
package config

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

// layer is one configuration file. Model is a pointer so an omitted key is
// distinguishable from a model whose value is blank and therefore invalid.
type layer struct {
	Model *string `json:"model,omitempty"`
}

// Source names the layer that supplied the resolved model. Its values also
// read naturally in log messages.
type Source string

const (
	SourceEnvironment Source = "OX_MODEL"
	SourceWorkspace   Source = "workspace configuration"
	SourceGlobal      Source = "global configuration"
)

// Environment is the configuration contributed by the process environment.
type Environment struct {
	GlobalPath    string
	ModelOverride string
}

// Resolved is the frozen configuration one session uses for its lifetime.
type Resolved struct {
	Model       string
	ModelSource Source
}

// GlobalPath returns $XDG_CONFIG_HOME/ox/config.json, falling back to
// $HOME/.config/ox/config.json. It returns "" when neither input yields an
// absolute base, in which case there is no global layer to consult.
func GlobalPath(xdgConfigHome, home string) string {
	var base string
	switch {
	case filepath.IsAbs(xdgConfigHome):
		base = xdgConfigHome
	case filepath.IsAbs(home):
		base = filepath.Join(home, ".config")
	default:
		return ""
	}
	return filepath.Join(base, "ox", "config.json")
}

// workspacePath returns <dir>/.ox/config.json. There is deliberately no upward
// walk: a parent's .ox directory belongs to a different workspace.
func workspacePath(dir string) string {
	return filepath.Join(dir, ".ox", "config.json")
}

// load decodes one configuration layer. A missing file or empty path is not a
// layer; a whitespace-only file is an empty layer. Every failure names the
// file, and unknown keys are rejected rather than silently ignored.
func load(path string) (*layer, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read configuration file %s: %w", path, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return &layer{}, nil
	}

	var decoded layer
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("parse configuration file %s: %w", path, err)
	}
	return &decoded, nil
}

// Resolve reads the global and workspace layers and resolves the model for one
// session. Precedence is OX_MODEL, workspace configuration, then global
// configuration.
func Resolve(environment Environment, cwd string) (Resolved, error) {
	global, err := load(environment.GlobalPath)
	if err != nil {
		return Resolved{}, err
	}
	workspaceFile := workspacePath(cwd)
	workspace, err := load(workspaceFile)
	if err != nil {
		return Resolved{}, err
	}

	if err := validateModel(global, environment.GlobalPath); err != nil {
		return Resolved{}, err
	}
	if err := validateModel(workspace, workspaceFile); err != nil {
		return Resolved{}, err
	}

	switch override := strings.TrimSpace(environment.ModelOverride); {
	case override != "":
		return Resolved{Model: override, ModelSource: SourceEnvironment}, nil
	case workspace != nil && workspace.Model != nil:
		return Resolved{
			Model:       strings.TrimSpace(*workspace.Model),
			ModelSource: SourceWorkspace,
		}, nil
	case global != nil && global.Model != nil:
		return Resolved{
			Model:       strings.TrimSpace(*global.Model),
			ModelSource: SourceGlobal,
		}, nil
	}

	consulted := make([]string, 0, 2)
	if environment.GlobalPath != "" {
		consulted = append(consulted, environment.GlobalPath)
	}
	consulted = append(consulted, workspaceFile)
	return Resolved{}, errors.New(
		`no model is configured: set OX_MODEL or set "model" in ` +
			strings.Join(consulted, " or "),
	)
}

func validateModel(decoded *layer, path string) error {
	if decoded == nil || decoded.Model == nil {
		return nil
	}
	if strings.TrimSpace(*decoded.Model) == "" {
		return fmt.Errorf(`configuration file %s: "model" must not be blank`, path)
	}
	return nil
}
