package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var oxBinary string

func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_E2E_MCP_SHUTDOWN_HELPER") == "enabled" {
		os.Exit(m.Run())
	}
	directory, err := os.MkdirTemp("", "ox-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create e2e build directory:", err)
		os.Exit(1)
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "get e2e working directory:", err)
		os.Exit(1)
	}
	moduleRoot, err := filepath.Abs(filepath.Join(workingDirectory, "..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve module root:", err)
		os.Exit(1)
	}

	oxBinary = filepath.Join(directory, "ox")
	arguments := []string{"build", "-tags=oxe2e"}
	if raceEnabled {
		arguments = append(arguments, "-race")
	}
	arguments = append(arguments, "-ldflags",
		"-X=github.com/kkestell/ox/internal/mcp.connectTimeoutSetting=2s "+
			"-X=github.com/kkestell/ox/internal/mcp.callTimeoutSetting=2s",
	)
	arguments = append(arguments, "-o", oxBinary, "./cmd/ox")
	build := exec.Command("go", arguments...)
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build ox: %v\n%s", err, output)
		os.Exit(1)
	}

	code := m.Run()
	if err := os.RemoveAll(directory); err != nil {
		fmt.Fprintln(os.Stderr, "remove e2e build directory:", err)
		code = 1
	}
	os.Exit(code)
}
