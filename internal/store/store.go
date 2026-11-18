// Package store keeps the daemon's records of workspaces and their sessions
// in one JSON file. It holds no running state.
package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type State struct {
	Workspaces []*Workspace `json:"workspaces"`
}

type Workspace struct {
	ID       string     `json:"id"`
	Path     string     `json:"path"`
	Sessions []*Session `json:"sessions"`
}

// Session is a Claude conversation. Its ID is Claude's session ID.
type Session struct {
	ID             string `json:"id"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
	// Resumable records that Claude has written the session's transcript, so
	// the next process resumes it rather than starting it.
	Resumable bool `json:"resumable"`
}

// Path returns state.json in the ox directory of $XDG_STATE_HOME, or of
// ~/.local/state when that is unset.
func Path() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "ox", "state.json"), nil
}

// Load reads the state file. A missing file is an empty state.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// Save replaces the state file, so a reader sees either the old or the new
// state.
func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
