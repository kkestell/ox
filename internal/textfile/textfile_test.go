package textfile

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadRejectsNonregularFilesWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(fifo, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, fifo, alias} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				_, err := Read(path)
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "regular file") {
					t.Fatalf("Read(%q) = %v", path, err)
				}
			case <-time.After(time.Second):
				// Release a reader blocked in open so a regression leaves no
				// goroutine behind after the test.
				writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				writer.Close()
				<-done
				t.Fatal("Read blocked on a nonregular file")
			}
		})
	}
}
