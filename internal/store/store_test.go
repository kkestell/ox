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
