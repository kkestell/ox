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

const (
	instructionsFile     = "AGENTS.md"
	userInstructionsFile = ".config/ox/AGENTS.md"
)

// ForWorkspace builds the system prompt for a workspace, with the user
// instructions in home. The caller captures it once when a session becomes
// active.
func ForWorkspace(home, workspace, shell string) (string, error) {
	userInstructions, err := readInstructions(filepath.Join(home, userInstructionsFile), "~/"+userInstructionsFile)
	if err != nil {
		return "", err
	}
	instructions, err := readInstructions(filepath.Join(workspace, instructionsFile), instructionsFile)
	if err != nil {
		return "", err
	}
	var prompt strings.Builder
	err = promptTemplate.Execute(&prompt, struct {
		Workspace, Platform, Shell, UserInstructions, Instructions string
	}{
		Workspace:        workspace,
		Platform:         runtime.GOOS + " (" + runtime.GOARCH + ")",
		Shell:            shell,
		UserInstructions: userInstructions,
		Instructions:     instructions,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimRight(prompt.String(), " \t\r\n"), nil
}

// readInstructions reads the instructions file at path, which errors call
// shown: empty when the file is missing or blank, and an error when it cannot
// be read, is not UTF-8, or is too large.
func readInstructions(path, shown string) (string, error) {
	text, err := textfile.Read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", shown, err)
	}
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	return strings.TrimRight(text, " \t\r\n"), nil
}
