package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Workspace struct {
	root     string
	readable []string
}

type committedError struct {
	err error
}

func (e committedError) Error() string {
	return e.err.Error()
}

func (e committedError) Unwrap() error {
	return e.err
}

func MutationCommitted(err error) bool {
	var committed committedError
	return errors.As(err, &committed)
}

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

func NewWorkspace(root string) *Workspace {
	return &Workspace{root: root}
}

func (w *Workspace) WithReadable(dir string) *Workspace {
	if dir != "" {
		w.readable = append(w.readable, dir)
	}
	return w
}

func (w *Workspace) ReadFile(path string) ([]byte, error) {
	root, name, err := w.confine(path, w.readRoots())
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = root.Close()
	}()
	file, err := openRegularFile(root, name)
	if err != nil {
		if errors.Is(err, errNotRegular) {
			return nil, fmt.Errorf("cannot access `%s`: not a regular file", path)
		}
		return nil, accessError(err, path)
	}
	defer func() {
		_ = file.Close()
	}()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, accessError(err, path)
	}
	return data, nil
}

func (w *Workspace) WriteFile(path string, data []byte) (bool, error) {
	key, ok := w.Key(path)
	if !ok {
		return false, fmt.Errorf("`%s` is outside the workspace", path)
	}
	root, name, err := w.confine(filepath.FromSlash(key), []string{w.root})
	if err != nil {
		return false, err
	}
	defer func() {
		_ = root.Close()
	}()

	dir := filepath.Dir(name)
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return false, writeError(err, path)
	}
	mode := fs.FileMode(0o644)
	created := false
	info, err := root.Stat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		created = true
	case err != nil:
		return false, writeError(err, path)
	case !info.Mode().IsRegular():
		return false, fmt.Errorf("cannot write `%s`: not a regular file", path)
	default:
		mode = info.Mode().Perm()
	}
	committed, err := atomicReplace(root, name, data, mode)
	if err != nil {
		if committed {
			return created, committedError{writeError(err, path)}
		}
		return false, writeError(err, path)
	}
	return created, nil
}

func (w *Workspace) Edit(path string, transform func([]byte) ([]byte, error)) error {
	key, ok := w.Key(path)
	if !ok {
		return fmt.Errorf("`%s` is outside the workspace", path)
	}
	root, name, err := w.confine(filepath.FromSlash(key), []string{w.root})
	if err != nil {
		return err
	}
	defer func() {
		_ = root.Close()
	}()

	file, err := openRegularFile(root, name)
	if err != nil {
		if errors.Is(err, errNotRegular) {
			return fmt.Errorf("cannot edit `%s`: not a regular file", path)
		}
		return writeError(err, path)
	}
	data, readErr := io.ReadAll(file)
	info, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, closeErr); err != nil {
		return writeError(err, path)
	}
	replacement, err := transform(data)
	if err != nil {
		return err
	}
	committed, err := atomicReplace(root, name, replacement, info.Mode().Perm())
	if err != nil {
		if committed {
			return committedError{writeError(err, path)}
		}
		return writeError(err, path)
	}
	return nil
}

func (w *Workspace) Key(path string) (string, bool) {
	_, name, ok := w.locate(path, []string{w.root})
	if !ok {
		return "", false
	}
	resolved, err := resolveExisting(filepath.Join(w.root, name))
	if err != nil {
		return "", false
	}
	for _, readable := range w.readable {
		resolvedReadable, err := resolveExisting(readable)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(resolvedReadable, resolved)
		if err == nil && !escapes(relative) {
			return "", false
		}
	}
	relative, err := filepath.Rel(w.root, resolved)
	if err != nil || escapes(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func (w *Workspace) WalkFiles(
	ctx context.Context,
	path string,
	yield func(WalkedFile) bool,
) error {
	root, name, err := w.confine(path, w.readRoots())
	if err != nil {
		return err
	}
	defer func() {
		_ = root.Close()
	}()
	return walkRoot(ctx, root, name, path, yield)
}

func (w *Workspace) readRoots() []string {
	return append([]string{w.root}, w.readable...)
}

func (w *Workspace) confine(path string, roots []string) (*os.Root, string, error) {
	dir, name, ok := w.locate(path, roots)
	if !ok {
		return nil, "", fmt.Errorf("`%s` is outside the workspace", path)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", fmt.Errorf("cannot access `%s`", path)
	}
	return root, name, nil
}

func (w *Workspace) locate(path string, roots []string) (dir, name string, ok bool) {
	if !filepath.IsAbs(path) {
		name := filepath.Clean(path)
		if escapes(name) {
			return "", "", false
		}
		return w.root, name, true
	}
	for _, dir := range roots {
		if rel, err := filepath.Rel(dir, path); err == nil && !escapes(rel) {
			return dir, rel, true
		}
	}
	return "", "", false
}

func escapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func accessError(err error, path string) error {
	if isEscape(err) {
		return fmt.Errorf("`%s` is outside the workspace", path)
	}
	return fmt.Errorf("cannot access `%s`", path)
}

func writeError(err error, path string) error {
	if isEscape(err) {
		return fmt.Errorf("`%s` is outside the workspace", path)
	}
	return fmt.Errorf("cannot write `%s`: %w", path, err)
}

func isEscape(err error) bool {
	var pathErr *fs.PathError
	return errors.As(err, &pathErr) &&
		strings.Contains(pathErr.Err.Error(), "escapes from parent")
}

func atomicReplace(
	root *os.Root,
	name string,
	data []byte,
	mode fs.FileMode,
) (bool, error) {
	temporary := filepath.Join(filepath.Dir(name), filepath.Base(name)+".ox-tmp")
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return false, err
	}
	remove := true
	defer func() {
		if remove {
			_ = root.Remove(temporary)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return false, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return false, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := root.Rename(temporary, name); err != nil {
		return false, err
	}
	remove = false
	return true, syncDir(filepath.Join(root.Name(), filepath.Dir(name)))
}

func resolveExisting(path string) (string, error) {
	var missing []string
	current := filepath.Clean(path)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
