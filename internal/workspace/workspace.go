// Package workspace confines filesystem paths to a session's working tree.
// Paths are opened through os.Root so the kernel applies the same boundary to
// the check and the operation, even when a symlink changes between them.
package workspace

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Canonical resolves dir to an absolute directory that Ox can list.
func Canonical(dir string) (string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("cannot access the working directory %s: %v", dir, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("cannot access the working directory %s: %v", dir, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("cannot access the working directory %s: %v", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory is not a directory: %s", dir)
	}

	handle, err := os.Open(canonical)
	if err != nil {
		return "", fmt.Errorf("cannot access the working directory %s: %v", dir, err)
	}
	_, readErr := handle.Readdirnames(1)
	closeErr := handle.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", fmt.Errorf("cannot access the working directory %s: %v", dir, readErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("cannot access the working directory %s: %v", dir, closeErr)
	}
	return canonical, nil
}

// Workspace is the canonical root owned by one session.
type Workspace struct {
	root string
}

// New creates a workspace from a canonical absolute root.
func New(root string) Workspace {
	if !filepath.IsAbs(root) {
		panic("workspace root must be absolute")
	}
	return Workspace{root: filepath.Clean(root)}
}

// Root returns the workspace's canonical absolute root.
func (w Workspace) Root() string {
	return w.root
}

// Resolve maps path to its canonical location inside the workspace.
func (w Workspace) Resolve(path string) (Path, error) {
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(w.root, candidate)
	}
	// Confinement is decided from the best resolution available, before any
	// failure is reported, so a path that leaves the tree is refused as outside
	// the workspace rather than by whatever the host said about it. Otherwise a
	// climb into a directory Ox cannot traverse would answer with the host's
	// permissions and tell the model what lives outside its tree.
	resolved, failure := resolveExisting(candidate)
	relative, ok := w.inside(resolved)
	if !ok {
		return Path{}, outsideError(path)
	}
	if failure != nil {
		return Path{}, accessError(failure, path)
	}
	return Path{root: w.root, name: relative}, nil
}

// inside reports where target sits relative to the root, and false if it sits
// outside.
func (w Workspace) inside(target string) (string, bool) {
	relative, err := filepath.Rel(w.root, target)
	if err != nil || escapes(relative) {
		return "", false
	}
	return relative, true
}

// Path is a workspace-confined path minted by Resolve.
type Path struct {
	root string
	name string
}

// String returns the workspace-relative spelling shown to people and models.
func (p Path) String() string {
	return filepath.ToSlash(p.name)
}

// Absolute returns the absolute spelling required by ACP filesystem messages.
func (p Path) Absolute() string {
	return filepath.Join(p.root, p.name)
}

// Open opens a regular file through the workspace root that confines it.
func (p Path) Open() (*os.File, error) {
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return nil, accessError(err, p.String())
	}
	file, openErr := root.OpenFile(p.name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	closeErr := root.Close()
	if openErr != nil {
		return nil, accessError(openErr, p.String())
	}
	if closeErr != nil {
		_ = file.Close()
		return nil, accessError(closeErr, p.String())
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, accessError(err, p.String())
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("cannot access `%s`: not a regular file", p.String())
	}
	return file, nil
}

// resolveExisting resolves the deepest existing part of path through symlinks
// and reattaches the components below it. A missing tail is ordinary, since a
// write needs one. Any other failure is returned alongside the best resolution
// reached anyway, so a caller can place the path before it reports the reason.
func resolveExisting(path string) (string, error) {
	var missing []string
	var failure error
	current := filepath.Clean(path)
	for {
		_, statErr := os.Lstat(current)
		if statErr == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr == nil {
				for index := len(missing) - 1; index >= 0; index-- {
					resolved = filepath.Join(resolved, missing[index])
				}
				return resolved, failure
			}
			// Something is here but does not resolve — a dangling or looping
			// symlink. That is a failure rather than a missing tail, and climbing
			// on now serves only to locate it.
			if failure == nil {
				failure = resolveErr
			}
		} else if failure == nil && !errors.Is(statErr, fs.ErrNotExist) {
			failure = statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Nothing above the filesystem root to climb to. Failing closed here
			// is the loop's bound: a root that will not resolve cannot be placed
			// inside any workspace.
			return current, failure
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func escapes(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func outsideError(path string) error {
	return fmt.Errorf("`%s` is outside the workspace", path)
}

func accessError(err error, path string) error {
	if isEscape(err) {
		return outsideError(path)
	}
	return fmt.Errorf("cannot access `%s`: %v", path, bareCause(err))
}

func isEscape(err error) bool {
	var pathError *fs.PathError
	return errors.As(err, &pathError) &&
		strings.Contains(pathError.Err.Error(), "escapes from parent")
}

func bareCause(err error) error {
	for {
		cause := errors.Unwrap(err)
		if cause == nil {
			return err
		}
		err = cause
	}
}
