// Package settings reads and writes the global settings file
// `~/.config/ox/settings.json`, read once at startup, and the workspace settings
// file `.ox/settings.json`, read when a session becomes active. MCP settings in
// both files are read separately at session activation. Workspace keys
// replace the same global keys. Neither file is required. Only the global file
// can set `models`, the OpenRouter provider pins, and `hooks`, the commands Ox
// runs during turns. The terminal client's fields, `servers` and `favorites`,
// are read only from the global file. The launch settings in `OX_SETTINGS`
// apply over both files and are never saved.
package settings

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"ox/internal/catalog"
	"ox/internal/textfile"
	"ox/internal/transcript"
)

// builtInModel is used when no settings file sets `model`: an OpenRouter alias
// for the latest DeepSeek Flash model, so the catalog's release-date filter
// does not drop it as a fixed model ID eventually would.
const builtInModel = "openrouter:~deepseek/deepseek-flash-latest"

// LaunchVariable names the environment variable that holds the launch
// settings: a JSON object in the settings file format, read where the files
// are read. It lets a program that launches Ox configure one launch without
// changing the user's files.
const LaunchVariable = "OX_SETTINGS"

// Settings are a model, effort level, and session mode: the defaults for new
// sessions, or one session's selections.
type Settings struct {
	Model  string
	Effort catalog.Effort
	Mode   transcript.Mode
}

// Home returns `$HOME`, which holds `~/.config/ox` and the user skills
// directories.
func Home() (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", errors.New("HOME is not set")
	}
	return home, nil
}

// GlobalPath returns the global settings file in home.
func GlobalPath(home string) string {
	return filepath.Join(home, ".config/ox/settings.json")
}

// file is the format of both settings files.
type file struct {
	provider bool
	hooks    bool
	model    *string
	effort   *string
	mode     *string
	models   map[string]struct {
		Providers []string `json:"providers"`
	}
}

// Load reads the global settings file at path and pins each listed model's
// providers in cat.
func Load(path string, cat catalog.Catalog) (Settings, error) {
	f, present, err := readSettings(path)
	if err != nil {
		return Settings{}, err
	}
	settings := Settings{Model: builtInModel, Effort: catalog.EffortDefault, Mode: transcript.ModeAsk}
	// Without OpenRouter, whose models come first, the built-in model is
	// missing; use the first model.
	if len(cat) > 0 && cat[0].Provider != catalog.OpenRouter {
		settings.Model = cat[0].QualifiedID()
	}
	if present {
		if err := settings.apply(path, f); err != nil {
			return Settings{}, err
		}
	}
	if err := settings.validate(path, cat); err != nil {
		return Settings{}, err
	}
	for _, id := range slices.Sorted(maps.Keys(f.models)) {
		model := cat.Lookup(id)
		if model == nil {
			return Settings{}, invalid(path, "model %s in models is not in the model catalog", id)
		}
		if model.Provider != catalog.OpenRouter {
			return Settings{}, invalid(path, "model %s in models is not an OpenRouter model; only OpenRouter models accept providers", id)
		}
		providers := f.models[id].Providers
		if len(providers) == 0 {
			return Settings{}, invalid(path, "model %s lists no providers", id)
		}
		model.Providers = providers
	}
	return settings, nil
}

// ForWorkspace returns s with each key set in the workspace settings file of
// workspace, then in the launch settings, replaced. A missing file or unset
// variable changes nothing.
func (s Settings) ForWorkspace(workspace string, cat catalog.Catalog) (Settings, error) {
	source := ""
	for _, path := range []string{filepath.Join(workspace, ".ox/settings.json"), LaunchVariable} {
		f, present, err := readSettings(path)
		if err != nil {
			return Settings{}, err
		}
		if !present {
			continue
		}
		if f.models != nil {
			return Settings{}, invalid(path, "models can be set only in the global settings file")
		}
		if f.hooks && path != LaunchVariable {
			return Settings{}, invalid(path, "hooks can be set only in the global settings file")
		}
		if err := s.apply(path, f); err != nil {
			return Settings{}, err
		}
		source = path
	}
	if source == "" {
		return s, nil
	}
	return s, s.validate(source, cat)
}

// readSettings reads one settings file and rejects the obsolete provider key.
// present is false when the file does not exist.
func readSettings(path string) (f file, present bool, err error) {
	f, err = read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return file{}, false, nil
	}
	if err != nil {
		return file{}, false, err
	}
	if f.provider {
		return file{}, false, obsoleteProvider(path)
	}
	return f, true, nil
}

func (s *Settings) apply(path string, f file) error {
	if f.model != nil {
		s.Model = *f.model
	}
	if f.effort != nil {
		effort, ok := catalog.ParseEffort(*f.effort)
		if !ok {
			return invalid(path, "unknown effort %s", *f.effort)
		}
		s.Effort = effort
	}
	if f.mode != nil {
		mode, ok := transcript.ParseMode(*f.mode)
		if !ok {
			return invalid(path, "unknown mode %s", *f.mode)
		}
		s.Mode = mode
	}
	return nil
}

func (s Settings) validate(path string, cat catalog.Catalog) error {
	model := cat.Lookup(s.Model)
	if model == nil {
		return invalid(path, "model %s is not in the model catalog", s.Model)
	}
	if !model.Supports(s.Effort) {
		return invalid(path, "effort %s is not supported by model %s", s.Effort, s.Model)
	}
	return nil
}

// Save writes the selections to the workspace settings file when it exists,
// otherwise to the global file, keeping the file's other fields. It reports
// whether the global file was written.
func Save(home, workspace string, selected Settings) (bool, error) {
	path := filepath.Join(workspace, ".ox/settings.json")
	_, err := os.Stat(path)
	global := errors.Is(err, fs.ErrNotExist)
	if err != nil && !global {
		return false, err
	}
	if global {
		path = GlobalPath(home)
	}
	err = update(path, func(fields map[string]any) {
		fields["model"] = selected.Model
		fields["effort"] = selected.Effort
		fields["mode"] = selected.Mode
	})
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return global, nil
}

// update rewrites the settings file at path with set applied to its fields.
func update(path string, set func(fields map[string]any)) error {
	fields := map[string]any{}
	mode := os.FileMode(0o666)
	text, err := textfile.Read(path)
	present := err == nil
	if err == nil {
		if err := json.Unmarshal([]byte(text), &fields); err != nil || fields == nil {
			return errors.New("settings must be a JSON object")
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	set(fields)
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o777); err != nil {
		return err
	}
	temporary := filepath.Join(directory, ".settings-"+rand.Text()+".tmp")
	defer os.Remove(temporary)
	if err := os.WriteFile(temporary, append(data, '\n'), mode); err != nil {
		return err
	}
	if present {
		// Replacing an existing file must preserve its permissions despite umask.
		if err := os.Chmod(temporary, mode); err != nil {
			return err
		}
	}
	return os.Rename(temporary, path)
}

// readSource reads the settings file at path, or the launch settings when path
// is LaunchVariable. A missing file or unset variable is fs.ErrNotExist.
func readSource(path string) (string, error) {
	if path != LaunchVariable {
		return textfile.Read(path)
	}
	text, ok := os.LookupEnv(LaunchVariable)
	if !ok {
		return "", fs.ErrNotExist
	}
	return text, nil
}

// read reads one settings file. Every error except a missing file names it.
func read(path string) (file, error) {
	text, err := readSource(path)
	if errors.Is(err, fs.ErrNotExist) {
		return file{}, err
	}
	if err != nil {
		return file{}, fmt.Errorf("%s: %w", path, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &fields); err != nil {
		return file{}, fmt.Errorf("%s: %w", path, err)
	}
	if fields == nil {
		return file{}, invalid(path, "settings must be a JSON object")
	}
	var f file
	_, f.provider = fields["provider"]
	_, f.hooks = fields["hooks"]
	for key, target := range map[string]any{"model": &f.model, "effort": &f.effort, "mode": &f.mode, "models": &f.models} {
		if raw, ok := fields[key]; ok {
			if err := json.Unmarshal(raw, target); err != nil {
				return file{}, fmt.Errorf("%s: %s: %w", path, key, err)
			}
		}
	}
	return f, nil
}

func obsoleteProvider(path string) error {
	return invalid(path, "provider is no longer supported; use a provider-qualified model ID")
}

func invalid(path, format string, args ...any) error {
	return fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...))
}
