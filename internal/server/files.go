package server

import (
	"bytes"
	"errors"
	"io"
	"os"
	"runtime"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
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
			info, err := root.Stat(folder + "/" + e.Name())
			isFolder = err == nil && info.IsDir()
		}
		list = append(list, folderEntry{localName(e.Name()), isFolder})
	}
	return list, nil
}

// localName is a name or path as oxd reports it. macOS filesystems treat the
// composed and decomposed Unicode spellings of a name as one name, and Git
// there reports the composed spelling (core.precomposeUnicode), while ReadDir
// returns the spelling on disk. On macOS every name is reported composed, so
// the file tree and the changes spell each file the same way. Elsewhere the
// two spellings are different names and are reported as they are.
func localName(name string) string {
	if runtime.GOOS == "darwin" {
		return norm.NFC.String(name)
	}
	return name
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
// path within dir.
func readFile(dir, file string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := openRegular(root, file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return "", err
	}
	return text(data)
}

// openRegular opens the regular file at the slash-separated local path within
// root. It checks the file's type before opening it, so a FIFO or device
// cannot block the open.
func openRegular(root *os.Root, file string) (*os.File, error) {
	info, err := root.Stat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return root.Open(file)
}

// text returns data, the first maxFileSize+1 bytes of a file, as text, or why
// it cannot be shown as text.
func text(data []byte) (string, error) {
	switch {
	case len(data) > maxFileSize:
		return "", errTooLarge
	case isBinary(data):
		return "", errBinary
	case !utf8.Valid(data):
		return "", errNotText
	}
	return string(data), nil
}

// isBinary reports whether a file starting with data is binary: a NUL byte in
// its first 8000 bytes, as Git decides.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}
