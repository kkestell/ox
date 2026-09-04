package workspace

import (
	"errors"
	"os"
	"syscall"
)

var errNotRegular = errors.New("not a regular file")

func openRegularFile(root *os.Root, name string) (*os.File, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errNotRegular
	}
	return file, nil
}

var syncDir = func(path string) (err error) {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, dir.Close())
	}()
	return dir.Sync()
}
