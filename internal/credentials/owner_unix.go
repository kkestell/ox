//go:build unix

package credentials

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func validateFileSecurityBeforeOpen(info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("must not be accessible by group or other users (mode is %04o)", info.Mode().Perm())
	}
	if info.Mode().Perm()&0o400 == 0 {
		return errors.New("must be readable by its owner")
	}
	if !ownedByCurrentUser(info) {
		return errors.New("must be owned by the current user")
	}
	return nil
}

func validateFileSecurity(_ *os.File, info os.FileInfo) error {
	return validateFileSecurityBeforeOpen(info)
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
