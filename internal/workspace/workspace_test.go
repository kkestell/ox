package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCanonicalRequiresAnExistingDirectoryAndResolvesSymlinks(t *testing.T) {
	parent := canonicalTempDir(t)
	real := filepath.Join(parent, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got, err := Canonical(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != real {
		t.Fatalf("canonical path = %q, want %q", got, real)
	}
	if _, err := Canonical(filepath.Join(parent, "missing")); err == nil {
		t.Fatal("missing directory was accepted")
	}
	file := writeFile(t, parent, "file", "x")
	if _, err := Canonical(file); err == nil {
		t.Fatal("file was accepted as a directory")
	}
}

func TestReadFileConfinesEveryEscapeRoute(t *testing.T) {
	root := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	secret := writeFile(t, outside, "secret", "secret")
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	files := NewWorkspace(root)
	for _, path := range []string{"../secret", secret, "link"} {
		if _, err := files.ReadFile(path); err == nil ||
			!strings.Contains(err.Error(), "outside the workspace") {
			t.Errorf("ReadFile(%q) error = %v", path, err)
		}
	}
}

func TestReadFileAllowsInternalSymlinkAndReadableRoot(t *testing.T) {
	root := canonicalTempDir(t)
	writeFile(t, root, "real", "inside")
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	spill := t.TempDir()
	spilled := writeFile(t, spill, "result.out", "overflow")
	files := NewWorkspace(root).WithReadable(spill)
	for path, want := range map[string]string{
		"link":  "inside",
		spilled: "overflow",
	} {
		got, err := files.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("ReadFile(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestWriteFileCreatesParentsAndAtomicallyPreservesMode(t *testing.T) {
	root := canonicalTempDir(t)
	files := NewWorkspace(root)
	created, err := files.WriteFile("nested/file.txt", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("new file was reported as an overwrite")
	}
	path := filepath.Join(root, "nested", "file.txt")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	created, err = files.WriteFile("nested/file.txt", []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("overwrite was reported as a create")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".ox-tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file remains: %v", err)
	}
}

func TestEditLeavesTheOriginalUntouchedWhenTransformRefuses(t *testing.T) {
	root := canonicalTempDir(t)
	path := writeFile(t, root, "file.txt", "original")
	wantErr := context.Canceled
	err := NewWorkspace(root).Edit("file.txt", func([]byte) ([]byte, error) {
		return nil, wantErr
	})
	if err != wantErr {
		t.Fatalf("error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("content = %q", data)
	}
	if _, err := os.Stat(path + ".ox-tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file remains: %v", err)
	}
}

func TestWritesStayInTheWritableRootAndRejectNonRegularTargets(t *testing.T) {
	root := canonicalTempDir(t)
	readable := canonicalTempDir(t)
	files := NewWorkspace(root).WithReadable(readable)
	for _, path := range []string{
		filepath.Join(readable, "spill"),
		"../outside",
	} {
		if _, err := files.WriteFile(path, []byte("x")); err == nil ||
			!strings.Contains(err.Error(), "outside the workspace") {
			t.Errorf("WriteFile(%q) error = %v", path, err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := files.WriteFile("directory", []byte("x")); err == nil ||
		!strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory write error = %v", err)
	}

	nestedReadable := filepath.Join(root, ".session", "spill")
	if err := os.MkdirAll(nestedReadable, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := NewWorkspace(root).WithReadable(nestedReadable)
	if _, err := nested.WriteFile(".session/spill/result", []byte("x")); err == nil ||
		!strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("nested readable-root write error = %v", err)
	}
}

func TestKeyCanonicalizesPathSpellingsAndInternalSymlinks(t *testing.T) {
	root := canonicalTempDir(t)
	writeFile(t, root, "real/file.txt", "x")
	if err := os.Symlink("real/file.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	files := NewWorkspace(root)
	for _, path := range []string{
		"real/file.txt",
		filepath.Join(root, "real", "file.txt"),
		"link",
	} {
		key, ok := files.Key(path)
		if !ok || key != "real/file.txt" {
			t.Errorf("Key(%q) = %q, %v", path, key, ok)
		}
	}
}

func TestMutationThroughAnInternalSymlinkChangesItsTarget(t *testing.T) {
	root := canonicalTempDir(t)
	target := writeFile(t, root, "real/file.txt", "before")
	link := filepath.Join(root, "link")
	if err := os.Symlink("real/file.txt", link); err != nil {
		t.Fatal(err)
	}
	files := NewWorkspace(root)
	if err := files.Edit("link", func([]byte) ([]byte, error) {
		return []byte("after edit"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := files.WriteFile("link", []byte("after write")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after write" {
		t.Fatalf("target content = %q", data)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("mutation replaced the symlink")
	}
}

func TestWriteReportsAPostRenameSyncFailureAsCommitted(t *testing.T) {
	root := canonicalTempDir(t)
	originalSyncDir := syncDir
	syncDir = func(string) error {
		return errors.New("sync failed")
	}
	t.Cleanup(func() {
		syncDir = originalSyncDir
	})

	created, err := NewWorkspace(root).WriteFile("file.txt", []byte("landed"))
	if !created || !MutationCommitted(err) ||
		!strings.Contains(err.Error(), "sync failed") {
		t.Fatalf("created = %v, error = %v", created, err)
	}
	data, readErr := os.ReadFile(filepath.Join(root, "file.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "landed" {
		t.Fatalf("content = %q", data)
	}
}

func TestWalkFilesHonorsGitignoreNegationAndHiddenEntries(t *testing.T) {
	root := canonicalTempDir(t)
	writeFile(t, root, ".gitignore", "*.tmp\n!keep.tmp\nignored/\n")
	writeFile(t, root, "keep.tmp", "")
	writeFile(t, root, "drop.tmp", "")
	writeFile(t, root, "visible.txt", "")
	writeFile(t, root, ".hidden.txt", "")
	writeFile(t, root, "ignored/kept-by-negation.txt", "")
	writeFile(t, root, "nested/.gitignore", "*.txt\n!keep.txt\n")
	writeFile(t, root, "nested/drop.txt", "")
	writeFile(t, root, "nested/keep.txt", "")

	var got []string
	err := NewWorkspace(root).WalkFiles(context.Background(), ".", func(file WalkedFile) bool {
		got = append(got, file.Display)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"./keep.tmp", "./nested/keep.txt", "./visible.txt"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("walk = %q, want %q", got, want)
	}
}

func TestWalkFilesSurfacesCancellation(t *testing.T) {
	root := canonicalTempDir(t)
	writeFile(t, root, "a", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewWorkspace(root).WalkFiles(ctx, ".", func(WalkedFile) bool {
		t.Fatal("cancelled walk yielded a file")
		return false
	})
	if err == nil {
		t.Fatal("cancelled walk returned a complete result")
	}
}

func TestWalkFilesFollowsAnExplicitInternalSymlinkAndRefusesAnEscape(t *testing.T) {
	root := canonicalTempDir(t)
	writeFile(t, root, "real.txt", "x")
	if err := os.Symlink("real.txt", filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	var got []string
	err := NewWorkspace(root).WalkFiles(context.Background(), "inside", func(file WalkedFile) bool {
		got = append(got, file.Display)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "inside" {
		t.Fatalf("internal symlink walk = %v", got)
	}

	outside := writeFile(t, canonicalTempDir(t), "outside", "x")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	err = NewWorkspace(root).WalkFiles(context.Background(), "escape", func(WalkedFile) bool {
		t.Fatal("escaping symlink yielded a file")
		return false
	})
	if err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("escaping symlink error = %v", err)
	}
}
