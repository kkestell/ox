// Package servertest builds the Ox binaries for client tests and runs
// ox-server against a scripted OpenRouter.
package servertest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ox/internal/openroutertest"
	"ox/internal/settings"
)

var directory string

// Run builds ox and ox-server into one directory, runs the tests, and removes
// the directory. Call it from TestMain.
func Run(m *testing.M) int {
	var err error
	directory, err = os.MkdirTemp("", "ox-servertest-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(directory)
	build := exec.Command("go", "build", "-o", directory+string(filepath.Separator), "ox/cmd/ox", "ox/cmd/ox-server")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the Ox binaries: %v\n%s", err, output)
		return 1
	}
	return m.Run()
}

// Binary returns the path of the built ox or ox-server.
func Binary(name string) string {
	return filepath.Join(directory, name)
}

// Environment writes global settings naming openroutertest.DefaultModel under
// root and returns the variables that give ox-server root as its home and data
// directory and point it at endpoint.
func Environment(t testing.TB, root, endpoint string) []string {
	t.Helper()
	config := filepath.Join(root, ".config/ox")
	if err := os.MkdirAll(config, 0o777); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"model": openroutertest.DefaultModel})
	if err := os.WriteFile(filepath.Join(config, "settings.json"), data, 0o666); err != nil {
		t.Fatal(err)
	}
	return []string{
		"HOME=" + root,
		"OX_DATA_DIR=" + filepath.Join(root, "data"),
		"OPENROUTER_API_KEY=test-key",
		"OX_OPENROUTER_ENDPOINT=" + endpoint,
	}
}

// Start starts a scripted OpenRouter that answers the catalog request and
// then replies in order, and points ox-server at it through the test's
// environment. It returns the server to start and an empty workspace.
func Start(t *testing.T, replies ...openroutertest.Reply) (settings.Server, string) {
	t.Helper()
	openrouter := openroutertest.Start(t, append([]openroutertest.Reply{openroutertest.CurrentCatalog()}, replies...)...)
	root := t.TempDir()
	for _, variable := range Environment(t, root, openrouter.URL) {
		name, value, _ := strings.Cut(variable, "=")
		t.Setenv(name, value)
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o777); err != nil {
		t.Fatal(err)
	}
	return settings.Server{Command: Binary("ox-server"), Args: []string{"acp"}}, workspace
}
