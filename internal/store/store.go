// Package store keeps the daemon's workspaces, sessions, and session events
// in a SQLite database. It holds no running state.
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Workspace struct {
	ID        string
	Path      string
	CreatedAt time.Time
	Sessions  []*Session
}

// Session is a Claude conversation. Its ID is Claude's session ID.
type Session struct {
	ID             string
	Model          string
	Effort         string
	PermissionMode string
	// Resumable records that Claude has written the session's transcript, so
	// the next process resumes it rather than starting it.
	Resumable bool
	CreatedAt time.Time
}

// Event is one recorded session update, as JSON.
type Event struct {
	CreatedAt time.Time
	Data      []byte
}

const schema = `
CREATE TABLE IF NOT EXISTS workspaces (
	id         TEXT PRIMARY KEY,
	path       TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	id              TEXT PRIMARY KEY,
	workspace_id    TEXT NOT NULL REFERENCES workspaces ON DELETE CASCADE,
	model           TEXT NOT NULL,
	effort          TEXT NOT NULL,
	permission_mode TEXT NOT NULL,
	resumable       INTEGER NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
	id         INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	data       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_session ON events (session_id, id);
`

type Store struct {
	db *sql.DB
}

// Path returns ox.db in the ox directory of $XDG_STATE_HOME, or of
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
	return filepath.Join(dir, "ox", "ox.db"), nil
}

// Open opens the database at path, creating it and its tables if needed.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// One connection serializes every statement, so writers never meet a
	// locked database.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Workspaces returns every workspace with its sessions, each in creation
// order.
func (s *Store) Workspaces() ([]*Workspace, error) {
	rows, err := s.db.Query(`SELECT id, path, created_at FROM workspaces ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	workspaces := []*Workspace{}
	byID := map[string]*Workspace{}
	for rows.Next() {
		w := &Workspace{Sessions: []*Session{}}
		var created string
		if err := rows.Scan(&w.ID, &w.Path, &created); err != nil {
			return nil, err
		}
		if w.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		workspaces = append(workspaces, w)
		byID[w.ID] = w
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.Query(`SELECT id, workspace_id, model, effort, permission_mode, resumable, created_at
		FROM sessions ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r := &Session{}
		var workspaceID, created string
		if err := rows.Scan(&r.ID, &workspaceID, &r.Model, &r.Effort, &r.PermissionMode, &r.Resumable, &created); err != nil {
			return nil, err
		}
		if r.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		w := byID[workspaceID]
		w.Sessions = append(w.Sessions, r)
	}
	return workspaces, rows.Err()
}

func (s *Store) AddWorkspace(w *Workspace) error {
	_, err := s.db.Exec(`INSERT INTO workspaces (id, path, created_at) VALUES (?, ?, ?)`,
		w.ID, w.Path, formatTime(w.CreatedAt))
	return err
}

func (s *Store) AddSession(workspaceID string, r *Session) error {
	_, err := s.db.Exec(`INSERT INTO sessions (id, workspace_id, model, effort, permission_mode, resumable, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, workspaceID, r.Model, r.Effort, r.PermissionMode, r.Resumable, formatTime(r.CreatedAt))
	return err
}

// SaveSession stores a session's settings and whether it is resumable.
func (s *Store) SaveSession(r *Session) error {
	_, err := s.db.Exec(`UPDATE sessions SET model = ?, effort = ?, permission_mode = ?, resumable = ? WHERE id = ?`,
		r.Model, r.Effort, r.PermissionMode, r.Resumable, r.ID)
	return err
}

// DeleteSession deletes a session and its events.
func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) AddEvent(sessionID string, e Event) error {
	_, err := s.db.Exec(`INSERT INTO events (session_id, created_at, data) VALUES (?, ?, ?)`,
		sessionID, formatTime(e.CreatedAt), string(e.Data))
	return err
}

// Events returns a session's events, oldest first.
func (s *Store) Events(sessionID string) ([]Event, error) {
	rows, err := s.db.Query(`SELECT created_at, data FROM events WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var created, data string
		if err := rows.Scan(&created, &data); err != nil {
			return nil, err
		}
		at, err := parseTime(created)
		if err != nil {
			return nil, err
		}
		events = append(events, Event{at, []byte(data)})
	}
	return events, rows.Err()
}

// Times are stored as fixed-width UTC RFC 3339 text, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(timeLayout, s)
}
