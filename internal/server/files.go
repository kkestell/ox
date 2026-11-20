package server

import (
	"os"
	"path"
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
