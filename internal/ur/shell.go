package ur

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/children"
)

// shellTool is the shell tool's definition, which every request offers.
var shellTool = json.RawMessage(`{
  "type": "function",
  "function": {
    "name": "shell",
    "description": "Run a bash command with bash -c, starting in the session's workspace directory, and return its exit status and the start and end of its combined stdout and stderr, at most 16 KiB. Each call starts a fresh shell with stdin from /dev/null, so directory changes and exported variables do not carry over. The middle of long output is omitted; run a narrower command or redirect long output to a file to inspect it. Commands run with the user's permissions and can reach paths outside the workspace.",
    "parameters": {
      "type": "object",
      "properties": {
        "command": {"type": "string", "description": "The command or multiline script to run."},
        "timeout_seconds": {"type": "integer", "minimum": 1, "maximum": 600, "default": 120, "description": "Seconds the command may run before it and every process it started are killed. Defaults to 120."}
      },
      "required": ["command"],
      "additionalProperties": false
    }
  }
}`)

// half is the size of the start and of the end of the output that a command's
// outcome keeps.
const half = 8 << 10

// shellCommand is a shell call's checked arguments. It is what the transcript
// and the approval card show, and what runs.
type shellCommand struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (c shellCommand) timeout() time.Duration {
	return time.Duration(c.TimeoutSeconds) * time.Second
}

// parseShell checks a shell call's arguments, which are one JSON object.
func parseShell(arguments string) (shellCommand, error) {
	var args struct {
		Command        *string `json:"command"`
		TimeoutSeconds *int    `json:"timeout_seconds"`
	}
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return shellCommand{}, fmt.Errorf("arguments: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return shellCommand{}, errors.New("arguments: trailing data after the JSON object")
	}
	if args.Command == nil {
		return shellCommand{}, errors.New("arguments: missing field command")
	}
	c := shellCommand{Command: *args.Command, TimeoutSeconds: 120}
	if args.TimeoutSeconds != nil {
		c.TimeoutSeconds = *args.TimeoutSeconds
		if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 600 {
			return shellCommand{}, errors.New("arguments: timeout_seconds must be from 1 through 600")
		}
	}
	return c, nil
}

// outcome is a call's result, which the model reads and the transcript shows.
type outcome struct {
	text    string
	isError bool
}

// shell is a started command.
type shell struct {
	cmd     *exec.Cmd
	ctx     context.Context
	cancel  context.CancelFunc
	timeout int // seconds
	output  *capture
}

// startShell starts c in workspace and registers it with children. It runs
// until it exits, its timeout passes, or ctx is done.
func startShell(ctx context.Context, workspace string, c shellCommand) (*shell, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	cmd := exec.CommandContext(ctx, "bash", "-c", c.Command)
	cmd.Dir = workspace
	cmd.Env = environment()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd.Process.Pid) }
	// A descendant holding the output pipe open holds Wait at most this long.
	cmd.WaitDelay = time.Second
	output := &capture{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := children.Start(cmd, func() { killGroup(cmd.Process.Pid) }); err != nil {
		cancel()
		return nil, err
	}
	return &shell{cmd, ctx, cancel, c.TimeoutSeconds, output}, nil
}

// wait waits for the command to end, then kills its process group, which ends
// the background processes it left.
func (s *shell) wait() outcome {
	defer s.cancel()
	s.cmd.Wait()
	killGroup(s.cmd.Process.Pid)
	children.Done(s.cmd)
	status := s.cmd.ProcessState.Sys().(syscall.WaitStatus)
	var line string
	switch {
	case status.Exited():
		line = fmt.Sprintf("Exit code: %d", status.ExitStatus())
	case errors.Is(s.ctx.Err(), context.DeadlineExceeded):
		line = fmt.Sprintf("Timed out after %d seconds", s.timeout)
	default:
		line = fmt.Sprintf("Terminated by signal: %d", status.Signal())
	}
	return outcome{line + "\n\n" + s.output.text(), line != "Exit code: 0"}
}

// environment is oxd's environment without the OpenRouter key.
func environment() []string {
	var env []string
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "OPENROUTER_API_KEY=") {
			env = append(env, variable)
		}
	}
	return env
}

// killGroup kills the process group led by pid. A group that has already
// ended is not an error.
func killGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// capture keeps the first and the latest half bytes written to it.
type capture struct {
	head, tail []byte
	total      int
}

func (c *capture) Write(p []byte) (int, error) {
	c.total += len(p)
	take := min(half-len(c.head), len(p))
	c.head = append(c.head, p[:take]...)
	c.tail = append(c.tail, p[take:]...)
	if len(c.tail) > half {
		c.tail = bytes.Clone(c.tail[len(c.tail)-half:])
	}
	return len(p), nil
}

// text is the output, with the count of the bytes dropped between its start
// and end. Invalid UTF-8 becomes U+FFFD.
func (c *capture) text() string {
	if c.total == 0 {
		return "(no output)"
	}
	omitted := c.total - len(c.head) - len(c.tail)
	if omitted == 0 {
		return valid(string(c.head) + string(c.tail))
	}
	return fmt.Sprintf("%s\n[... %d bytes omitted ...]\n%s", valid(string(c.head)), omitted, valid(string(c.tail)))
}

func valid(s string) string {
	return strings.ToValidUTF8(s, "�")
}
