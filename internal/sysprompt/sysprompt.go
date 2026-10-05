// Package sysprompt builds Ox's system prompt by filling its built-in template
// with the environment and workspace instructions.
package sysprompt

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"ox/internal/textfile"
)

//go:embed system_prompt.md
var builtIn string

var promptTemplate = template.Must(template.New("system_prompt.md").Parse(builtIn))

const instructionsFile = "AGENTS.md"

// ForWorkspace builds the system prompt for a workspace. The caller captures
// it once when a session becomes active.
func ForWorkspace(workspace, shell string) (string, error) {
	instructions, err := readInstructions(workspace)
	if err != nil {
		return "", err
	}
	var prompt strings.Builder
	err = promptTemplate.Execute(&prompt, struct {
		Workspace, Platform, Shell, Instructions string
	}{
		Workspace:    workspace,
		Platform:     runtime.GOOS + " (" + runtime.GOARCH + ")",
		Shell:        shell,
		Instructions: instructions,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimRight(prompt.String(), " \t\r\n"), nil
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
