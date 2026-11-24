package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/children"
)

// Ask runs Claude once with instructions as its whole system prompt and text
// as the prompt, and returns its reply. Claude runs without tools, MCP
// servers, project instructions, hooks, or a saved session.
func Ask(ctx context.Context, model, effort, instructions, text string) (string, error) {
	args := []string{
		"-p", "--output-format=json", "--safe-mode", "--no-session-persistence",
		"--tools=", "--disallowedTools=mcp__*", "--strict-mcp-config", "--disable-slash-commands",
		"--system-prompt", instructions, "--model=" + model,
	}
	if effort != "" {
		args = append(args, "--effort="+effort)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = os.TempDir()
	cmd.Env = subscriptionEnv(cmd)
	cmd.Stdin = strings.NewReader(text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err := children.Start(cmd, func() { cmd.Process.Kill() }); err != nil {
		return "", err
	}
	waitErr := cmd.Wait()
	children.Done(cmd)
	var result message
	if json.Unmarshal(stdout.Bytes(), &result) == nil && result.Type == "result" {
		if result.failed() {
			return "", errors.New(result.errorText())
		}
		return result.Result, nil
	}
	if out := strings.TrimSpace(stdout.String()); out != "" {
		return "", errors.New(out)
	}
	if out := strings.TrimSpace(stderr.String()); out != "" {
		return "", errors.New(out)
	}
	if waitErr != nil {
		return "", waitErr
	}
	return "", errors.New("claude printed no result")
}
