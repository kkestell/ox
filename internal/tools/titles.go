package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A title is what an ACP client shows for a tool call, so it names the work
// rather than the tool. Arguments are decoded for display only: a malformed or
// incomplete value leaves the field unset and the call keeps a static label.

const titleLimit = 80

func title(verb string, subject *string, fallback string) string {
	if subject == nil {
		return fallback
	}
	text := titleText(*subject)
	if text == "" {
		return fallback
	}
	return verb + " " + text
}

// titleText reduces an argument to a single short line.
func titleText(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		value = strings.TrimSpace(value[:index]) + " …"
	}
	runes := []rune(value)
	if len(runes) > titleLimit {
		return strings.TrimRight(string(runes[:titleLimit]), " ") + "…"
	}
	return value
}

// subagentTitle identifies a child by the short form of its ID, which is enough
// to tell concurrent children apart in a client transcript.
func subagentTitle(verb string, id *string, fallback string) string {
	if id == nil {
		return fallback
	}
	value := strings.TrimSpace(*id)
	if len(value) > 8 {
		value = value[:8]
	}
	return title(verb, &value, fallback)
}

func shellTitle(arguments json.RawMessage) string {
	var input shellArguments
	_ = json.Unmarshal(arguments, &input)
	if input.Command == nil {
		return "Run a shell command"
	}
	if command := titleText(*input.Command); command != "" {
		return command
	}
	return "Run a shell command"
}

func readTitle(arguments json.RawMessage) string {
	var input readArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Read", input.Path, "Read a file")
}

func writeTitle(arguments json.RawMessage) string {
	var input writeArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Write", input.Path, "Write a file")
}

func editTitle(arguments json.RawMessage) string {
	var input editArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Edit", input.Path, "Edit a file")
}

func globTitle(arguments json.RawMessage) string {
	var input globArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Find", input.Pattern, "Find files")
}

func grepTitle(arguments json.RawMessage) string {
	var input grepArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Search for", input.Pattern, "Search files")
}

func skillTitle(arguments json.RawMessage) string {
	var input skillArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Load skill", input.Name, "Load a skill")
}

func todoTitle(json.RawMessage) string {
	return "Update the todo list"
}

func questionTitle(arguments json.RawMessage) string {
	var input questionArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Ask:", input.Question, "Ask the user a question")
}

func webFetchTitle(arguments json.RawMessage) string {
	var input webFetchArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Fetch", input.URL, "Fetch web page")
}

func memorySearchTitle(arguments json.RawMessage) string {
	var input memorySearchArgs
	_ = json.Unmarshal(arguments, &input)
	return title("Search memory for", input.Query, "List recent memory")
}

func memoryWriteTitle(arguments json.RawMessage) string {
	var input memoryWriteArgs
	_ = json.Unmarshal(arguments, &input)
	return title("Remember:", input.Content, "Write workspace memory")
}

func memoryDeleteTitle(arguments json.RawMessage) string {
	var input memoryDeleteArgs
	_ = json.Unmarshal(arguments, &input)
	return title("Delete workspace memory", input.ID, "Delete workspace memory")
}

func subagentStartTitle(arguments json.RawMessage) string {
	var input subagentStartArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Start subagent", input.Name, "Start a subagent")
}

func subagentSendTitle(arguments json.RawMessage) string {
	var input subagentMessageArguments
	_ = json.Unmarshal(arguments, &input)
	return subagentTitle("Message subagent", input.ID, "Message a subagent")
}

func subagentStopTitle(arguments json.RawMessage) string {
	var input subagentIDArguments
	_ = json.Unmarshal(arguments, &input)
	return subagentTitle("Stop subagent", input.ID, "Stop a subagent")
}

func subagentListTitle(json.RawMessage) string {
	return "List subagents"
}

func subagentWaitTitle(arguments json.RawMessage) string {
	var input subagentWaitArguments
	_ = json.Unmarshal(arguments, &input)
	switch len(input.IDs) {
	case 0:
		return "Wait for any subagent"
	case 1:
		return subagentTitle("Wait for subagent", &input.IDs[0], "Wait for any subagent")
	default:
		return fmt.Sprintf("Wait for %d subagents", len(input.IDs))
	}
}

func subagentReportTitle(arguments json.RawMessage) string {
	var input subagentReportArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Report:", input.Message, "Report to the primary agent")
}

// A language-query title names the position it asks about, because the file
// alone does not say which symbol the model is following.
func lspPositionTitle(verb string, arguments json.RawMessage, fallback string) string {
	var input lspPositionArguments
	_ = json.Unmarshal(arguments, &input)
	if input.Path == nil || input.Line == nil || input.Column == nil {
		return fallback
	}
	where := fmt.Sprintf("%s:%d:%d", *input.Path, *input.Line, *input.Column)
	return title(verb, &where, fallback)
}

func lspDefinitionTitle(arguments json.RawMessage) string {
	return lspPositionTitle("Find the definition of", arguments, "Find a definition")
}

func lspReferencesTitle(arguments json.RawMessage) string {
	return lspPositionTitle("Find references to", arguments, "Find references")
}

func lspDocumentSymbolsTitle(arguments json.RawMessage) string {
	var input lspPathArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Outline", input.Path, "Outline a file")
}

func lspWorkspaceSymbolsTitle(arguments json.RawMessage) string {
	var input lspQueryArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Find symbol", input.Query, "Find workspace symbols")
}

func lspDiagnosticsTitle(arguments json.RawMessage) string {
	var input lspPathArguments
	_ = json.Unmarshal(arguments, &input)
	return title("Check", input.Path, "Check diagnostics")
}
