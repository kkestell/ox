package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ox/internal/settings"
)

func TestParseOptions(t *testing.T) {
	for _, test := range []struct {
		args []string
		want options
	}{
		{nil, options{dir: "."}},
		{[]string{"--dir", "d"}, options{dir: "d"}},
		{[]string{"--server", "X"}, options{dir: ".", server: "X"}},
		{[]string{"-V"}, options{dir: ".", version: true}},
	} {
		got, err := parseOptions(test.args)
		if err != nil || got != test.want {
			t.Errorf("%q: %+v, %v", test.args, got, err)
		}
	}
	for _, args := range [][]string{{"login"}, {"--dir"}, {"--dir", "d", "run"}, {"--unknown"}} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
}

func TestWorkspaceRequiresAnExistingDirectory(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace(root); err != nil {
		t.Error(err)
	}
	if _, err := workspace(filepath.Join(root, "missing")); err == nil {
		t.Error("accepted a missing directory")
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace(file); err == nil {
		t.Error("accepted a file")
	}
}

func TestSelectionRequiresANameUnlessExactlyOneServerExists(t *testing.T) {
	servers := func(names ...string) []settings.Server {
		var servers []settings.Server
		for _, name := range names {
			servers = append(servers, settings.Server{Name: name, Command: "server"})
		}
		return servers
	}
	if _, err := selectServer(nil, ""); err == nil {
		t.Error("selected from no servers")
	}
	if server, err := selectServer(servers("Alpha"), ""); err != nil || server.Name != "Alpha" {
		t.Errorf("%+v, %v", server, err)
	}
	several := servers("Alpha", "Beta")
	if _, err := selectServer(several, ""); err == nil || !strings.Contains(err.Error(), "Alpha, Beta") {
		t.Errorf("%v", err)
	}
	if server, err := selectServer(several, "Beta"); err != nil || server.Name != "Beta" {
		t.Errorf("%+v, %v", server, err)
	}
	if _, err := selectServer(several, "missing"); err == nil {
		t.Error("selected a missing server")
	}
}

func TestInvalidStartupDoesNotLaunchAServer(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "launched")
	config := filepath.Join(root, ".config/ox")
	if err := os.MkdirAll(config, 0o777); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"servers": []map[string]any{
		{"name": "Fake", "command": "/bin/sh", "args": []string{"-c", "touch " + marker}},
	}})
	if err := os.WriteFile(filepath.Join(config, "settings.json"), data, 0o666); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	for _, args := range [][]string{{"--server", "missing"}, {"--dir", "/nonexistent-ox-test-directory"}} {
		if err := run(args); err == nil {
			t.Errorf("%q started", args)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("launched the server")
	}
}
