//go:build unix

package workspace

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPathOpenRefusesANamedPipeWithoutBlocking(t *testing.T) {
	root, workspace := testWorkspace(t)
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := workspace.Resolve("pipe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := path.Open(); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Open() error = %v, want a non-regular-file refusal", err)
	}
}
