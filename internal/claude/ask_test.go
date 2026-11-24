package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Ask runs Claude once without tools, MCP servers, or an API key, with the
// text on stdin, and returns the result's text or the reason it failed.
func TestAsk(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	dir := t.TempDir()
	args, stdin, key := filepath.Join(dir, "args"), filepath.Join(dir, "stdin"), filepath.Join(dir, "key")
	script(t, "#!/bin/sh\n"+
		`for a in "$@"; do printf '%s\n' "$a"; done > `+args+"\n"+
		"cat > "+stdin+"\n"+
		`printf '%s' "${ANTHROPIC_API_KEY-unset}" > `+key+"\n"+
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"A title"}'`+"\n")
	reply, err := Ask(t.Context(), "claude-haiku-5-5", "low", "Write a title.", "the prompt")
	if err != nil || reply != "A title" {
		t.Fatalf("Ask = %q, %v", reply, err)
	}
	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	gotArgs := strings.Split(strings.TrimSuffix(read(args), "\n"), "\n")
	for _, want := range []string{
		"-p", "--output-format=json", "--safe-mode", "--no-session-persistence", "--tools=",
		"--disallowedTools=mcp__*", "--strict-mcp-config", "--disable-slash-commands",
		"--model=claude-haiku-5-5", "--effort=low",
	} {
		if !slices.Contains(gotArgs, want) {
			t.Errorf("arguments %q lack %q", gotArgs, want)
		}
	}
	if i := slices.Index(gotArgs, "--system-prompt"); i < 0 || i+1 >= len(gotArgs) || gotArgs[i+1] != "Write a title." {
		t.Errorf("arguments %q do not set the system prompt", gotArgs)
	}
	if got := read(stdin); got != "the prompt" {
		t.Errorf("stdin = %q", got)
	}
	if got := read(key); got != "unset" {
		t.Errorf("ANTHROPIC_API_KEY = %q", got)
	}

	for _, c := range []struct{ name, script, want string }{
		{"error result", `echo '{"type":"result","subtype":"success","is_error":true,"result":"model not found"}'; exit 1`, "model not found"},
		{"not logged in", `echo 'Not logged in · Please run /login'; exit 1`, "Not logged in · Please run /login"},
		{"stderr reason", `echo 'claude: bad flag' >&2; exit 2`, "claude: bad flag"},
	} {
		script(t, "#!/bin/sh\ncat > /dev/null\n"+c.script+"\n")
		if reply, err := Ask(t.Context(), "m", "", "i", "t"); err == nil || err.Error() != c.want {
			t.Errorf("%s: Ask = %q, %v; want error %q", c.name, reply, err, c.want)
		}
	}

	script(t, "#!/bin/sh\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Ask(ctx, "m", "", "i", "t"); err == nil {
		t.Error("a cancelled Ask succeeded")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("a cancelled Ask returned after %v", elapsed)
	}
}
