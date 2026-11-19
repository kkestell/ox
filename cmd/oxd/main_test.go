package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListenReplacesOnlyAStaleSocket(t *testing.T) {
	// t.TempDir is too long for a socket path on macOS.
	dir, err := os.MkdirTemp("", "oxd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	stale := filepath.Join(dir, "stale.sock")
	var l *net.UnixListener
	l, err = net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	l.Close()
	if l, err := listen(stale); err != nil {
		t.Fatalf("a stale socket was not replaced: %v", err)
	} else {
		l.Close()
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, err := listen(file); err == nil {
		l.Close()
		t.Error("listen took the path of a regular file")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "kept" {
		t.Fatalf("listen changed the regular file at its path: %q, %v", data, err)
	}
}
