package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveProcessPathsAppliesOneXDGPolicy pins the rule every per-user
// location shares. A relative XDG value must not be honored: durable session
// storage would otherwise follow whatever directory Ox started in.
func TestResolveProcessPathsAppliesOneXDGPolicy(t *testing.T) {
	for _, test := range []struct {
		name     string
		xdgValue string
		home     string
		want     string
	}{
		{
			name:     "an absolute XDG value wins",
			xdgValue: "/var/state/user",
			home:     "/home/user",
			want:     filepath.Join("/var/state/user", "ox", "sessions"),
		},
		{
			name:     "a relative XDG value falls back to home",
			xdgValue: "relative",
			home:     "/home/user",
			want:     filepath.Join("/home/user", ".local", "share", "ox", "sessions"),
		},
		{
			name: "no absolute base yields no path",
			home: "relative",
		},
		{name: "nothing set yields no path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := storePath(
				test.xdgValue, test.home,
				[]string{".local", "share"}, "ox", "sessions",
			)
			if got != test.want {
				t.Fatalf("storePath = %q, want %q", got, test.want)
			}
		})
	}
}

// TestResolveProcessPathsPlacesEveryStore checks that each location lands where
// its XDG base directory says it should.
func TestResolveProcessPathsPlacesEveryStore(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")

	paths := resolveProcessPaths()
	for name, expected := range map[string][2]string{
		"settings":     {paths.settings, filepath.Join("/home/user", ".config", "ox", "settings.json")},
		"sessions":     {paths.sessions, filepath.Join("/home/user", ".local", "share", "ox", "sessions")},
		"memory":       {paths.memory, filepath.Join("/home/user", ".local", "share", "ox", "memory")},
		"modelCatalog": {paths.modelCatalog, filepath.Join("/home/user", ".cache", "ox", "models.json")},
	} {
		if expected[0] != expected[1] {
			t.Errorf("%s path = %q, want %q", name, expected[0], expected[1])
		}
	}

	t.Setenv("XDG_DATA_HOME", "/var/state/user")
	paths = resolveProcessPaths()
	if paths.sessions != filepath.Join("/var/state/user", "ox", "sessions") ||
		paths.memory != filepath.Join("/var/state/user", "ox", "memory") {
		t.Fatalf("XDG data paths = %q, %q", paths.sessions, paths.memory)
	}
	if paths.settings != filepath.Join("/home/user", ".config", "ox", "settings.json") {
		t.Fatalf("settings path followed the data directory: %q", paths.settings)
	}
}

// TestShippedBinaryDoesNotLinkTesting guards against a test helper named so
// that Go does not treat it as one. A file whose name does not end in
// _test.go compiles into the binary and drags its imports along with it.
func TestShippedBinaryDoesNotLinkTesting(t *testing.T) {
	command := exec.Command("go", "list", "-deps", ".")
	command.Env = append(os.Environ(), "GOFLAGS=")
	output, err := command.Output()
	if err != nil {
		t.Skipf("cannot list dependencies: %v", err)
	}
	for _, dependency := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if dependency == "testing" {
			t.Fatal("the ox binary depends on testing; a test helper is misnamed")
		}
	}
}
