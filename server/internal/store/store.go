// Package store keeps session transcripts in one SQLite database at
// `{data}/ox.db`: `$OX_DATA_DIR`, else `$XDG_DATA_HOME/ox`, else
// `~/.local/share/ox`.
//
// Timestamps are RFC 3339 UTC with millisecond precision, so they sort
// lexicographically and `ORDER BY updated_at` needs no date parsing.
package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/ncruces/go-sqlite3/driver"

	"ox/internal/transcript"
)

// maxTitleChars is the longest session title derived from a prompt.
const maxTitleChars = 80

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id             TEXT PRIMARY KEY,
    workspace_path TEXT NOT NULL,
    title          TEXT,
    updated_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_by_activity ON sessions (updated_at DESC, id);

CREATE TABLE IF NOT EXISTS transcript_entries (
    id         INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    data       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS transcript_entries_by_session
    ON transcript_entries (session_id, id);
`

// ErrNotFound means the session does not exist.
var ErrNotFound = errors.New("session does not exist")

// ErrRelativeWorkspace means a workspace path is not absolute.
var ErrRelativeWorkspace = errors.New("workspace path is not absolute")

// Summary describes one session.
type Summary struct {
	ID        string
	Workspace string
	// Title is empty until the first nonblank prompt names the session.
	Title     string
	UpdatedAt string
}

// Session is a session's summary and transcript.
type Session struct {
	Summary    Summary
	Transcript []transcript.Entry
}

// Store is the session database.
type Store struct {
	db *sql.DB
}

// DatabasePath returns the database location.
func DatabasePath() (string, error) {
	if dir := os.Getenv("OX_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "ox.db"), nil
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "ox", "ox.db"), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", errors.New("HOME is not set")
	}
	return filepath.Join(home, ".local/share/ox/ox.db"), nil
}

// Open opens or creates the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return nil, err
	}
	location := url.URL{Scheme: "file", OmitHost: true, Path: path}
	return open(location.String() + "?_pragma=foreign_keys(1)&_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
}

// OpenMemory opens an empty in-memory database.
func OpenMemory() (*Store, error) {
	return open("file::memory:?_pragma=foreign_keys(1)")
}

func open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	// One connection serializes writes and keeps an in-memory database alive.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Create creates an empty session. Workspaces are compared as the exact
// absolute path the client supplied.
func (s *Store) Create(workspace string) (Summary, error) {
	if !filepath.IsAbs(workspace) {
		return Summary{}, fmt.Errorf("%w: %s", ErrRelativeWorkspace, workspace)
	}
	summary := Summary{ID: newID(), Workspace: workspace, UpdatedAt: now()}
	_, err := s.db.Exec("INSERT INTO sessions (id, workspace_path, updated_at) VALUES (?, ?, ?)",
		summary.ID, summary.Workspace, summary.UpdatedAt)
	return summary, err
}

// Read returns a session, or ErrNotFound. A malformed transcript is an error.
func (s *Store) Read(id string) (*Session, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	summary, err := readSummary(tx, id)
	if err != nil {
		return nil, err
	}
	entries, err := readTranscript(tx, id)
	if err == nil {
		err = transcript.Validate(entries)
	}
	if err != nil {
		return nil, fmt.Errorf("session %s has an invalid transcript: %w", id, err)
	}
	return &Session{Summary: summary, Transcript: entries}, nil
}

// List returns the sessions in workspace, or every session when workspace is
// empty, most recently active first. Ties are broken by ID.
func (s *Store) List(workspace string) ([]Summary, error) {
	rows, err := s.db.Query(`SELECT id, workspace_path, COALESCE(title, ''), updated_at FROM sessions
		WHERE ?1 = '' OR workspace_path = ?1 ORDER BY updated_at DESC, id ASC`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var summaries []Summary
	for rows.Next() {
		var summary Summary
		if err := rows.Scan(&summary.ID, &summary.Workspace, &summary.Title, &summary.UpdatedAt); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

// AppendTurnStart appends a turn start, adopts a session title when none has
// been saved, and updates activity, in one transaction.
func (s *Store) AppendTurnStart(id string, start *transcript.TurnStart) (Summary, error) {
	if err := start.Validate(); err != nil {
		return Summary{}, err
	}
	var title string
	if start.Input.Skill != nil {
		title = titleFromPrompt(start.Input.Skill.CommandText())
	} else {
		title = titleFromPrompt(start.Input.Message.Text())
		if title == "" && start.Input.Message.HasImages() {
			title = "Image"
		}
	}
	return s.append(id, title, start)
}

// AppendBatch appends an assistant batch and updates activity in one
// transaction.
func (s *Store) AppendBatch(id string, batch *transcript.AssistantBatch) error {
	if err := batch.Validate(); err != nil {
		return err
	}
	_, err := s.append(id, "", batch)
	return err
}

// AppendTurnError appends a turn error and updates activity in one
// transaction.
func (s *Store) AppendTurnError(id, text string) error {
	_, err := s.append(id, "", transcript.TurnError(text))
	return err
}

func (s *Store) append(id, title string, entry transcript.Entry) (Summary, error) {
	kind, data, err := transcript.Encode(entry)
	if err != nil {
		return Summary{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Summary{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE sessions SET updated_at = ?2, title = COALESCE(title, NULLIF(?3, '')) WHERE id = ?1",
		id, now(), title)
	if err != nil {
		return Summary{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return Summary{}, err
	} else if changed == 0 {
		return Summary{}, fmt.Errorf("session %s does not exist", id)
	}
	if _, err := tx.Exec("INSERT INTO transcript_entries (session_id, kind, data) VALUES (?, ?, ?)", id, kind, data); err != nil {
		return Summary{}, err
	}
	summary, err := readSummary(tx, id)
	if err != nil {
		return Summary{}, err
	}
	return summary, tx.Commit()
}

// Delete removes a session and its transcript. An absent session is a
// success.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

func readSummary(tx *sql.Tx, id string) (Summary, error) {
	summary := Summary{ID: id}
	err := tx.QueryRow("SELECT workspace_path, COALESCE(title, ''), updated_at FROM sessions WHERE id = ?", id).
		Scan(&summary.Workspace, &summary.Title, &summary.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	return summary, err
}

func readTranscript(tx *sql.Tx, id string) ([]transcript.Entry, error) {
	rows, err := tx.Query("SELECT kind, data FROM transcript_entries WHERE session_id = ? ORDER BY id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []transcript.Entry
	for rows.Next() {
		var kind, data string
		if err := rows.Scan(&kind, &data); err != nil {
			return nil, err
		}
		entry, err := transcript.Decode(kind, data)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// titleFromPrompt returns the first nonblank line of a prompt, at most
// maxTitleChars characters counting the ellipsis that marks a shortened one.
// A blank prompt has no title, so it does not spend the session's one chance
// at a title.
func titleFromPrompt(text string) string {
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) <= maxTitleChars {
			return line
		}
		return string([]rune(line)[:maxTitleChars-1]) + "…"
	}
	return ""
}

func now() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// newID returns a random UUID version 4.
func newID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
