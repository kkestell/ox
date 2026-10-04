package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"ox/internal/shellproc"
	"ox/internal/transcript"
)

// ShellProgram is found on PATH, so a newer bash takes precedence over an old
// /bin/bash.
const ShellProgram = "bash"

const (
	shellBodyLimit        = 14 * 1024
	defaultTimeoutSeconds = 120
	maxWaitSeconds        = 30
	maxInputBytes         = 16 * 1024
)

var shellSchema = compact(`{
  "type": "function",
  "function": {
    "name": "shell",
    "description": "Run a bash command starting in the session workspace. An ordinary call waits for the command to finish and returns the exit status and the start and end of stdout and stderr, at most 16 KiB total. Output has a shared 14 KiB budget: 7 KiB per stream, with unused space given to the other stream. The middle of long output is omitted; rerun a narrower command or redirect long output to a file to inspect it. Output is already bounded, so run a command directly rather than piping it through tail or head: a pipeline reports only its last command's exit status. Each ordinary call starts a fresh shell in the workspace with stdin connected to /dev/null, so directory changes and exported variables do not carry over. Set background to true for a development server, watcher, or long build: the call returns a process ID as soon as the command starts, and the command keeps running across turns until it exits, shell_process stops it, the session is deleted, or Ox exits. Commands run with Ox's permissions and can access paths outside the workspace.",
    "parameters": {
      "type": "object",
      "properties": {
        "command": {"type": "string", "description": "Shell command or multiline script. Use shell syntax for directory changes, environment overrides, pipelines, and redirection."},
        "timeout_seconds": {"type": "integer", "minimum": 1, "maximum": 600, "default": 120, "description": "Maximum execution time in seconds of an ordinary call. Defaults to 120. Not allowed with background."},
        "background": {"type": "boolean", "default": false, "description": "Start the command with piped stdin and return its process ID without waiting for it. Background commands have no timeout; use shell_process to read their output, write to their stdin, or stop them. Keep the main process in the foreground of this shell, as in ` + "`npm run dev` rather than `npm run dev &`" + `."}
      },
      "required": ["command"],
      "additionalProperties": false
    }
  }
}`)

var shellProcessSchema = compact(`{
  "type": "function",
  "function": {
    "name": "shell_process",
    "description": "Inspect or control the background commands you started with shell and background true. list returns each process ID, command, and state, including processes that have exited or been stopped. read returns the state and the retained tails of stdout and stderr, optionally waiting up to wait_seconds for the command to end; reads do not consume output, so repeated reads may repeat it. write sends text to stdin exactly as given, adding no newline, and close_stdin closes stdin afterward. stop sends SIGTERM to the command's process group, then SIGKILL after 2 seconds. Process IDs are not operating-system PIDs and are valid only for the background commands you started.",
    "parameters": {
      "type": "object",
      "properties": {
        "action": {"type": "string", "enum": ["list", "read", "write", "stop"], "description": "What to do."},
        "process_id": {"type": "string", "description": "The process ID returned by shell. Required for read, write, and stop."},
        "wait_seconds": {"type": "integer", "minimum": 0, "maximum": 30, "default": 0, "description": "For read only: seconds to wait for the command to end before returning. Reaching the limit leaves the command running. Defaults to 0."},
        "text": {"type": "string", "description": "For write only, and required there: UTF-8 text of at most 16 KiB, sent exactly as given. Include \\n to end a line. May be empty only with close_stdin."},
        "close_stdin": {"type": "boolean", "default": false, "description": "For write only: close stdin after writing, which signals end of input."}
      },
      "required": ["action"],
      "additionalProperties": false
    }
  }
}`)

func (t *Toolbox) command(text string) *exec.Cmd {
	cmd := exec.Command(ShellProgram, "-c", text)
	cmd.Dir = t.Workspace
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "OPENROUTER_API_KEY=") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	return cmd
}

func (t *Toolbox) shell(ctx context.Context, arguments string) transcript.ToolOutcome {
	var args struct {
		Command        *string         `json:"command"`
		TimeoutSeconds json.RawMessage `json:"timeout_seconds"`
		Background     bool            `json:"background"`
	}
	if err := decodeArguments(arguments, &args); err != nil {
		return shellFailure(err.Error())
	}
	if args.Command == nil {
		return shellFailure("arguments: missing field `command`")
	}
	seconds := uint64(defaultTimeoutSeconds)
	if args.TimeoutSeconds != nil {
		if args.Background {
			return shellFailure("arguments: timeout_seconds is not allowed with background; background commands have no timeout")
		}
		if string(args.TimeoutSeconds) == "null" || json.Unmarshal(args.TimeoutSeconds, &seconds) != nil {
			return shellFailure("arguments: timeout_seconds must be an integer")
		}
		if seconds < 1 || seconds > 600 {
			return shellFailure("arguments: timeout_seconds must be from 1 through 600")
		}
	}
	if ctx.Err() != nil {
		return transcript.Cancelled("Cancelled before this tool was started.")
	}
	cmd := t.command(*args.Command)
	if args.Background {
		// Once started, the start is the observed outcome; later
		// cancellation does not undo it.
		process, err := t.Processes.Start(cmd, *args.Command, shellBodyLimit)
		if err != nil {
			return shellFailure(fmt.Sprintf("Could not start %s in %s: %v", ShellProgram, t.Workspace, err))
		}
		return transcript.Completed(fmt.Sprintf("Started shell process %s.\nThe command is running in the background. This confirms that it started, not that it finished or is ready. Use shell_process to read its output, write to its stdin, or stop it.", process.ID))
	}
	timeout := time.Duration(seconds) * time.Second
	result, err := shellproc.Run(ctx, cmd, shellBodyLimit, timeout)
	if err != nil {
		return shellFailure(fmt.Sprintf("Could not start %s in %s: %v", ShellProgram, t.Workspace, err))
	}
	var status string
	switch result.Outcome {
	case shellproc.Exited:
		status = shellproc.ExitText(result.Status)
	case shellproc.TimedOut:
		status = fmt.Sprintf("Timed out after %d seconds; partial changes may remain.", seconds)
	case shellproc.Cancelled:
		status = "Cancelled during execution; partial changes may remain."
	}
	text, content := report(status, result.Stdout, result.Stderr, result.Diagnostics)
	switch {
	case result.Outcome == shellproc.Exited && result.Status.Success():
		outcome := transcript.Completed(text)
		outcome.Content = content
		return outcome
	case result.Outcome == shellproc.Cancelled:
		return transcript.Cancelled(text)
	}
	return transcript.Failed(text)
}

// shellFailure reports a call that never ran a command.
func shellFailure(message string) transcript.ToolOutcome {
	empty := shellproc.NewCapture(shellBodyLimit)
	text, _ := report(message, empty, empty, "")
	return transcript.Failed(text)
}

// processAction is one validated shell_process call.
type processAction struct {
	Action      string
	ProcessID   string
	WaitSeconds uint64
	Text        string
	CloseStdin  bool
}

func parseProcessAction(arguments string) (processAction, error) {
	var fields map[string]json.RawMessage
	if err := decodeArguments(arguments, &fields); err != nil {
		return processAction{}, err
	}
	var action processAction
	if err := json.Unmarshal(fields["action"], &action.Action); err != nil || fields["action"] == nil {
		return processAction{}, errors.New("arguments: missing field `action`")
	}
	allowed := map[string][]string{
		"list":  {},
		"read":  {"process_id", "wait_seconds"},
		"write": {"process_id", "text", "close_stdin"},
		"stop":  {"process_id"},
	}
	names, ok := allowed[action.Action]
	if !ok {
		return processAction{}, fmt.Errorf("arguments: unknown action %q, expected list, read, write, or stop", action.Action)
	}
	targets := map[string]any{"process_id": &action.ProcessID, "wait_seconds": &action.WaitSeconds, "text": &action.Text, "close_stdin": &action.CloseStdin}
	for name, raw := range fields {
		if name == "action" {
			continue
		}
		if !slices.Contains(names, name) {
			return processAction{}, fmt.Errorf("arguments: unknown field `%s` for action %s", name, action.Action)
		}
		if string(raw) == "null" || json.Unmarshal(raw, targets[name]) != nil {
			return processAction{}, fmt.Errorf("arguments: invalid %s", name)
		}
	}
	for _, required := range map[string][]string{"read": {"process_id"}, "write": {"process_id", "text"}, "stop": {"process_id"}}[action.Action] {
		if fields[required] == nil {
			return processAction{}, fmt.Errorf("arguments: missing field `%s`", required)
		}
	}
	switch {
	case action.Action == "read" && action.WaitSeconds > maxWaitSeconds:
		return processAction{}, fmt.Errorf("arguments: wait_seconds must be from 0 through %d", maxWaitSeconds)
	case action.Action == "write" && len(action.Text) > maxInputBytes:
		return processAction{}, errors.New("arguments: text exceeds 16 KiB")
	case action.Action == "write" && action.Text == "" && !action.CloseStdin:
		return processAction{}, errors.New("arguments: text is empty; set close_stdin to close stdin without writing")
	}
	return action, nil
}

// shellProcess runs one shell_process action. Reads and writes handle
// cancellation themselves so the command keeps running; a stop finishes its
// cleanup.
func (t *Toolbox) shellProcess(ctx context.Context, arguments string) transcript.ToolOutcome {
	action, err := parseProcessAction(arguments)
	if err != nil {
		return bounded("", nil, err)
	}
	if action.Action == "list" {
		return t.listProcesses()
	}
	process := t.Processes.Get(action.ProcessID)
	if process == nil {
		return bounded("", nil, fmt.Errorf("No shell process %s that you started. Call shell_process with action \"list\" to see the shell processes you started.", action.ProcessID))
	}
	switch action.Action {
	case "read":
		if !process.Wait(ctx, time.Duration(action.WaitSeconds)*time.Second) {
			return transcript.Cancelled(fmt.Sprintf("Cancelled while waiting for shell process %s; it was not stopped.", process.ID))
		}
		return renderProcess(process.ID, process.Command, process.Output(), false)
	case "write":
		return renderWritten(process.ID, len(action.Text), process.Write(ctx, []byte(action.Text), action.CloseStdin))
	}
	return renderProcess(process.ID, process.Command, process.Stop(), true)
}

func (t *Toolbox) listProcesses() transcript.ToolOutcome {
	processes := t.Processes.List()
	if len(processes) == 0 {
		return transcript.Completed("No shell processes that you started.")
	}
	var lines []string
	for _, process := range processes {
		state := process.State()
		description := "running"
		if !state.Running() {
			verb := "exited"
			if state.Stopped {
				verb = "stopped"
			}
			description = fmt.Sprintf("%s (%s)", verb, shellproc.ExitText(state.Status))
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s", process.ID, description, shorten(commandLine(process.Command))))
	}
	return bounded(strings.Join(lines, "\n"), nil, nil)
}

// renderProcess reports a read or stop. A read follows the shell conventions
// for the observed exit and shows its output blocks; a stop completes whatever
// the command's exit.
func renderProcess(id, command string, output shellproc.Output, stop bool) transcript.ToolOutcome {
	state, succeeded := "State: running", true
	if status := output.State.Status; status != nil {
		verb := "exited"
		if output.State.Stopped {
			verb = "stopped"
		}
		state = fmt.Sprintf("State: %s\n%s", verb, shellproc.ExitText(status))
		succeeded = status.Success()
	}
	header := fmt.Sprintf("Process ID: %s\nCommand: %s\n%s", id, shorten(commandLine(command)), state)
	text, content := report(header, output.Stdout, output.Stderr, output.Diagnostics)
	outcome := transcript.Completed(text)
	switch {
	case stop:
	case succeeded:
		outcome.Content = content
	default:
		outcome.Status = transcript.ToolFailed
	}
	return outcome
}

func renderWritten(id string, total int, written shellproc.Written) transcript.ToolOutcome {
	stdin := "stdin remains open"
	if written.StdinClosed {
		stdin = "stdin is closed"
	}
	var reason string
	switch written.Interruption {
	case shellproc.NotInterrupted:
		return transcript.Completed(fmt.Sprintf("Wrote %d bytes to shell process %s; %s.", written.Bytes, id, stdin))
	case shellproc.StdinClosed:
		reason = "stdin was already closed"
	case shellproc.Finished:
		reason = "the command has finished"
	case shellproc.WriteTimedOut:
		reason = "the command did not accept the input within 5 seconds"
	case shellproc.WriteCancelled:
		reason = "the prompt was cancelled"
	case shellproc.WriteFailed:
		reason = "writing failed: " + written.Error
	}
	text := fmt.Sprintf("Wrote %d of %d bytes to shell process %s, then stopped because %s; %s. The rest was not sent, and the command may not have processed what was.",
		written.Bytes, total, id, reason, stdin)
	if written.Interruption == shellproc.WriteCancelled {
		return transcript.Cancelled(text)
	}
	return transcript.Failed(text)
}

// excerpt fits a stream's text within budget bytes by cutting from the middle,
// and reports whether any output was omitted.
func excerpt(head, tail string, omitted bool, budget int) (string, bool) {
	if !omitted {
		if len(head) <= budget {
			return head, false
		}
		tail = head
	}
	head = truncate(head, max(budget/2, budget-len(tail)))
	tail = trimFront(tail, budget-len(head))
	return head + "\n[...]\n" + tail, true
}

// trimFront keeps at most limit bytes from the end of text, at a character
// boundary.
func trimFront(text string, limit int) string {
	start := max(len(text)-limit, 0)
	for start < len(text) && !isRuneStart(text[start]) {
		start++
	}
	return text[start:]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// report returns status and diagnostics followed by the output excerpts,
// within the tool output limit, and the client's blocks: the status, then each
// nonempty excerpt.
func report(status string, stdout, stderr *shellproc.Capture, diagnostics string) (string, []transcript.ToolContent) {
	if diagnostics != "" {
		status += "\n" + diagnostics
	}
	if len(status) > 1536 {
		status = truncate(status, 1500) + "\nDiagnostic truncated."
	}
	outHead, outTail := stdout.Text()
	errHead, errTail := stderr.Text()
	half := shellBodyLimit / 2
	outBudget := max(half, shellBodyLimit-len(errHead)-len(errTail))
	errBudget := shellBodyLimit - min(len(outHead)+len(outTail), outBudget)
	outText, outOmitted := excerpt(outHead, outTail, stdout.Omitted, outBudget)
	errText, errOmitted := excerpt(errHead, errTail, stderr.Omitted, errBudget)
	content := []transcript.ToolContent{{Text: status}}
	for _, stream := range []struct {
		name    string
		text    string
		omitted bool
		total   uint64
	}{{"stdout", outText, outOmitted, stdout.Total}, {"stderr", errText, errOmitted, stderr.Total}} {
		status += "\n\n" + stream.name + ":"
		if stream.omitted {
			status += fmt.Sprintf(" (start and end of %d bytes; the middle is omitted)", stream.total)
		}
		if stream.text == "" {
			status += "\n(empty)"
		} else {
			status += "\n" + stream.text
			content = append(content, transcript.ToolContent{Text: stream.text})
		}
	}
	if len(status) > outputLimit {
		panic("shell output exceeds its limit")
	}
	return status, content
}
