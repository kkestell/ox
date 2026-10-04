package tools

import "golang.org/x/sys/unix"

func renameNoReplace(fromFD int, source string, toFD int, destination string) error {
	return unix.RenameatxNp(fromFD, source, toFD, destination, unix.RENAME_EXCL)
}
