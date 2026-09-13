package workspace

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

var (
	errNotRegular = errors.New("not a regular file")
	// errTooLarge lets a caller replace the bound's wording with its own.
	errTooLarge = errors.New("file exceeds its size limit")
)

// ReadConfined reads name under root with the rules a trusted-input loader
// needs: a symbolic link is refused before opening, the opened handle is
// checked again so a path swapped between the two cannot smuggle in a device or
// FIFO, the open is non-blocking so a raced FIFO cannot hang the caller, and no
// more than limit bytes are materialized. Content policy, such as requiring
// UTF-8, belongs to the caller. A missing file reports fs.ErrNotExist.
func ReadConfined(root *os.Root, name string, limit int) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("symbolic links are not allowed")
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	file, err := openRegularFile(root, name)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	return readBounded(file, limit)
}

// readBounded materializes at most limit bytes and refuses anything longer, so
// a reader never allocates what it is about to reject.
func readBounded(file *os.File, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%w of %d bytes", errTooLarge, limit)
	}
	return data, nil
}

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

// SyncDirectory flushes a directory's entries, so a file created or replaced
// inside it survives a crash.
func SyncDirectory(path string) error {
	return syncDir(path)
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
