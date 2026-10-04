// Package sysprompt builds Ox's system prompt from its built-in prompt, the
// environment, and workspace instructions.
package sysprompt

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"

	"ox/internal/textfile"
)

//go:embed system_prompt.md
var builtIn string

const instructionsFile = "AGENTS.md"

// ForWorkspace builds the system prompt for a workspace. The caller captures
// it once when a session becomes active.
func ForWorkspace(workspace, shell string) (string, error) {
	instructions, err := readInstructions(workspace)
	if err != nil {
		return "", err
	}
	prompt := strings.TrimRight(builtIn, " \t\r\n") + fmt.Sprintf(
		"\n\n# Environment\n\n- Workspace: %s\n- Platform: %s (%s)\n- Shell: %s",
		workspace, runtime.GOOS, runtime.GOARCH, shell)
	if instructions != "" {
		prompt += "\n\n# Workspace instructions from AGENTS.md\n\n" + strings.TrimRight(instructions, " \t\r\n")
	}
	return prompt, nil
}

// readInstructions reads `<workspace>/AGENTS.md`: empty when the file is
// missing or blank, and an error when it cannot be read, is not UTF-8, or is
// too large.
func readInstructions(workspace string) (string, error) {
	text, err := textfile.Read(filepath.Join(workspace, instructionsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", instructionsFile, err)
	}
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	return text, nil
}
