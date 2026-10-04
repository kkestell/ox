// Package tools is the model's tool set: the schemas sent to the model, tool
// call titles, Ask mode permission, and execution of one complete call.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ox/internal/shellproc"
	"ox/internal/transcript"
)

const (
	ApplyPatch   = "apply_patch"
	ReadFile     = "read_file"
	Glob         = "glob"
	Grep         = "grep"
	Shell        = "shell"
	ShellProcess = "shell_process"
)

// outputLimit bounds every result the model reads.
const outputLimit = 16 * 1024

// bodyLimit leaves room for line numbers, continuation instructions, and
// truncation notices.
const bodyLimit = outputLimit - 256

// maxTitleChars is long enough to name a path or pattern, short enough for one
// display line.
const maxTitleChars = 80

// Toolbox runs calls for one session.
type Toolbox struct {
	Workspace string
	// Processes are the session's background processes, which outlive calls.
	Processes *shellproc.Processes
}

// Schemas returns every tool definition sent to the model.
func Schemas() []json.RawMessage {
	return []json.RawMessage{shellSchema, shellProcessSchema, readSchema, globSchema, grepSchema, patchSchema()}
}

// Execute runs one call. Unknown names and invalid arguments are failed
// results the model reads on its next request, not errors that end the turn.
// Shell tools handle cancellation themselves, so they can report a partial
// write or finish a stop's cleanup.
func (t *Toolbox) Execute(ctx context.Context, call transcript.ToolCall) transcript.ToolOutcome {
	switch call.Name {
	case Shell:
		return t.shell(ctx, call.Arguments)
	case ShellProcess:
		return t.shellProcess(ctx, call.Arguments)
	case ReadFile, Glob, Grep, ApplyPatch:
	default:
		return transcript.Failed("Unknown tool: " + call.Name)
	}
	result := make(chan transcript.ToolOutcome, 1)
	go func() { result <- bounded(t.run(ctx, call)) }()
	select {
	case outcome := <-result:
		return outcome
	case <-ctx.Done():
		return transcript.Cancelled("Cancelled while this tool was running; no result was observed.")
	}
}

func (t *Toolbox) run(ctx context.Context, call transcript.ToolCall) (string, []transcript.ToolContent, error) {
	switch call.Name {
	case ReadFile:
		return read(t.Workspace, call.Arguments)
	case ApplyPatch:
		var args struct {
			Patch *string `json:"patch"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return "", nil, err
		}
		if args.Patch == nil {
			return "", nil, errors.New("arguments: missing field `patch`")
		}
		return applyPatch(t.Workspace, *args.Patch)
	}
	return search(ctx, t.Workspace, call.Name, call.Arguments)
}

// bounded is the outcome of a tool that returns the model's text and the
// client's content, or an error.
func bounded(text string, content []transcript.ToolContent, err error) transcript.ToolOutcome {
	if err != nil {
		message := err.Error()
		if len(message) > outputLimit {
			message = truncate(message, bodyLimit) + "\nError truncated."
		}
		return transcript.Failed(message)
	}
	if len(text) > outputLimit {
		panic("tool output exceeds its limit")
	}
	outcome := transcript.Completed(text)
	outcome.Content = content
	return outcome
}

// truncate cuts text to at most limit bytes at a character boundary.
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

// decodeArguments decodes a call's JSON object, rejecting unknown fields.
func decodeArguments(arguments string, value any) error {
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("arguments: %w", err)
	}
	if decoder.More() {
		return errors.New("arguments: trailing characters after the JSON object")
	}
	return nil
}

// Permission is what Ask mode must request before a call runs.
type Permission struct {
	// Kind is PermissionNone when the call runs without asking.
	Kind PermissionKind
	// For PermissionInput: the process ID, its command (empty when the ID
	// names no process), and the text and stdin closure being sent.
	ProcessID  string
	Command    string
	Text       string
	CloseStdin bool
}

type PermissionKind int

const (
	PermissionNone PermissionKind = iota
	// PermissionCommand is running a shell command.
	PermissionCommand
	// PermissionInput is sending text to a background process's stdin.
	PermissionInput
)

// Permission classifies a call by the same argument validation its execution
// uses.
func (t *Toolbox) Permission(call transcript.ToolCall) Permission {
	switch call.Name {
	case Shell:
		// Every shell call needs permission, even one whose arguments are
		// invalid.
		return Permission{Kind: PermissionCommand}
	case ShellProcess:
		action, err := parseProcessAction(call.Arguments)
		if err != nil || action.Action != "write" {
			return Permission{}
		}
		permission := Permission{Kind: PermissionInput, ProcessID: action.ProcessID, Text: action.Text, CloseStdin: action.CloseStdin}
		if process := t.Processes.Get(action.ProcessID); process != nil {
			permission.Command = process.Command
		}
		return permission
	}
	return Permission{}
}

// Title is what the ACP client shows for a call. Arguments come from the model
// and may be missing or malformed, so a call that cannot be described by its
// arguments is named by its tool alone.
func Title(call transcript.ToolCall) string {
	if description := describe(call); description != "" {
		return shorten(description)
	}
	switch call.Name {
	case Shell:
		return "Run shell command"
	case ShellProcess:
		return "Use shell process"
	case ReadFile:
		return "Read file"
	case Glob:
		return "Find files"
	case Grep:
		return "Search file contents"
	case ApplyPatch:
		return "Apply patch"
	}
	return call.Name
}

// describe says what a call does in the terms its arguments give, or nothing
// when they do not parse or omit what names the work.
func describe(call transcript.ToolCall) string {
	var arguments map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
		return ""
	}
	argument := func(name string) string {
		value, _ := arguments[name].(string)
		return value
	}
	switch call.Name {
	case Shell:
		command := commandLine(argument("command"))
		if command != "" && arguments["background"] == true {
			return "Background: " + command
		}
		return command
	case ShellProcess:
		process := argument("process_id")
		if argument("action") == "list" {
			return "List shell processes"
		}
		if process == "" {
			return ""
		}
		switch argument("action") {
		case "read":
			return "Read shell process " + process
		case "write":
			return "Write to shell process " + process
		case "stop":
			return "Stop shell process " + process
		}
	case ReadFile:
		if path := argument("path"); path != "" {
			return "Read " + path
		}
	case Glob:
		pattern := argument("pattern")
		if pattern == "" {
			return ""
		}
		if path := searchedPath(argument("path")); path != "" {
			return fmt.Sprintf("Find files matching %s in %s", pattern, path)
		}
		return "Find files matching " + pattern
	case Grep:
		pattern := argument("pattern")
		if pattern == "" {
			return ""
		}
		description := "Search for " + pattern
		if path := searchedPath(argument("path")); path != "" {
			description += " in " + path
		}
		if glob := argument("glob"); glob != "" {
			description += fmt.Sprintf(" (files matching %s)", glob)
		}
		return description
	case ApplyPatch:
		switch paths := changedPaths(argument("patch")); len(paths) {
		case 0:
		case 1:
			return "Apply patch to " + paths[0]
		default:
			return fmt.Sprintf("Apply patch to %d files", len(paths))
		}
	}
	return ""
}

// searchedPath is the part of the workspace a search covers, or empty when it
// covers the whole workspace and so says nothing useful.
func searchedPath(path string) string {
	switch path {
	case ".", "./":
		return ""
	}
	return path
}

// commandLine is a command's first nonblank line, marked when more follows.
func commandLine(command string) string {
	var lines []string
	for line := range strings.Lines(command) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	switch len(lines) {
	case 0:
		return ""
	case 1:
		return lines[0]
	}
	return lines[0] + " …"
}

// shorten limits a title to maxTitleChars characters, counting the ellipsis
// that marks a shortened one.
func shorten(title string) string {
	runes := []rune(title)
	if len(runes) <= maxTitleChars {
		return title
	}
	return strings.TrimRight(string(runes[:maxTitleChars-1]), " \t") + "…"
}

// compact re-encodes a schema literal as compact JSON.
func compact(schema string) json.RawMessage {
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, []byte(schema)); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}
