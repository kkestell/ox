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

// Session is a conversation with a harness.
type Session struct {
	ID             string
	Harness        string
	Model          string
	Effort         string
	PermissionMode string
	// ResumeToken is the harness's token for resuming the conversation,
	// empty until the harness has saved enough to resume it.
	ResumeToken string
	CreatedAt   time.Time
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
	harness         TEXT NOT NULL,
	model           TEXT NOT NULL,
	effort          TEXT NOT NULL,
	permission_mode TEXT NOT NULL,
	resume_token    TEXT NOT NULL,
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

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// Serialize this store's operations on one SQLite connection.
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

	rows, err = s.db.Query(`SELECT id, workspace_id, harness, model, effort, permission_mode, resume_token, created_at
		FROM sessions ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r := &Session{}
		var workspaceID, created string
		if err := rows.Scan(&r.ID, &workspaceID, &r.Harness, &r.Model, &r.Effort, &r.PermissionMode, &r.ResumeToken, &created); err != nil {
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
	_, err := s.db.Exec(`INSERT INTO sessions (id, workspace_id, harness, model, effort, permission_mode, resume_token, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, workspaceID, r.Harness, r.Model, r.Effort, r.PermissionMode, r.ResumeToken, formatTime(r.CreatedAt))
	return err
}

func (s *Store) SaveSession(r *Session) error {
	_, err := s.db.Exec(`UPDATE sessions SET model = ?, effort = ?, permission_mode = ?, resume_token = ? WHERE id = ?`,
		r.Model, r.Effort, r.PermissionMode, r.ResumeToken, r.ID)
	return err
}

func (s *Store) DeleteWorkspace(id string) error {
	_, err := s.db.Exec(`DELETE FROM workspaces WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) AddEvent(sessionID string, e Event) error {
	_, err := s.db.Exec(`INSERT INTO events (session_id, created_at, data) VALUES (?, ?, ?)`,
		sessionID, formatTime(e.CreatedAt), string(e.Data))
	return err
}

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
