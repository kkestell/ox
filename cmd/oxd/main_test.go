package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/claude"
	"github.com/kkestell/ox/internal/config"
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

func TestConfigOffersOnlyClaudesEfforts(t *testing.T) {
	builtIn, err := config.Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	harnesses := map[string]agent.Start{"claude": claude.Start}
	if err := checkConfig(builtIn, harnesses); err != nil {
		t.Fatalf("the built-in configuration was refused: %v", err)
	}
	misspelled := &config.Config{Harnesses: map[string]config.Harness{"claude": {
		Models: []config.Model{{ID: "claude-haiku-5-5", Efforts: []config.Option{{ID: "hgih"}}}},
	}}}
	if err := checkConfig(misspelled, harnesses); err == nil {
		t.Error("a Claude effort level Claude does not take was accepted")
	}
}
