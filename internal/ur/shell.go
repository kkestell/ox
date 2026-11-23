package ur

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
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

// shellCommand is a shell call's checked arguments.
type shellCommand struct {
	command string
	timeout time.Duration
}

// parseShell checks a shell call's arguments.
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
	if args.Command == nil {
		return shellCommand{}, errors.New("arguments: missing field command")
	}
	seconds := 120
	if args.TimeoutSeconds != nil {
		seconds = *args.TimeoutSeconds
		if seconds < 1 || seconds > 600 {
			return shellCommand{}, errors.New("arguments: timeout_seconds must be from 1 through 600")
		}
	}
	return shellCommand{*args.Command, time.Duration(seconds) * time.Second}, nil
}

// outcome is a call's result, which the model reads and the transcript shows.
type outcome struct {
	text    string
	isError bool
}

// commands counts the running commands, so oxd can wait for them to end.
var commands sync.WaitGroup

// WaitAll returns once every command has ended and its process group has been
// killed. The daemon calls it after stopping every session.
func WaitAll() {
	commands.Wait()
}

// runShell runs c in workspace until it exits, its timeout passes, or ctx is
// done, then kills its process group, which ends the background processes it
// left.
func runShell(ctx context.Context, workspace string, c shellCommand) outcome {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", c.command)
	cmd.Dir = workspace
	cmd.Env = environment()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd.Process.Pid) }
	// A descendant holding the output pipe open holds Wait at most this long.
	cmd.WaitDelay = time.Second
	output := &capture{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		return outcome{"Could not start bash: " + err.Error(), true}
	}
	cmd.Wait()
	killGroup(cmd.Process.Pid)
	status := cmd.ProcessState.Sys().(syscall.WaitStatus)
	var line string
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		line = fmt.Sprintf("Timed out after %d seconds", int(c.timeout/time.Second))
	case status.Signaled():
		line = fmt.Sprintf("Terminated by signal: %d", status.Signal())
	default:
		line = fmt.Sprintf("Exit code: %d", status.ExitStatus())
	}
	return outcome{line + "\n\n" + output.text(), line != "Exit code: 0"}
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
