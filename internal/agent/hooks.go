package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"ox/internal/control"
	"ox/internal/settings"
	"ox/internal/shellproc"
	"ox/internal/tools"
	"ox/internal/transcript"
)

// hookOutputLimit bounds each captured stream of a hook. Larger output is
// truncated, so a larger JSON reply fails to parse and is ignored.
const hookOutputLimit = 1 << 20

// postToolUseInput is what a PostToolUse hook reads on stdin. Its names
// follow Claude Code's hook input, so one hook script works for both.
type postToolUseInput struct {
	SessionID     string          `json:"session_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolUseID     string          `json:"tool_use_id"`
	ToolResponse  toolResponse    `json:"tool_response"`
}

// toolResponse is the outcome of the call as the model reads it.
type toolResponse struct {
	Status transcript.ToolStatus `json:"status"`
	Text   string                `json:"text"`
}

// postToolUse runs the PostToolUse hooks that match call, in order, and
// returns the context they add. A failed hook is reported on stderr and
// otherwise ignored, so a hook never ends a turn.
func (t *Turn) postToolUse(ctx context.Context, call transcript.ToolCall, outcome transcript.ToolOutcome) []string {
	var input []byte
	var context []string
	for _, hook := range t.agent.Hooks.PostToolUse {
		if !hook.Matches(call.Name) || ctx.Err() != nil {
			continue
		}
		if input == nil {
			arguments := json.RawMessage(call.Arguments)
			if !json.Valid(arguments) {
				arguments, _ = json.Marshal(call.Arguments)
			}
			input, _ = json.Marshal(postToolUseInput{
				SessionID: t.summary.ID, Cwd: t.summary.Workspace, HookEventName: "PostToolUse",
				ToolName: call.Name, ToolInput: arguments, ToolUseID: call.CallID,
				ToolResponse: toolResponse{Status: outcome.Status, Text: outcome.Text},
			})
		}
		added, problem := runHook(ctx, hook, t.summary.Workspace, input)
		if problem != "" {
			fmt.Fprintf(os.Stderr, "ox: PostToolUse hook %q: %s\n", hook.Command, control.Escape(problem))
		}
		if added != "" {
			context = append(context, added)
		}
	}
	return context
}

// runHook runs one command hook with input on stdin and returns the
// additionalContext it printed, and a description of what went wrong or what
// it wrote to stderr.
func runHook(ctx context.Context, hook settings.Hook, workspace string, input []byte) (added, problem string) {
	cmd := tools.ShellCommand(workspace, hook.Command)
	cmd.Stdin = bytes.NewReader(input)
	// A descendant that escapes the process group can hold stdin open.
	cmd.WaitDelay = time.Second
	result, err := shellproc.Run(ctx, cmd, hookOutputLimit, hook.Timeout)
	if err != nil {
		return "", fmt.Sprintf("could not start %s: %v", tools.ShellProgram, err)
	}
	stdout, _ := result.Stdout.Text()
	stderr, _ := result.Stderr.Text()
	stderr = strings.TrimSpace(stderr)
	failure := ""
	switch {
	case result.Outcome == shellproc.TimedOut:
		failure = fmt.Sprintf("timed out after %s", hook.Timeout)
	case result.Outcome == shellproc.Cancelled:
		failure = "cancelled"
	case !result.Status.Success():
		failure = shellproc.ExitText(result.Status)
	case strings.TrimSpace(stdout) != "":
		var output struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(stdout), &output); err != nil {
			failure = "output is not a JSON object with hookSpecificOutput: " + err.Error()
		} else {
			added = output.HookSpecificOutput.AdditionalContext
		}
	}
	switch {
	case failure != "" && stderr != "":
		return "", failure + "; stderr: " + stderr
	case failure != "":
		return "", failure
	case stderr != "":
		return added, "stderr: " + stderr
	}
	return added, ""
}
