// Package paths places Ox's files in the XDG base directories, each beneath
// an ox directory. Whoever creates a file creates its directory.
package paths

import (
	"os"
	"path/filepath"
)

// Config is the configuration file, beneath $XDG_CONFIG_HOME or ~/.config.
func Config() (string, error) {
	return file("XDG_CONFIG_HOME", ".config", "config.json")
}

// Database is the database file, beneath $XDG_STATE_HOME or ~/.local/state.
func Database() (string, error) {
	return file("XDG_STATE_HOME", filepath.Join(".local", "state"), "ox.db")
}

// Ur is the directory of Ur's conversation files, beneath $XDG_STATE_HOME or
// ~/.local/state.
func Ur() (string, error) {
	return file("XDG_STATE_HOME", filepath.Join(".local", "state"), "ur")
}

// Worktrees is the directory of session worktrees, beneath $XDG_DATA_HOME or
// ~/.local/share.
func Worktrees() (string, error) {
	return file("XDG_DATA_HOME", filepath.Join(".local", "share"), "worktrees")
}

// Socket is oxd's socket, beneath $XDG_RUNTIME_DIR, or beside the database
// when that is unset, as it is on macOS.
func Socket() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "ox", "oxd.sock"), nil
	}
	return file("XDG_STATE_HOME", filepath.Join(".local", "state"), "oxd.sock")
}

// file is name beneath the directory in variable, or beneath fallback in the
// home directory when variable is unset.
func file(variable, fallback, name string) (string, error) {
	dir := os.Getenv(variable)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, fallback)
	}
	return filepath.Join(dir, "ox", name), nil
}
