package tools

import "golang.org/x/sys/unix"

func renameNoReplace(fromFD int, source string, toFD int, destination string) error {
	return unix.Renameat2(fromFD, source, toFD, destination, unix.RENAME_NOREPLACE)
}
