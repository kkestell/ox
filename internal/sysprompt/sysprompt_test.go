package sysprompt

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ox/internal/textfile"
)

func TestPromptAddsTheEnvironmentAndBoundedWorkspaceInstructions(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	path := filepath.Join(workspace, instructionsFile)
	prompt, err := ForWorkspace(home, workspace, "bash", false)
	if err != nil || !strings.HasPrefix(prompt, "You are Ox, a coding agent") ||
		!strings.HasSuffix(prompt, "# Environment\n\n- Workspace: "+workspace+"\n- Platform: "+runtime.GOOS+" ("+runtime.GOARCH+")\n- Shell: bash") {
		t.Fatalf("prompt without instructions: %q, %v", prompt, err)
	}
	if strings.Contains(prompt, "# Workers") {
		t.Errorf("prompt without swarm mentions workers: %q", prompt)
	}
	if swarm, _ := ForWorkspace(home, workspace, "bash", true); !strings.Contains(swarm, ".\n\n# Workers\n\nDelegate all substantial work to workers") ||
		!strings.Contains(swarm, "how to\ncheck its work.\n\n# Environment\n") {
		t.Errorf("swarm prompt = %q", swarm)
	}
	for _, test := range []struct {
		contents []byte
		want     string
		err      string
	}{
		{[]byte(" \n\t\n"), "", ""},
		{[]byte(strings.Repeat("a", textfile.MaxBytes)), strings.Repeat("a", textfile.MaxBytes), ""},
		{[]byte{0xff, 0xfe}, "", "AGENTS.md: not UTF-8 text"},
		{[]byte(strings.Repeat("a", textfile.MaxBytes+1)), "", "AGENTS.md: larger than 32 KiB"},
	} {
		os.WriteFile(path, test.contents, 0o666)
		text, err := readInstructions(path, instructionsFile)
		if text != test.want || (err == nil) != (test.err == "") || err != nil && err.Error() != test.err {
			t.Errorf("%.20q: %.20q, %v", test.contents, text, err)
		}
	}
	os.WriteFile(path, []byte("Answer in French.\n"), 0o666)
	prompt, _ = ForWorkspace(home, workspace, "bash", false)
	if !strings.HasSuffix(prompt, "\n\n# Workspace instructions from AGENTS.md\n\nAnswer in French.") {
		t.Errorf("prompt = %q", prompt)
	}
}

func TestPromptAddsUserInstructionsBeforeWorkspaceInstructions(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config/ox"), 0o777)
	os.WriteFile(filepath.Join(home, userInstructionsFile), []byte("Sign your commits.\n"), 0o666)
	prompt, err := ForWorkspace(home, workspace, "bash", false)
	if err != nil || !strings.HasSuffix(prompt, "- Shell: bash\n\n# User instructions from ~/.config/ox/AGENTS.md\n\nSign your commits.") {
		t.Fatalf("prompt = %q, %v", prompt, err)
	}
	os.WriteFile(filepath.Join(workspace, instructionsFile), []byte("Answer in French.\n"), 0o666)
	prompt, _ = ForWorkspace(home, workspace, "bash", false)
	if !strings.HasSuffix(prompt, "Sign your commits.\n\n# Workspace instructions from AGENTS.md\n\nAnswer in French.") {
		t.Errorf("prompt = %q", prompt)
	}
	os.WriteFile(filepath.Join(home, userInstructionsFile), []byte{0xff}, 0o666)
	if _, err := ForWorkspace(home, workspace, "bash", false); err == nil || err.Error() != "~/.config/ox/AGENTS.md: not UTF-8 text" {
		t.Errorf("err = %v", err)
	}
}
