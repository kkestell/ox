// Package worktree creates and removes worker worktrees under a checkout.
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Root returns the top level of the checkout containing dir.
func Root(dir string) (string, error) {
	return git(dir, "rev-parse", "--show-toplevel")
}

// WorkerBranch names a worker's branch.
func WorkerBranch(name string) string {
	return "ox/" + name
}

// WorktreeDir is where a worker's worktree lives. Worktrees sit inside the
// checkout so that tools extend the checkout's folder trust to them.
func WorktreeDir(root, name string) string {
	return filepath.Join(root, ".ox", "worktrees", name)
}

// Workspace returns the directory in worktree that matches dir's place in
// root. A dir at root maps to worktree itself, and a subdirectory maps to the
// same subdirectory of worktree.
func Workspace(root, worktree, dir string) (string, error) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is not inside %s", dir, root)
	}
	return filepath.Join(worktree, rel), nil
}

// AddWorktree creates the worker's branch from HEAD and checks it out in dir.
// It adds .ox/worktrees/ to the checkout's local excludes.
func AddWorktree(root, dir, branch string) error {
	exclude, err := git(root, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(root, exclude)
	}
	data, _ := os.ReadFile(exclude)
	if !slices.Contains(strings.Split(string(data), "\n"), "/.ox/worktrees/") {
		if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString("/.ox/worktrees/\n")
		f.Close()
		if err != nil {
			return err
		}
	}
	_, err = git(root, "worktree", "add", "-b", branch, dir, "HEAD")
	return err
}

// Removable returns an error if the worktree has uncommitted changes or its
// branch is not merged into HEAD.
func Removable(root, dir, branch string) error {
	if _, err := git(root, "merge-base", "--is-ancestor", branch, "HEAD"); err != nil {
		return fmt.Errorf("branch %s is not merged into HEAD", branch)
	}
	dirty, err := Dirty(dir)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("worktree %s has uncommitted changes", dir)
	}
	return nil
}

// Dirty reports whether the checkout in dir has uncommitted changes or
// untracked files.
func Dirty(dir string) (bool, error) {
	status, err := git(dir, "status", "--porcelain")
	return status != "", err
}

// RemoveWorktree removes the worktree and deletes its branch if Removable
// allows it.
func RemoveWorktree(root, dir, branch string) error {
	if err := Removable(root, dir, branch); err != nil {
		return err
	}
	if _, err := git(root, "worktree", "remove", dir); err != nil {
		return err
	}
	_, err := git(root, "branch", "-d", branch)
	return err
}
