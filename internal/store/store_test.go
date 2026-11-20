package store

import (
	"os"
	"path/filepath"
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
	defer st.Close()
	if err := st.AddWorkspace(&Workspace{ID: "w", Path: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("SQLite did not open the literal database path: %v", err)
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
		r := &Session{ID: id, CreatedAt: time.Now()}
		if name != "" {
			r.Child = &Child{ParentID: "parent", Name: name, Title: "Title " + name, Directory: "/d/" + name}
		}
		return st.AddSession("w", r)
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
	if sessions := workspaces[0].Sessions; len(sessions) != 2 || sessions[0].Child != nil ||
		*sessions[1].Child != (Child{ParentID: "parent", Name: "worker", Title: "Title worker", Directory: "/d/worker"}) {
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
