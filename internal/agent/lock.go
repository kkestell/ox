package agent

import (
	"errors"
	"os"
	"syscall"

	"github.com/kkestell/ox/internal/workspace"
)

func syncDirectory(path string) error {
	return workspace.SyncDirectory(path)
}

func tryLock(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errSessionLocked
	}
	return err
}

func lock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

func unlock(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
