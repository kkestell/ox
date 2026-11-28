package store

import (
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
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
	PermissionMode agent.PermissionMode
	// ResumeToken is the harness's token for resuming the conversation,
	// empty until the harness has saved enough to resume it.
	ResumeToken string
	// Directory is the absolute directory the harness runs in, fixed at
	// creation.
	Directory string
	CreatedAt time.Time
	// Child is set for a child session, nil for a top-level one.
	Child *Child
	// Creator is the ID of the session whose Claude started this top-level
	// session, empty for one the user created or whose creator was deleted.
	// Fixed at creation.
	Creator  string
	Archived bool
	// Pinned is set for a top-level session the user pinned.
	Pinned bool
	// ActiveAt is the time of the session's last event other than parked, or
	// CreatedAt when it has none.
	ActiveAt time.Time
	// Title is a top-level session's title: the one its creator gave it, or
	// else its generated title, empty until it has one.
	Title string
	// Worktree is the root of the git worktree a top-level session was
	// created in, empty for a session without one. Fixed at creation.
	Worktree string
}

// Child is what makes a session a child: its parent, its name among the
// parent's children, and its title.
type Child struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
	Title    string `json:"title"`
}

// Image is an image attached to a prompt.
type Image struct {
	ID       string
	MimeType string
	Data     []byte
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
	created_at      TEXT NOT NULL,
	directory       TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
	id         INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	data       TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS children (
	session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE,
	parent_id  TEXT NOT NULL REFERENCES sessions,
	name       TEXT NOT NULL,
	title      TEXT NOT NULL,
	UNIQUE (parent_id, name)
);
CREATE TABLE IF NOT EXISTS creators (
	session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE,
	creator_id TEXT NOT NULL REFERENCES sessions ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS archived (
	session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS pinned (
	session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS titles (
	session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE,
	title      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS worktrees (
	session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE,
	worktree   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS images (
	id         TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions ON DELETE CASCADE,
	mime_type  TEXT NOT NULL,
	data       BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS events_session ON events (session_id, id);
CREATE INDEX IF NOT EXISTS images_session ON images (session_id);
`

// Store holds its database exclusively, through a lock on a file beside it,
// from Open until Close.
type Store struct {
	db   *sql.DB
	lock *os.File
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// SQLite's own locks on the database file are POSIX locks, which a flock
	// on the same file conflicts with on macOS, so the lock has its own file.
	// The kernel releases it when the process exits.
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another oxd is using %s", path)
		}
		return nil, err
	}
	// SQLite's file URI decodes the path, rather than treating its punctuation
	// as the start of the DSN's options. ./ keeps special names like :memory:
	// from being interpreted as anything other than relative filenames.
	uri := "file:"
	if filepath.IsAbs(path) {
		uri += "//" // empty authority, then the absolute path
	} else {
		uri += "./"
	}
	dsn := uri + (&url.URL{Path: path}).EscapedPath() + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	// In WAL mode, synchronous(NORMAL) keeps every commit through a killed
	// process without waiting on fsync; only a power loss drops the latest.
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		lock.Close()
		return nil, err
	}
	// Serialize this store's operations on one SQLite connection.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		lock.Close()
		return nil, err
	}
	if err := upgrade(db); err != nil {
		db.Close()
		lock.Close()
		return nil, fmt.Errorf("upgrade database: %w", err)
	}
	// A session stored in a permission mode Ox no longer offers runs in
	// manual.
	if _, err := db.Exec(`UPDATE sessions SET permission_mode = 'manual' WHERE permission_mode NOT IN ('manual', 'auto')`); err != nil {
		db.Close()
		lock.Close()
		return nil, err
	}
	return &Store{db, lock}, nil
}

// upgrade moves a database whose children alone stored a directory to one
// where every session stores its own: a top-level session's is its
// workspace's path.
func upgrade(db *sql.DB) error {
	var upgraded bool
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('sessions') WHERE name = 'directory'`).Scan(&upgraded); err != nil {
		return err
	}
	if upgraded {
		return nil
	}
	return withTx(db, func(tx *sql.Tx) error {
		for _, statement := range []string{
			`ALTER TABLE sessions ADD COLUMN directory TEXT NOT NULL DEFAULT ''`,
			`UPDATE sessions SET directory = (SELECT path FROM workspaces WHERE id = sessions.workspace_id)`,
			`UPDATE sessions SET directory = (SELECT directory FROM children WHERE session_id = sessions.id)
				WHERE id IN (SELECT session_id FROM children)`,
			`ALTER TABLE children DROP COLUMN directory`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
		return nil
	})
}

// withTx runs f in a transaction that commits only when f returns nil.
func withTx(db *sql.DB, f func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error {
	err := s.db.Close()
	s.lock.Close()
	return err
}

func (s *Store) Workspaces() ([]*Workspace, error) {
	workspaces, err := s.loadWorkspaces()
	if err != nil {
		return nil, err
	}
	byID := map[string]*Workspace{}
	for _, w := range workspaces {
		byID[w.ID] = w
	}

	rows, err := s.db.Query(`SELECT s.id, s.workspace_id, s.harness, s.model, s.effort, s.permission_mode, s.resume_token, s.created_at, s.directory,
			c.parent_id, c.name, c.title, coalesce(cr.creator_id, ''), a.session_id IS NOT NULL, coalesce(t.title, ''), coalesce(wt.worktree, ''),
			p.session_id IS NOT NULL,
			coalesce((SELECT e.created_at FROM events e WHERE e.session_id = s.id AND json_extract(e.data, '$.type') != 'parked'
				ORDER BY e.id DESC LIMIT 1), s.created_at)
		FROM sessions s LEFT JOIN children c ON c.session_id = s.id LEFT JOIN creators cr ON cr.session_id = s.id
			LEFT JOIN archived a ON a.session_id = s.id LEFT JOIN pinned p ON p.session_id = s.id LEFT JOIN titles t ON t.session_id = s.id LEFT JOIN worktrees wt ON wt.session_id = s.id
		ORDER BY s.created_at, s.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r := &Session{}
		var workspaceID, created, active string
		var parentID, name, title sql.NullString
		if err := rows.Scan(&r.ID, &workspaceID, &r.Harness, &r.Model, &r.Effort, &r.PermissionMode, &r.ResumeToken, &created, &r.Directory,
			&parentID, &name, &title, &r.Creator, &r.Archived, &r.Title, &r.Worktree,
			&r.Pinned, &active); err != nil {
			return nil, err
		}
		if parentID.Valid {
			r.Child = &Child{ParentID: parentID.String, Name: name.String, Title: title.String}
		}
		if r.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if r.ActiveAt, err = parseTime(active); err != nil {
			return nil, err
		}
		w := byID[workspaceID]
		w.Sessions = append(w.Sessions, r)
	}
	return workspaces, rows.Err()
}

// loadWorkspaces reads the workspaces without their sessions. It closes its
// result set before it returns, so the caller's next query finds the store's
// one connection free.
func (s *Store) loadWorkspaces() ([]*Workspace, error) {
	rows, err := s.db.Query(`SELECT id, path, created_at FROM workspaces ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	workspaces := []*Workspace{}
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
	}
	return workspaces, rows.Err()
}

func (s *Store) AddWorkspace(w *Workspace) error {
	_, err := s.db.Exec(`INSERT INTO workspaces (id, path, created_at) VALUES (?, ?, ?)`,
		w.ID, w.Path, formatTime(w.CreatedAt))
	return err
}

// AddSession stores a session, and with it a child session's child row, a
// started session's creator, and a session's title and worktree.
func (s *Store) AddSession(workspaceID string, r *Session) error {
	if !filepath.IsAbs(r.Directory) {
		return errors.New("session directory must be an absolute path")
	}
	if r.Child != nil && r.Worktree != "" {
		return errors.New("a child session cannot have a worktree")
	}
	if r.Child != nil && r.Creator != "" {
		return errors.New("a child session cannot have a creator")
	}
	return withTx(s.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO sessions (id, workspace_id, harness, model, effort, permission_mode, resume_token, created_at, directory)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, workspaceID, r.Harness, r.Model, r.Effort, r.PermissionMode, r.ResumeToken, formatTime(r.CreatedAt), r.Directory); err != nil {
			return err
		}
		if c := r.Child; c != nil {
			if _, err := tx.Exec(`INSERT INTO children (session_id, parent_id, name, title) VALUES (?, ?, ?, ?)`,
				r.ID, c.ParentID, c.Name, c.Title); err != nil {
				return err
			}
		}
		if r.Creator != "" {
			if _, err := tx.Exec(`INSERT INTO creators (session_id, creator_id) VALUES (?, ?)`, r.ID, r.Creator); err != nil {
				return err
			}
		}
		if r.Title != "" {
			if _, err := tx.Exec(`INSERT INTO titles (session_id, title) VALUES (?, ?)`, r.ID, r.Title); err != nil {
				return err
			}
		}
		if r.Worktree != "" {
			if _, err := tx.Exec(`INSERT INTO worktrees (session_id, worktree) VALUES (?, ?)`, r.ID, r.Worktree); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) SaveSession(r *Session) error {
	_, err := s.db.Exec(`UPDATE sessions SET model = ?, effort = ?, permission_mode = ?, resume_token = ? WHERE id = ?`,
		r.Model, r.Effort, r.PermissionMode, r.ResumeToken, r.ID)
	return err
}

// SetTitle stores a top-level session's generated title.
func (s *Store) SetTitle(sessionID, title string) error {
	_, err := s.db.Exec(`INSERT INTO titles (session_id, title) VALUES (?, ?)`, sessionID, title)
	return err
}

// SetArchived archives or unarchives the sessions together.
func (s *Store) SetArchived(ids []string, archived bool) error {
	query := `DELETE FROM archived WHERE session_id = ?`
	if archived {
		query = `INSERT OR IGNORE INTO archived (session_id) VALUES (?)`
	}
	return withTx(s.db, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.Exec(query, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetPinned pins or unpins the session.
func (s *Store) SetPinned(id string, pinned bool) error {
	query := `DELETE FROM pinned WHERE session_id = ?`
	if pinned {
		query = `INSERT OR IGNORE INTO pinned (session_id) VALUES (?)`
	}
	_, err := s.db.Exec(query, id)
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

// ToolNames returns the distinct names of the tool calls the session's events
// record.
func (s *Store) ToolNames(sessionID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT json_extract(data, '$.tool_name') FROM events WHERE session_id = ?
		AND json_extract(data, '$.type') = 'tool_use'`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// LastEvent returns the data of the session's latest event, or nil when it
// has none.
func (s *Store) LastEvent(sessionID string) ([]byte, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM events WHERE session_id = ? ORDER BY id DESC LIMIT 1`, sessionID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return []byte(data), err
}

// UserPrompts yields the text of each prompt event the user sent the session,
// which has no sender or reports, oldest first; a prompt of images alone has text "".
// The caller makes no other store call until it stops.
func (s *Store) UserPrompts(sessionID string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		rows, err := s.db.Query(`SELECT coalesce(json_extract(data, '$.text'), '') FROM events WHERE session_id = ?
			AND json_extract(data, '$.type') = 'prompt' AND json_extract(data, '$.from') IS NULL
			AND json_extract(data, '$.reports') IS NULL ORDER BY id`, sessionID)
		if err != nil {
			yield("", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err != nil {
				yield("", err)
				return
			}
			if !yield(text, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield("", err)
		}
	}
}

// AddImages stores a session's images together.
func (s *Store) AddImages(sessionID string, images []Image) error {
	return withTx(s.db, func(tx *sql.Tx) error {
		for _, image := range images {
			if _, err := tx.Exec(`INSERT INTO images (id, session_id, mime_type, data) VALUES (?, ?, ?, ?)`,
				image.ID, sessionID, image.MimeType, image.Data); err != nil {
				return err
			}
		}
		return nil
	})
}

// Image returns the session's image with the ID, or nil when it has none.
func (s *Store) Image(sessionID, id string) (*Image, error) {
	image := &Image{ID: id}
	err := s.db.QueryRow(`SELECT mime_type, data FROM images WHERE session_id = ? AND id = ?`, sessionID, id).
		Scan(&image.MimeType, &image.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return image, err
}

// DeleteImages deletes the images with the IDs.
func (s *Store) DeleteImages(ids []string) error {
	return withTx(s.db, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.Exec(`DELETE FROM images WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Times are stored as fixed-width UTC RFC 3339 text, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(timeLayout, s)
}
