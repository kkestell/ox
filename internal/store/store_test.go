package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenUsesLiteralPath(t *testing.T) {
	dir := t.TempDir()
	t.Run("absolute", func(t *testing.T) { testLiteralPath(t, filepath.Join(dir, "ox?literal%3f#name.db")) })
	t.Run("relative", func(t *testing.T) {
		t.Chdir(dir)
		testLiteralPath(t, ":memory:")
	})
}

func testLiteralPath(t *testing.T, path string) {
	t.Helper()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddWorkspace(&Workspace{ID: "w", Path: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("SQLite did not open the literal database path: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if workspaces, err := other.Workspaces(); err != nil || len(workspaces) != 1 || workspaces[0].ID != "w" {
		t.Fatalf("reopening the database lost its workspace: %+v, %v", workspaces, err)
	}
}

// A child's row is stored with its session and goes with it; its name is
// unique among its parent's children, and its parent cannot be deleted first.
// Every session stores an absolute directory.
func TestChildren(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddWorkspace(&Workspace{ID: "w", Path: "/w", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	add := func(id, name string) error {
		r := &Session{ID: id, Directory: "/w", CreatedAt: time.Now()}
		if name != "" {
			r.Child = &Child{ParentID: "parent", Name: name, Title: "Title " + name}
			r.Directory = "/d/" + name
		}
		return st.AddSession("w", r)
	}
	for _, directory := range []string{"", "relative"} {
		if err := st.AddSession("w", &Session{ID: "unrooted", Directory: directory, CreatedAt: time.Now()}); err == nil {
			t.Fatalf("a session with directory %q was stored", directory)
		}
	}
	if err := add("parent", ""); err != nil {
		t.Fatal(err)
	}
	if err := add("first", "worker"); err != nil {
		t.Fatal(err)
	}
	if err := add("second", "worker"); err == nil {
		t.Fatal("a second child named worker was stored")
	}
	workspaces, err := st.Workspaces()
	if err != nil {
		t.Fatal(err)
	}
	if sessions := workspaces[0].Sessions; len(sessions) != 2 || sessions[0].Child != nil || sessions[0].Directory != "/w" ||
		*sessions[1].Child != (Child{ParentID: "parent", Name: "worker", Title: "Title worker"}) || sessions[1].Directory != "/d/worker" {
		t.Fatalf("Workspaces read %+v", sessions)
	}
	if err := st.DeleteSession("parent"); err == nil {
		t.Fatal("a parent with a child was deleted")
	}
	if err := st.DeleteSession("first"); err != nil {
		t.Fatal(err)
	}
	if err := add("second", "worker"); err != nil {
		t.Fatalf("a deleted child's name stayed taken: %v", err)
	}
}

func TestStoreHoldsDatabaseUntilClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ox.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Open(path); err == nil || !strings.Contains(err.Error(), "another oxd is using") {
		if second != nil {
			second.Close()
		}
		t.Fatalf("a second store opened a held database: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatalf("a closed store still holds its database: %v", err)
	}
	again.Close()
}

// A session's images go with it.
func TestImagesDeletedWithSession(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddWorkspace(&Workspace{ID: "w", Path: "/w", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &Session{ID: "s", Directory: "/w", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddImages("s", []Image{{ID: "i", MimeType: "image/png", Data: []byte("png")}}); err != nil {
		t.Fatal(err)
	}
	if image, err := st.Image("s", "i"); err != nil || image == nil || string(image.Data) != "png" {
		t.Fatalf("the stored image read %+v, %v", image, err)
	}
	if err := st.DeleteSession("s"); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := st.db.QueryRow(`SELECT count(*) FROM images`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d images outlived their session: %v", left, err)
	}
}

// A session's generated title is read with it and goes with it.
func TestTitles(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.AddWorkspace(&Workspace{ID: "w", Path: "/w", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"titled", "untitled"} {
		if err := st.AddSession("w", &Session{ID: id, Directory: "/w", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetTitle("titled", "A title"); err != nil {
		t.Fatal(err)
	}
	workspaces, err := st.Workspaces()
	if err != nil {
		t.Fatal(err)
	}
	if sessions := workspaces[0].Sessions; len(sessions) != 2 || sessions[0].Title != "A title" || sessions[1].Title != "" {
		t.Fatalf("Workspaces read %+v", sessions)
	}
	if err := st.DeleteSession("titled"); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := st.db.QueryRow(`SELECT count(*) FROM titles`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d titles outlived their session: %v", left, err)
	}
}

// oldSchema is the schema before every session stored its directory, when
// only a child's row did.
const oldSchema = `
CREATE TABLE workspaces (
	id         TEXT PRIMARY KEY,
	path       TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL
);
CREATE TABLE sessions (
	id              TEXT PRIMARY KEY,
	workspace_id    TEXT NOT NULL REFERENCES workspaces ON DELETE CASCADE,
	harness         TEXT NOT NULL,
	model           TEXT NOT NULL,
	effort          TEXT NOT NULL,
	permission_mode TEXT NOT NULL,
	resume_token    TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE TABLE events (
	id         INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	data       TEXT NOT NULL
);
CREATE TABLE children (
	session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE,
	parent_id  TEXT NOT NULL REFERENCES sessions,
	name       TEXT NOT NULL,
	title      TEXT NOT NULL,
	directory  TEXT NOT NULL,
	UNIQUE (parent_id, name)
);
CREATE TABLE archived (
	session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE
);
CREATE TABLE titles (
	session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE,
	title      TEXT NOT NULL
);
CREATE TABLE images (
	id         TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions ON DELETE CASCADE,
	mime_type  TEXT NOT NULL,
	data       BLOB NOT NULL
);
CREATE INDEX events_session ON events (session_id, id);
CREATE INDEX images_session ON images (session_id);
`

// Opening a database whose children alone stored a directory gives every
// session its directory, as spelled, and keeps every session and its data. A
// failed upgrade leaves the database as it was.
func TestUpgradeAddsSessionDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ox.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	now := formatTime(time.Now())
	if _, err := old.Exec(oldSchema+`
		INSERT INTO workspaces VALUES ('w', '/w/', ?1);
		INSERT INTO sessions VALUES ('parent', 'w', 'claude', 'haiku', '', 'default', 'token', ?1);
		INSERT INTO sessions VALUES ('child', 'w', 'claude', 'haiku', '', 'default', '', ?1);
		INSERT INTO children VALUES ('child', 'parent', 'worker', 'Worker', '/w/./sub');
		INSERT INTO archived VALUES ('child');
		INSERT INTO titles VALUES ('parent', 'A title');
		INSERT INTO events (session_id, created_at, data) VALUES ('parent', ?1, '{"type":"prompt"}');
		INSERT INTO events (session_id, created_at, data) VALUES ('child', ?1, '{"type":"end"}');
		CREATE INDEX children_directory ON children (directory);`, now); err != nil {
		t.Fatal(err)
	}
	hasColumn := func(table string) bool {
		t.Helper()
		var n int
		if err := old.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = 'directory'`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}

	if st, err := Open(path); err == nil || !strings.HasPrefix(err.Error(), "upgrade database:") {
		if st != nil {
			st.Close()
		}
		t.Fatalf("an upgrade the index blocks opened with %v", err)
	}
	if hasColumn("sessions") || !hasColumn("children") {
		t.Fatal("a failed upgrade changed the database")
	}

	if _, err := old.Exec(`DROP INDEX children_directory`); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	check := func(st *Store) {
		t.Helper()
		workspaces, err := st.Workspaces()
		if err != nil {
			t.Fatal(err)
		}
		if len(workspaces) != 1 || len(workspaces[0].Sessions) != 2 {
			t.Fatalf("Workspaces read %+v", workspaces)
		}
		parent, child := workspaces[0].Sessions[0], workspaces[0].Sessions[1]
		if parent.Directory != "/w/" || parent.ResumeToken != "token" || parent.Title != "A title" || parent.Archived {
			t.Fatalf("the top-level session read %+v", parent)
		}
		if child.Directory != "/w/./sub" || !child.Archived ||
			child.Child == nil || *child.Child != (Child{ParentID: "parent", Name: "worker", Title: "Worker"}) {
			t.Fatalf("the child read %+v", child)
		}
		for _, id := range []string{"parent", "child"} {
			if events, err := st.Events(id); err != nil || len(events) != 1 {
				t.Fatalf("%s's events read %v, %v", id, events, err)
			}
		}
	}
	check(st)
	if hasColumn("children") {
		t.Fatal("children kept its directory")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("an upgraded database did not reopen: %v", err)
	}
	defer again.Close()
	check(again)
}
