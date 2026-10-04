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
	workspace := t.TempDir()
	path := filepath.Join(workspace, instructionsFile)
	prompt, err := ForWorkspace(workspace, "bash")
	if err != nil || !strings.HasPrefix(prompt, "You are Ox, a coding agent") ||
		!strings.HasSuffix(prompt, "# Environment\n\n- Workspace: "+workspace+"\n- Platform: "+runtime.GOOS+" ("+runtime.GOARCH+")\n- Shell: bash") {
		t.Fatalf("prompt without instructions: %q, %v", prompt, err)
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
		text, err := readInstructions(workspace)
		if text != test.want || (err == nil) != (test.err == "") || err != nil && err.Error() != test.err {
			t.Errorf("%.20q: %.20q, %v", test.contents, text, err)
		}
	}
	os.WriteFile(path, []byte("Answer in French.\n"), 0o666)
	prompt, _ = ForWorkspace(workspace, "bash")
	if !strings.HasSuffix(prompt, "\n\n# Workspace instructions from AGENTS.md\n\nAnswer in French.") {
		t.Errorf("prompt = %q", prompt)
	}
}
