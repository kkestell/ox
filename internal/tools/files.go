package tools

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Relative paths use the session workspace; absolute paths are used directly.
func filePath(root, name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Join(root, name)
}

// openRegular avoids blocking on a FIFO and rejects other nonregular files.
func openRegular(name string) (*os.File, error) {
	file, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
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

// writeFile replaces contents, or exclusively creates a file and its parents.
func writeContents(name string, contents []byte, create bool) error {
	flags := os.O_WRONLY | syscall.O_NONBLOCK
	if create {
		if err := os.MkdirAll(filepath.Dir(name), 0o777); err != nil {
			return err
		}
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(name, flags, 0o666)
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

func moveFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o777); err != nil {
		return err
	}
	return renameNoReplace(source, destination)
}
