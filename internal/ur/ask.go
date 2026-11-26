package ur

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/children"
)

// ask runs ur -ask once with instructions as the system message and text as
// the user's, and returns the reply. Its error is ur's stderr.
func ask(ctx context.Context, command, endpoint string, providers map[string][]string, model, effort, instructions, text string) (string, error) {
	args := append([]string{"-ask", "-instructions=" + instructions}, requestFlags(endpoint, providers, model, effort)...)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = os.TempDir()
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
	if waitErr == nil {
		return stdout.String(), nil
	}
	if out := strings.TrimSpace(stderr.String()); out != "" {
		return "", errors.New(out)
	}
	return "", waitErr
}
