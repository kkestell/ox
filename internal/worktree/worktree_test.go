package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo initializes a checkout with one commit and returns its root.
func repo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run(t, root, "init", "-q")
	run(t, root, "config", "user.email", "test@example.com")
	run(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "add", ".")
	run(t, root, "commit", "-qm", "initial")
	return root
}

func TestRootFindsTheCheckoutFromASubdirectory(t *testing.T) {
	root := repo(t)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, sub} {
		if got, err := Root(dir); err != nil || got != root {
			t.Errorf("Root(%s) = %q, %v; want %q", dir, got, err, root)
		}
	}
	if got, err := Root(t.TempDir()); err == nil {
		t.Errorf("Root of a directory outside a checkout = %q, want an error", got)
	}
}

func TestAddWorktreeCreatesTheBranchAndExcludesTheDirectory(t *testing.T) {
	root := repo(t)
	dir := WorktreeDir(root, "alice")
	branch := WorkerBranch("alice")
	if err := AddWorktree(root, dir, branch); err != nil {
		t.Fatal(err)
	}
	if got := run(t, dir, "branch", "--show-current"); got != branch {
		t.Errorf("branch = %q, want %q", got, branch)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Errorf("worktree does not have HEAD's files: %v", err)
	}
	if got, want := run(t, dir, "rev-parse", "HEAD"), run(t, root, "rev-parse", "HEAD"); got != want {
		t.Errorf("worktree HEAD = %q, want %q", got, want)
	}
	if status := run(t, root, "status", "--porcelain"); status != "" {
		t.Errorf("the checkout is dirty: %q", status)
	}
	exclude, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exclude), "/.ox/worktrees/\n") {
		t.Errorf("exclude = %q", exclude)
	}
}

func TestWorkspaceMapsTheCheckoutSubdirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "checkout")
	dir := WorktreeDir(root, "alice")
	for path, want := range map[string]string{
		root:                       dir,
		filepath.Join(root, "sub"): filepath.Join(dir, "sub"),
		filepath.Join(root, "a/b"): filepath.Join(dir, "a/b"),
	} {
		if got, err := Workspace(root, dir, path); err != nil || got != want {
			t.Errorf("Workspace(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	for _, path := range []string{filepath.Dir(root), root + "2"} {
		if got, err := Workspace(root, dir, path); err == nil {
			t.Errorf("Workspace(%q) = %q, want an error", path, got)
		}
	}
}

func TestRemovableChecksChangesMergesAndRemoval(t *testing.T) {
	root := repo(t)
	dir := WorktreeDir(root, "alice")
	branch := WorkerBranch("alice")
	if err := AddWorktree(root, dir, branch); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Removable(root, dir, branch); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("Removable with an untracked file = %v", err)
	}

	if err := os.Remove(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "commit", "-qam", "change")
	if err := Removable(root, dir, branch); err == nil || !strings.Contains(err.Error(), "not merged") {
		t.Errorf("Removable with an unmerged branch = %v", err)
	}
	if err := RemoveWorktree(root, dir, branch); err == nil {
		t.Error("an unmerged worktree was removed")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the refused worktree is gone: %v", err)
	}

	run(t, root, "merge", "-q", "--no-ff", "-m", "merge alice", branch)
	if err := Removable(root, dir, branch); err != nil {
		t.Errorf("Removable after merge: %v", err)
	}
	if err := RemoveWorktree(root, dir, branch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the worktree still exists: %v", err)
	}
	if branches := run(t, root, "branch", "--list", branch); branches != "" {
		t.Errorf("the branch still exists: %q", branches)
	}
}

func TestRemoveWorktreeRefusesAWorktreeWithChanges(t *testing.T) {
	root := repo(t)
	dir := WorktreeDir(root, "alice")
	branch := WorkerBranch("alice")
	if err := AddWorktree(root, dir, branch); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(root, dir, branch); err == nil {
		t.Error("a worktree with changes was removed")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the refused worktree is gone: %v", err)
	}
}
