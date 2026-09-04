package workspace

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadFileRefusesAFIFOWithoutBlocking(t *testing.T) {
	root := canonicalTempDir(t)
	path := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := NewWorkspace(root).ReadFile("pipe")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("FIFO error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO read blocked")
	}
}
