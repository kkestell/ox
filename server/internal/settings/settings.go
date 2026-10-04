// Package settings reads and writes the global settings file
// `~/.config/ox/settings.json`, read once at startup, and the workspace settings
// file `.ox/settings.json`, read when a session becomes active. Workspace keys
// replace the same global keys. Neither file is required. Only the global file
// can set `models`, the OpenRouter provider pins. The Ox client reads and writes
// other fields of the global file.
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
			return Settings{}, invalid(path, "model %s in models is not in the OpenRouter model catalog", id)
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
// workspace replaced. A missing file changes nothing.
func (s Settings) ForWorkspace(workspace string, cat catalog.Catalog) (Settings, error) {
	path := filepath.Join(workspace, ".ox/settings.json")
	f, present, err := readSettings(path)
	if err != nil {
		return Settings{}, err
	}
	if !present {
		return s, nil
	}
	if f.models != nil {
		return Settings{}, invalid(path, "models can be set only in the global settings file")
	}
	if err := s.apply(path, f); err != nil {
		return Settings{}, err
	}
	return s, s.validate(path, cat)
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
	if err := write(path, selected); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return global, nil
}

func write(path string, selected Settings) error {
	fields := map[string]any{}
	text, err := textfile.Read(path)
	if err == nil {
		if err := json.Unmarshal([]byte(text), &fields); err != nil || fields == nil {
			return errors.New("settings must be a JSON object")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fields["model"] = selected.Model
	fields["effort"] = selected.Effort
	fields["mode"] = selected.Mode
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o777); err != nil {
		return err
	}
	temporary := filepath.Join(directory, ".settings-"+rand.Text()+".tmp")
	if err := os.WriteFile(temporary, append(data, '\n'), 0o666); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return err
	}
	return nil
}

// read reads one settings file. Every error except a missing file names it.
func read(path string) (file, error) {
	text, err := textfile.Read(path)
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
