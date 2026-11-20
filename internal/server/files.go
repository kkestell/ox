package server

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"unicode/utf8"
)

// hidden names version-control and OS files the file tree leaves out at any
// depth.
func hidden(name string) bool {
	switch name {
	case ".git", ".svn", ".hg", ".hypercube", ".DS_Store", "Thumbs.db":
		return true
	}
	return false
}

type folderEntry struct {
	Name   string `json:"name"`
	Folder bool   `json:"folder"`
}

// listFolder lists the folder at the slash-separated local path within dir,
// in directory order. os.Root refuses any path or symlink that leads outside
// dir, so a symlink that does, or that dangles, is listed as a file.
func listFolder(dir, folder string) ([]folderEntry, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if folder == "" {
		folder = "."
	}
	f, err := root.Open(folder)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	list := []folderEntry{}
	for _, e := range entries {
		if hidden(e.Name()) {
			continue
		}
		isFolder := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			info, err := root.Stat(path.Join(folder, e.Name()))
			isFolder = err == nil && info.IsDir()
		}
		list = append(list, folderEntry{e.Name(), isFolder})
	}
	return list, nil
}

// maxFileSize is the largest file readFile returns.
const maxFileSize = 1 << 20

var (
	errNotRegular = errors.New("not a regular file")
	errTooLarge   = errors.New("file is larger than 1 MiB")
	errBinary     = errors.New("file is binary")
	errNotText    = errors.New("file is not UTF-8 text")
)

// readFile returns the text of the regular file at the slash-separated local
// path within dir. It checks the file's type before opening it, so a FIFO or
// device cannot block the read. A NUL byte in the first 8000 bytes marks a
// file as binary, as Git decides.
func readFile(dir, file string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	info, err := root.Stat(file)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errNotRegular
	}
	f, err := root.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return "", err
	}
	switch {
	case len(data) > maxFileSize:
		return "", errTooLarge
	case bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0:
		return "", errBinary
	case !utf8.Valid(data):
		return "", errNotText
	}
	return string(data), nil
}
