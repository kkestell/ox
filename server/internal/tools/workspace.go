package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// workspace opens files relative to a session workspace through an os.Root,
// so no path, and no link swapped into a validated path, reaches outside it.
type workspace struct {
	// given is the workspace path as Ox was given it; root is its canonical
	// form.
	given string
	root  string
	dir   *os.Root
}

func openWorkspace(path string) (*workspace, error) {
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if root, err = filepath.Abs(root); err != nil {
		return nil, err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	return &workspace{given: path, root: root, dir: dir}, nil
}

func (w *workspace) Close() { w.dir.Close() }

// relativeName returns name relative to the workspace when it is an absolute
// path inside it, under the given path or its canonical form, and otherwise
// name itself. The workspace itself becomes ".".
func (w *workspace) relativeName(name string) string {
	if !filepath.IsAbs(name) {
		return name
	}
	for _, root := range []string{w.given, w.root} {
		if relative, err := filepath.Rel(root, name); err == nil && filepath.IsLocal(relative) {
			return relative
		}
	}
	return name
}

var errOutsideRelative = errors.New("expected a path inside the workspace without parent traversal; use the shell tool for other paths")

// resolve resolves a name relative to the workspace, following links, and
// returns the canonical relative path of its target, which must be inside the
// workspace. The workspace itself is ".".
func (w *workspace) resolve(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || hasParent(name) {
		return "", errOutsideRelative
	}
	target, err := filepath.EvalSymlinks(filepath.Join(w.root, name))
	if err != nil {
		return "", err
	}
	return w.relative(target)
}

// relative returns a canonical absolute path relative to the workspace.
func (w *workspace) relative(path string) (string, error) {
	relative, err := filepath.Rel(w.root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return "", errors.New("path resolves outside the workspace")
	}
	return relative, nil
}

func hasParent(name string) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(name), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// openRegular opens a regular file for reading. Opening does not block on a
// FIFO.
func (w *workspace) openRegular(name string) (*os.File, error) {
	file, err := w.dir.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("path must name a regular file")
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func (w *workspace) isDirectory(name string) bool {
	info, err := w.dir.Lstat(name)
	return err == nil && info.IsDir()
}

func (w *workspace) isRegular(name string) bool {
	info, err := w.dir.Lstat(name)
	return err == nil && info.Mode().IsRegular()
}

// writeFile replaces a regular file's contents, or creates a file that must
// not exist, creating its missing parent directories.
func (w *workspace) writeFile(name string, contents []byte, create bool) error {
	flags := os.O_WRONLY | syscall.O_NONBLOCK
	if create {
		if err := w.dir.MkdirAll(filepath.Dir(name), 0o777); err != nil {
			return err
		}
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := w.dir.OpenFile(name, flags, 0o666)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("path must name a regular file")
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err = file.Write(contents)
	return err
}

func (w *workspace) remove(name string) error {
	return w.dir.Remove(name)
}

// move renames a file, creating the destination's missing parent directories.
func (w *workspace) move(source, destination string) error {
	if err := w.dir.MkdirAll(filepath.Dir(destination), 0o777); err != nil {
		return err
	}
	// Open both parents through the workspace before using descriptor-relative
	// rename, so swapped links cannot redirect the move outside it.
	from, err := w.dir.OpenFile(filepath.Dir(source), os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer from.Close()
	to, err := w.dir.OpenFile(filepath.Dir(destination), os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer to.Close()
	if err := renameNoReplace(int(from.Fd()), filepath.Base(source), int(to.Fd()), filepath.Base(destination)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("destination %s already exists: %w", destination, err)
		}
		return err
	}
	return nil
}
