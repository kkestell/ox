package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// gitError is a git command's failure: the step it was part of, git's
// trimmed stderr, and its exit code.
type gitError struct {
	step   string
	stderr string
	code   int
}

func (e *gitError) Error() string {
	return e.step + ": " + e.stderr
}

// git runs git with args and stdin closed, and returns its stdout. A failure
// is a *gitError naming step, unless git cannot be run at all.
func git(step string, args ...string) (string, error) {
	return gitCommand(step, exec.Command("git", args...))
}

// gitCommand runs cmd, a git command, and returns its stdout, as git does.
func gitCommand(step string, cmd *exec.Cmd) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return "", &gitError{step, strings.TrimSpace(stderr.String()), exit.ExitCode()}
	case err != nil:
		return "", fmt.Errorf("%s: %w", step, err)
	}
	return stdout.String(), nil
}

// worktreePaths lists the worktrees of workspace's repository, as git
// records them: physical paths.
func worktreePaths(step, workspace string) ([]string, error) {
	out, err := git(step, "-C", workspace, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for field := range strings.SplitSeq(out, "\x00") {
		if path, ok := strings.CutPrefix(field, "worktree "); ok {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// within reports whether path is dir or lies beneath it.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// physicalPath is path with the symlinks in its existing part resolved, so
// a directory can be placed before it is made.
func physicalPath(path string) (string, error) {
	dir, rest := filepath.Clean(path), ""
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) || filepath.Dir(dir) == dir {
			return "", err
		}
		dir, rest = filepath.Dir(dir), filepath.Join(filepath.Base(dir), rest)
	}
}

// worktreeBranch is the branch Ox makes for the worktree at root.
func worktreeBranch(root string) string {
	return "ox/" + filepath.Base(root)
}

// copyWorktreeInclude copies into the worktree at root the untracked,
// gitignored files that the repository's .worktreeinclude at top matches, the
// way Claude Code does. Git does the matching. Nothing happens without the
// file.
func copyWorktreeInclude(top, root string) error {
	const step = "copy worktree files"
	if _, err := os.Stat(filepath.Join(top, ".worktreeinclude")); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	matched, err := git(step, "-C", top, "ls-files", "-z", "--others", "--ignored", "--exclude-from=.worktreeinclude")
	if err != nil || matched == "" {
		return err
	}
	check := exec.Command("git", "-C", top, "check-ignore", "-z", "--stdin")
	check.Stdin = strings.NewReader(matched)
	ignored, err := gitCommand(step, check)
	var exit *gitError
	if errors.As(err, &exit) && exit.code == 1 {
		return nil // no path is ignored
	} else if err != nil {
		return err
	}
	for name := range strings.SplitSeq(ignored, "\x00") {
		if name == "" {
			continue
		}
		if err := copyFile(filepath.Join(top, name), filepath.Join(root, name), root); err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
	}
	return nil
}

// copyFile copies the regular file src to dst, whose parent directory must
// resolve within root before any of it is made. Other entries are skipped.
func copyFile(src, dst, root string) error {
	info, err := os.Lstat(src)
	if err != nil || !info.Mode().IsRegular() {
		return err
	}
	dir := filepath.Dir(dst)
	if resolved, err := physicalPath(dir); err != nil {
		return err
	} else if !within(resolved, root) {
		return errors.New("destination is outside the worktree")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// addWorktree makes a worktree of workspace's repository in worktrees, on a
// new branch from HEAD, both named for a fresh name. It returns the
// worktree's physical root and its copy of workspace, holding the committed
// files and those .worktreeinclude lists. On failure it leaves
// nothing behind, unless discarding fails too, which its error says.
func addWorktree(workspace, worktrees string) (root, directory string, err error) {
	const step = "create worktree"
	prefix, err := git(step, "-C", workspace, "rev-parse", "--show-prefix")
	if err != nil {
		return "", "", err
	}
	prefix = strings.TrimSuffix(prefix, "\n")
	top, err := git(step, "-C", workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	top = strings.TrimSuffix(top, "\n")
	physical, err := physicalPath(worktrees)
	if err != nil {
		return "", "", err
	}
	listed, err := worktreePaths(step, workspace)
	if err != nil {
		return "", "", err
	}
	for _, path := range listed {
		if within(physical, path) {
			return "", "", fmt.Errorf("worktree directory %s is inside the repository at %s", worktrees, path)
		}
	}
	if err := os.MkdirAll(worktrees, 0o700); err != nil {
		return "", "", err
	}
	path := filepath.Join(worktrees, newID())
	branch := worktreeBranch(path)
	if _, err := git(step, "-C", workspace, "branch", branch, "HEAD"); err != nil {
		return "", "", err
	}
	// With hooks off, nothing runs after the checkout, and git removes a
	// checkout that fails partway itself, leaving only the branch.
	if _, err := git(step, "-c", "core.hooksPath=/dev/null", "-C", workspace, "worktree", "add", path, branch); err != nil {
		_, deleteErr := git("delete branch", "-C", workspace, "branch", "-D", branch)
		return "", "", errors.Join(err, deleteErr)
	}
	root, err = filepath.EvalSymlinks(path)
	if err == nil {
		directory = filepath.Join(root, prefix)
		// A folder holding only untracked files is absent from the checkout,
		// and one HEAD has as a symlink can lead out of it.
		resolved, resolveErr := filepath.EvalSymlinks(directory)
		info, statErr := os.Stat(resolved)
		if resolveErr != nil || statErr != nil || !info.IsDir() || !within(resolved, root) {
			err = fmt.Errorf("%s is not in the worktree", strings.TrimSuffix(prefix, "/"))
		} else {
			err = copyWorktreeInclude(top, root)
		}
	}
	if err != nil {
		if root == "" {
			root = path
		}
		return "", "", errors.Join(err, discardWorktree(workspace, root))
	}
	return root, directory, nil
}

// discardWorktree removes a worktree and its branch that no session has
// used. It runs only before publication, when no process has run in the
// worktree, so forcing the removal loses nothing.
func discardWorktree(workspace, root string) error {
	_, removeErr := git("discard worktree", "-C", workspace, "worktree", "remove", "--force", root)
	_, deleteErr := git("delete branch", "-C", workspace, "branch", "-D", worktreeBranch(root))
	return errors.Join(removeErr, deleteErr)
}

// removeWorktree removes the worktree at root of the session sessionID,
// unless it has uncommitted changes or untracked files or is on no branch.
// It keeps the branch. It reads the worktree's registration and folder live,
// so it finishes what an earlier removal left: a registered worktree whose
// folder is gone has its registration cleared, and a folder git no longer
// lists is left. Git runs in the worktree while it is present, so a removal
// needs no workspace folder, and otherwise in the workspace.
func removeWorktree(sessionID, workspace, root string) error {
	const step = "remove worktree"
	present := exists(root)
	dir := root
	if !present {
		if !exists(workspace) {
			// Git marks the registration prunable itself.
			log.Printf("session %s: worktree %s and workspace %s are gone; leaving the registration", sessionID, root, workspace)
			return nil
		}
		dir = workspace
	}
	listed, err := worktreePaths(step, dir)
	if err != nil && present {
		// Git cannot run in a folder whose registration is gone, so the
		// workspace's repository says whether it is registered.
		dir = workspace
		listed, err = worktreePaths(step, dir)
	}
	if err != nil {
		return err
	}
	switch registered := slices.Contains(listed, root); {
	case registered && present:
		var exit *gitError
		if _, err := git(step, "-C", root, "symbolic-ref", "-q", "HEAD"); errors.As(err, &exit) && exit.code == 1 {
			return errors.New("worktree HEAD is not on a branch")
		} else if err != nil {
			return err
		}
		status, err := git(step, "-C", root, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
		if err != nil {
			return err
		}
		if status != "" {
			return errors.New("worktree has uncommitted changes")
		}
	case registered:
		// Git cannot resolve a path whose parent is gone.
		if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
			return err
		}
	case present:
		log.Printf("session %s: worktree %s is not registered with git; leaving it", sessionID, root)
		return nil
	default:
		return nil
	}
	// Unforced, so git's own refusals pass through, and its recheck counts
	// untracked files whatever the repository's status.showUntrackedFiles.
	_, err = git(step, "-c", "status.showUntrackedFiles=all", "-C", dir, "worktree", "remove", root)
	return err
}
