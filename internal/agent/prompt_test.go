package agent

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestComposePromptAppendsEnvironmentAfterBasePrompt(t *testing.T) {
	now := time.Date(2026, time.July, 27, 23, 59, 0, 0, time.FixedZone("test", -6*60*60))
	prompt := composePrompt("/workspace/project", now)

	base := strings.TrimSpace(basePrompt)
	if base == "" || !strings.HasPrefix(prompt, base+"\n\n<environment>\n") {
		t.Fatal("prompt does not start with the non-empty base text and environment block")
	}
	wantEnvironment := "<environment>\n" +
		"<workspace-root>/workspace/project</workspace-root>\n" +
		"<platform>" + runtime.GOOS + "</platform>\n" +
		"<current-date>2026-07-27</current-date>\n" +
		"</environment>"
	if !strings.Contains(prompt, wantEnvironment+"\n\nFile tools resolve relative paths") {
		t.Fatalf("prompt = %q, want environment followed by file-tool guidance", prompt)
	}
}
