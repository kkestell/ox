package workspace

import (
	"errors"
	"io/fs"
	"os"
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

// TestReadConfinedRefusesWhatIsNotABoundedRegularFile covers the read every
// trusted-input loader shares. Each refusal here is a way a workspace path
// could otherwise reach a loader as something it did not ask for.
func TestReadConfinedRefusesWhatIsNotABoundedRegularFile(t *testing.T) {
	dir := canonicalTempDir(t)
	writeFile(t, dir, "plain.txt", "contents")
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("plain.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	data, err := ReadConfined(root, "plain.txt", 64)
	if err != nil || string(data) != "contents" {
		t.Fatalf("ReadConfined = %q, %v", data, err)
	}
	if _, err := ReadConfined(root, "big.txt", 10); err != nil {
		t.Fatalf("a file exactly at the bound was refused: %v", err)
	}

	for name, test := range map[string]struct {
		file  string
		limit int
		want  string
	}{
		"symlink":   {file: "link", limit: 64, want: "symbolic links"},
		"directory": {file: "directory", limit: 64, want: "not a regular file"},
		"oversized": {file: "big.txt", limit: 9, want: "exceeds"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadConfined(root, test.file, test.limit); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReadConfined(%q) = %v, want it to mention %q", test.file, err, test.want)
			}
		})
	}

	// A loader distinguishes "no such file" from a refusal, so the missing-file
	// error has to survive.
	if _, err := ReadConfined(root, "missing.txt", 64); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}
}
