//go:build unix

package agent

import (
	"syscall"
	"testing"
)

func makeInstructionFIFO(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}
