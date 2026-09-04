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

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/shellrules"
	"github.com/kkestell/ox/internal/workspace"
)

const (
	shellDefaultTimeout         = 120
	shellMaximumTimeout         = 600
	shellWaitDelay              = 3 * time.Second
	shellTerminalCleanupTimeout = 5 * time.Second
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
	if command == "" {
		return "", errors.New("`command` must not be empty")
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
	if invocation.Terminal.Available() {
		return executeDelegatedShell(ctx, invocation, command, timeout)
	}
	return executeLocalShell(ctx, invocation, command, timeout)
}

func executeLocalShell(
	ctx context.Context,
	invocation agent.Invocation,
	command string,
	timeout int,
) (string, error) {
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

func executeDelegatedShell(
	ctx context.Context,
	invocation agent.Invocation,
	command string,
	timeout int,
) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	root := invocation.Root
	outputLimit := workspace.CollectionLimitBytes
	created, err := invocation.Terminal.Create(callCtx, acp.CreateTerminalRequest{
		SessionID:       invocation.SessionID,
		Command:         "/bin/sh",
		Args:            []string{"-c", command},
		Env:             terminalEnvironment(),
		CWD:             &root,
		OutputByteLimit: &outputLimit,
	})
	if err != nil {
		return "", fmt.Errorf("%s: %w", acp.MethodTerminalCreate, err)
	}
	if err := created.Validate(); err != nil {
		return "", fmt.Errorf("%s: %w", acp.MethodTerminalCreate, err)
	}
	terminalID := created.TerminalID

	exit, waitErr := invocation.Terminal.WaitForExit(
		callCtx,
		acp.WaitForTerminalExitRequest{
			SessionID:  invocation.SessionID,
			TerminalID: terminalID,
		},
	)
	stoppedByContext := waitErr != nil && callCtx.Err() != nil
	if waitErr != nil && !stoppedByContext {
		firstErr := fmt.Errorf("%s: %w", acp.MethodTerminalWaitForExit, waitErr)
		return "", releaseTerminal(invocation.Terminal, invocation.SessionID, terminalID, firstErr)
	}

	if stoppedByContext {
		return finishStoppedTerminal(ctx, invocation, terminalID, timeout)
	}

	output, outputErr := invocation.Terminal.Output(callCtx, acp.TerminalOutputRequest{
		SessionID:  invocation.SessionID,
		TerminalID: terminalID,
	})
	var firstErr error
	if outputErr != nil {
		firstErr = fmt.Errorf("%s: %w", acp.MethodTerminalOutput, outputErr)
	}
	firstErr = releaseTerminal(
		invocation.Terminal,
		invocation.SessionID,
		terminalID,
		firstErr,
	)
	if firstErr != nil {
		return "", firstErr
	}
	return renderDelegatedShellOutput(invocation, output.Output, output.Truncated, exit, 0)
}

func finishStoppedTerminal(
	ctx context.Context,
	invocation agent.Invocation,
	terminalID string,
	timeout int,
) (string, error) {
	var firstErr error
	killCtx, cancelKill := terminalCleanupContext()
	if err := invocation.Terminal.Kill(killCtx, acp.KillTerminalRequest{
		SessionID:  invocation.SessionID,
		TerminalID: terminalID,
	}); err != nil {
		firstErr = fmt.Errorf("%s: %w", acp.MethodTerminalKill, err)
	}
	cancelKill()

	outputCtx, cancelOutput := terminalCleanupContext()
	output, err := invocation.Terminal.Output(outputCtx, acp.TerminalOutputRequest{
		SessionID:  invocation.SessionID,
		TerminalID: terminalID,
	})
	cancelOutput()
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("%s: %w", acp.MethodTerminalOutput, err)
	}
	releaseCtx, cancelRelease := terminalCleanupContext()
	if err := invocation.Terminal.Release(releaseCtx, acp.ReleaseTerminalRequest{
		SessionID:  invocation.SessionID,
		TerminalID: terminalID,
	}); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("%s: %w", acp.MethodTerminalRelease, err)
	}
	cancelRelease()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if firstErr != nil {
		return "", firstErr
	}
	status := acp.WaitForTerminalExitResponse{}
	if output.ExitStatus != nil {
		status.ExitCode = output.ExitStatus.ExitCode
		status.Signal = output.ExitStatus.Signal
	}
	return renderDelegatedShellOutput(
		invocation,
		output.Output,
		output.Truncated,
		status,
		timeout,
	)
}

func releaseTerminal(
	terminal agent.ClientTerminal,
	sessionID string,
	terminalID string,
	firstErr error,
) error {
	cleanupCtx, cancel := terminalCleanupContext()
	defer cancel()
	if err := terminal.Release(cleanupCtx, acp.ReleaseTerminalRequest{
		SessionID:  sessionID,
		TerminalID: terminalID,
	}); err != nil && firstErr == nil {
		return fmt.Errorf("%s: %w", acp.MethodTerminalRelease, err)
	}
	return firstErr
}

func terminalCleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), shellTerminalCleanupTimeout)
}

func renderDelegatedShellOutput(
	invocation agent.Invocation,
	output string,
	truncated bool,
	status acp.WaitForTerminalExitResponse,
	timedOutAfter int,
) (string, error) {
	recorder := workspace.NewStreamRecorder(
		invocation.SpillDir,
		"shell",
		invocation.CallID,
		invocation.Emit,
	)
	defer recorder.Close()
	if _, err := recorder.Write([]byte(output)); err != nil {
		return "", err
	}
	rendered, err := recorder.Finish()
	if err != nil {
		return "", err
	}
	if rendered.Spilled != "" && invocation.ReportSpill != nil {
		invocation.ReportSpill(rendered.Spilled)
	}

	statusText := "exit status: unavailable"
	if status.ExitCode != nil {
		statusText = "exit code: " + strconv.Itoa(*status.ExitCode)
	} else if status.Signal != nil {
		statusText = "signal: " + *status.Signal
	}
	content := rendered.Content
	if content == "" {
		content = "(no output)"
	}
	if truncated {
		content = fmt.Sprintf(
			"[client output truncated to the last %d bytes]\n%s",
			workspace.CollectionLimitBytes,
			content,
		)
	}
	result := statusText + "\n" + content
	if timedOutAfter > 0 {
		result = fmt.Sprintf(
			"timed out after %d seconds; retry with a larger timeout if the command needs longer\n%s",
			timedOutAfter,
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

func terminalEnvironment() []acp.EnvVariable {
	environment := shellEnvironment()
	variables := make([]acp.EnvVariable, 0, len(environment))
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		variables = append(variables, acp.EnvVariable{Name: name, Value: value})
	}
	return variables
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
