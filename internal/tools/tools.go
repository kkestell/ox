// Package tools is the model's tool set: the schemas sent to the model, tool
// call titles, Ask mode permission, and execution of one complete call.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"ox/internal/mcpclient"
	"ox/internal/settings"
	"ox/internal/shellproc"
	"ox/internal/transcript"
)

const (
	Shell        = "shell"
	ShellProcess = "shell_process"
	WebFetch     = "web_fetch"
)

// outputLimit bounds every result the model reads.
const outputLimit = 16 * 1024

// bodyLimit leaves room for truncation notices.
const bodyLimit = outputLimit - 256

// maxTitleChars is long enough to name a path or pattern, short enough for one
// display line.
const maxTitleChars = 80

// Toolbox runs calls for one session.
type Toolbox struct {
	Workspace string
	MCP       *mcpclient.Catalog
	Team      Team
	// Processes are the session's background processes, which outlive calls.
	Processes *shellproc.Processes
}

// Schemas returns every tool definition sent to the model.
func Schemas() []json.RawMessage {
	return []json.RawMessage{shellSchema, shellProcessSchema, webFetchSchema}
}

// Schemas combines built-ins with the immutable catalog for this turn.
func (t *Toolbox) Schemas() []json.RawMessage {
	schemas := append(Schemas(), t.teamSchemas()...)
	names := map[string]bool{}
	for _, raw := range schemas {
		var definition struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if err := json.Unmarshal(raw, &definition); err != nil {
			panic(err)
		}
		names[definition.Function.Name] = true
	}
	for _, tool := range t.MCP.Tools() {
		if names[tool.Name] {
			panic("MCP tool collides with built-in: " + tool.Name)
		}
		raw, err := json.Marshal(map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema}})
		if err != nil {
			panic(err)
		}
		schemas = append(schemas, raw)
	}
	return schemas
}

// Execute runs one call. Unknown names and invalid arguments are failed
// results the model reads on its next request, not errors that end the turn.
// Shell tools handle cancellation themselves, so they can report a partial
// write or finish a stop's cleanup.
func (t *Toolbox) Execute(ctx context.Context, call transcript.ToolCall) transcript.ToolOutcome {
	if t.hasTeamTool(call.Name) {
		return t.team(ctx, call)
	}
	switch call.Name {
	case Shell:
		return t.shell(ctx, call.Arguments)
	case ShellProcess:
		return t.shellProcess(ctx, call.Arguments)
	case WebFetch:
		// Wait for cancellation to finish before returning: large pages may
		// be written to a file for later reading.
		text, content, err := webFetch(ctx, t.Workspace, call.Arguments)
		if errors.Is(err, context.Canceled) {
			return transcript.Cancelled("Cancelled while fetching the page.")
		}
		return bounded(text, content, err)
	default:
		if _, ok := t.MCP.Lookup(call.Name); ok {
			return t.mcp(ctx, call)
		}
		return transcript.Failed("Unknown tool: " + call.Name)
	}
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
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("arguments: trailing characters after the JSON object")
	}
	return nil
}

// Permission is what Ask mode must request before a call runs.
type Permission struct {
	// Kind is PermissionNone when the call runs without asking.
	Kind         PermissionKind
	Server, Tool string
	// For PermissionInput: the process ID, its command (empty when the ID
	// names no process), and the text and stdin closure being sent.
	ProcessID  string
	Command    string
	Text       string
	CloseStdin bool
	Agent      CreateAgentArgs
	Profile    settings.AgentProfile
}

type PermissionKind int

const (
	PermissionNone PermissionKind = iota
	// PermissionCommand is running a shell command.
	PermissionCommand
	// PermissionInput is sending text to a background process's stdin.
	PermissionInput
	PermissionMCP
	PermissionAgent
)

// Permission classifies a call by the same argument validation its execution
// uses.
func (t *Toolbox) Permission(call transcript.ToolCall) Permission {
	if call.Name == CreateAgent && t.hasTeamTool(call.Name) {
		if args, profile, err := t.parseCreateAgent(call.Arguments); err == nil {
			return Permission{Kind: PermissionAgent, Agent: args, Profile: profile}
		}
		return Permission{}
	}
	if tool, ok := t.MCP.Lookup(call.Name); ok {
		if tool.ReadOnly {
			return Permission{}
		}
		return Permission{Kind: PermissionMCP, Server: tool.Server, Tool: tool.OriginalName}
	}
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
	case CreateAgent:
		return "Create agent"
	case SendMessage:
		return "Send message"
	case ListAgents:
		return "List agents"
	case RemoveAgent:
		return "Remove agent"
	case Wait:
		return "Wait for agents"
	case WebFetch:
		return "Fetch web page"
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
	case CreateAgent:
		if name := argument("name"); name != "" {
			return "Create agent " + name
		}
	case SendMessage:
		if to := argument("to"); to != "" {
			return "Message " + to
		}
	case RemoveAgent:
		if name := argument("name"); name != "" {
			return "Remove agent " + name
		}
	case WebFetch:
		if url := argument("url"); url != "" {
			return "Fetch " + url
		}

	}
	return ""
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
