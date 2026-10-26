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

// hookInput is what every hook reads on stdin. Its names follow Claude Code's
// hook input, so one hook script works for both.
type hookInput struct {
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
}

// sessionStartInput says whether the session is new or has earlier turns.
type sessionStartInput struct {
	hookInput
	Source string `json:"source"`
}

type userPromptSubmitInput struct {
	hookInput
	Prompt string `json:"prompt"`
}

type postToolUseInput struct {
	hookInput
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolUseID    string          `json:"tool_use_id"`
	ToolResponse toolResponse    `json:"tool_response"`
}

// stopInput says whether a Stop hook already continued the turn.
type stopInput struct {
	hookInput
	StopHookActive bool `json:"stop_hook_active"`
}

// toolResponse is the outcome of the call as the model reads it.
type toolResponse struct {
	Status transcript.ToolStatus `json:"status"`
	Text   string                `json:"text"`
}

// hookOutput is what a hook prints: context for the model, or for Stop, a
// decision to block the stop and the reason the model reads.
type hookOutput struct {
	Decision           string `json:"decision"`
	Reason             string `json:"reason"`
	HookSpecificOutput struct {
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func (t *Turn) hookInput(event string) hookInput {
	return hookInput{SessionID: t.summary.ID, Cwd: t.summary.Workspace, HookEventName: event}
}

// promptHooks runs the SessionStart hooks when first is set, then the
// UserPromptSubmit hooks, and returns the context they add.
func (t *Turn) promptHooks(ctx context.Context, first bool, transcriptLength int, input transcript.TurnInput) []string {
	var context []string
	if first {
		source := "startup"
		if transcriptLength > 0 {
			source = "resume"
		}
		for _, output := range t.runHooks(ctx, "SessionStart", t.agent.Hooks.SessionStart, sessionStartInput{t.hookInput("SessionStart"), source}) {
			context = appendText(context, output.HookSpecificOutput.AdditionalContext)
		}
	}
	prompt := userPromptSubmitInput{t.hookInput("UserPromptSubmit"), input.ModelMessage().Text()}
	for _, output := range t.runHooks(ctx, "UserPromptSubmit", t.agent.Hooks.UserPromptSubmit, prompt) {
		context = appendText(context, output.HookSpecificOutput.AdditionalContext)
	}
	return hookContext(context)
}

// postToolUse runs the PostToolUse hooks that match call, in order, and
// returns the context they add.
func (t *Turn) postToolUse(ctx context.Context, call transcript.ToolCall, outcome transcript.ToolOutcome) []string {
	var matching []settings.Hook
	for _, hook := range t.agent.Hooks.PostToolUse {
		if hook.Matches(call.Name) {
			matching = append(matching, hook)
		}
	}
	arguments := json.RawMessage(call.Arguments)
	if !json.Valid(arguments) {
		arguments, _ = json.Marshal(call.Arguments)
	}
	var context []string
	for _, output := range t.runHooks(ctx, "PostToolUse", matching, postToolUseInput{
		hookInput: t.hookInput("PostToolUse"), ToolName: call.Name, ToolInput: arguments, ToolUseID: call.CallID,
		ToolResponse: toolResponse{Status: outcome.Status, Text: outcome.Text},
	}) {
		context = appendText(context, output.HookSpecificOutput.AdditionalContext)
	}
	return hookContext(context)
}

// stop runs the Stop hooks and returns the reasons of those that block the
// stop. The turn continues when there are any.
func (t *Turn) stop(ctx context.Context, active bool) []string {
	var reasons []string
	for _, output := range t.runHooks(ctx, "Stop", t.agent.Hooks.Stop, stopInput{t.hookInput("Stop"), active}) {
		if output.Decision == "block" {
			reasons = appendText(reasons, output.Reason)
		}
	}
	return hookContext(reasons)
}

// hookContext labels hook output before it joins context from other sources.
// Models read it as a user message because they distrust instructions in tool
// output, and hooks come from the user's own configuration.
func hookContext(texts []string) []string {
	if len(texts) == 0 {
		return nil
	}
	return []string{"Context added by the user's hooks:\n\n" + strings.Join(texts, "\n\n")}
}

func appendText(texts []string, text string) []string {
	if text == "" {
		return texts
	}
	return append(texts, text)
}

// runHooks runs hooks in order with input on stdin and returns their outputs.
// A failed hook is reported on stderr and otherwise ignored, so a hook never
// ends a turn.
func (t *Turn) runHooks(ctx context.Context, event string, hooks []settings.Hook, input any) []hookOutput {
	var data []byte
	var outputs []hookOutput
	for _, hook := range hooks {
		if ctx.Err() != nil {
			break
		}
		if data == nil {
			data, _ = json.Marshal(input)
		}
		output, problem := runHook(ctx, hook, t.summary.Workspace, data)
		if problem != "" {
			fmt.Fprintf(os.Stderr, "ox: %s hook %q: %s\n", event, hook.Command, control.Escape(problem))
		}
		if output != nil {
			outputs = append(outputs, *output)
		}
	}
	return outputs
}

// runHook runs one command hook with input on stdin and returns what it
// printed, if anything, and a description of what went wrong or what it wrote
// to stderr.
func runHook(ctx context.Context, hook settings.Hook, workspace string, input []byte) (output *hookOutput, problem string) {
	cmd := tools.ShellCommand(workspace, hook.Command)
	cmd.Stdin = bytes.NewReader(input)
	// A descendant that escapes the process group can hold stdin open.
	cmd.WaitDelay = time.Second
	result, err := shellproc.Run(ctx, cmd, hookOutputLimit, hook.Timeout)
	if err != nil {
		return nil, fmt.Sprintf("could not start %s: %v", tools.ShellProgram, err)
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
		output = &hookOutput{}
		if err := json.Unmarshal([]byte(stdout), output); err != nil {
			output, failure = nil, "output is not a JSON object: "+err.Error()
		}
	}
	switch {
	case failure != "" && stderr != "":
		return nil, failure + "; stderr: " + stderr
	case failure != "":
		return nil, failure
	case stderr != "":
		return output, "stderr: " + stderr
	}
	return output, ""
}
