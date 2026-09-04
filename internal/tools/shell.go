package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/shellrules"
	"github.com/kkestell/ox/internal/workspace"
)

const (
	shellDefaultTimeout = 120
	shellMaximumTimeout = 600
	shellWaitDelay      = 3 * time.Second
)

const shellDescription = "Run a command through /bin/sh -c from the workspace root. " +
	"The command runs on the host with the user's authority. Stdout and stderr are " +
	"combined, streamed live, and spilled to a readable file when large."

const shellSchema = `{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "The shell command to execute from the workspace root."
		},
		"timeout": {
			"type": "integer",
			"minimum": 1,
			"maximum": 600,
			"default": 120,
			"description": "Wall-clock timeout in seconds."
		}
	},
	"required": ["command"],
	"additionalProperties": false
}`

type shellArguments struct {
	Command *string `json:"command"`
	Timeout *int    `json:"timeout"`
}

func executeShell(ctx context.Context, invocation agent.Invocation) (string, error) {
	var arguments shellArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	command, err := requireString("command", arguments.Command)
	if err != nil {
		return "", err
	}
	timeout := shellDefaultTimeout
	if arguments.Timeout != nil {
		timeout = *arguments.Timeout
	}
	if timeout < 1 || timeout > shellMaximumTimeout {
		return "", fmt.Errorf(
			"`timeout` must be an integer between 1 and %d",
			shellMaximumTimeout,
		)
	}
	if invocation.FileReads != nil {
		invocation.FileReads.Clear()
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(callCtx, "/bin/sh", "-c", command)
	cmd.Dir = invocation.Root
	cmd.Stdin = nil
	cmd.Env = shellEnvironment()

	recorder := workspace.NewStreamRecorder(
		invocation.SpillDir,
		"shell",
		invocation.CallID,
		invocation.Emit,
	)
	defer recorder.Close()
	cmd.Stdout = recorder
	cmd.Stderr = recorder
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = shellWaitDelay

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to spawn shell: %w", err)
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	timedOut := errors.Is(callCtx.Err(), context.DeadlineExceeded)
	exitCode, err := shellExitCode(cmd, waitErr)
	if err != nil {
		return "", err
	}
	rendered, err := recorder.Finish()
	if err != nil {
		return "", err
	}
	if rendered.Spilled != "" && invocation.ReportSpill != nil {
		invocation.ReportSpill(rendered.Spilled)
	}

	status := "terminated by signal"
	if exitCode != nil {
		status = strconv.Itoa(*exitCode)
	}
	output := rendered.Content
	if output == "" {
		output = "(no output)"
	}
	result := "exit code: " + status + "\n" + output
	if timedOut {
		result = fmt.Sprintf(
			"timed out after %d seconds; retry with a larger timeout if the command needs longer\n%s",
			timeout,
			result,
		)
	}
	return result, nil
}

func shellExitCode(cmd *exec.Cmd, waitErr error) (*int, error) {
	if waitErr == nil {
		zero := 0
		return &zero, nil
	}
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		if cmd.ProcessState.Exited() {
			code := cmd.ProcessState.ExitCode()
			return &code, nil
		}
		return nil, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if exitErr.Exited() {
			code := exitErr.ExitCode()
			return &code, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("failed to run shell command: %w", waitErr)
}

func shellEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "OPENROUTER_API_KEY" ||
			strings.HasPrefix(key, "OX_") ||
			key == "PAGER" ||
			key == "GIT_PAGER" ||
			key == "GIT_TERMINAL_PROMPT" ||
			key == "TERM" ||
			key == "NO_COLOR" {
			continue
		}
		environment = append(environment, entry)
	}
	return append(
		environment,
		"PAGER=cat",
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
		"TERM=dumb",
		"NO_COLOR=1",
	)
}

func shellSuggestion(arguments json.RawMessage) string {
	var value shellArguments
	if decodeArgs(arguments, &value) != nil || value.Command == nil {
		return ""
	}
	return shellrules.Suggest(*value.Command)
}

func shellCovered(rules []string, arguments json.RawMessage) bool {
	var value shellArguments
	if decodeArgs(arguments, &value) != nil || value.Command == nil {
		return false
	}
	return shellrules.Allowed(rules, *value.Command)
}
