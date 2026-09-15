package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kkestell/ox/internal/agent"
)

// A tool presentation gives clients an action and its display argument
// separately. Arguments are decoded for display only: malformed or incomplete
// input leaves the argument unset.

const titleLimit = 80

func presentation(name string, subject *string) agent.ToolPresentation {
	result := agent.ToolPresentation{Name: name}
	if subject != nil {
		result.Arguments = titleText(*subject)
	}
	return result
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

// subagentPresentation identifies a child by the short form of its ID, which is
// enough to tell concurrent children apart in a client transcript.
func subagentPresentation(name string, id *string) agent.ToolPresentation {
	if id == nil {
		return presentation(name, nil)
	}
	value := strings.TrimSpace(*id)
	if len(value) > 8 {
		value = value[:8]
	}
	return presentation(name, &value)
}

func shellPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input shellArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Run", input.Command)
}

func readPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input readArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Read", input.Path)
}

func writePresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input writeArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Write", input.Path)
}

func editPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input editArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Edit", input.Path)
}

func globPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input globArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Find", input.Pattern)
}

func grepPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input grepArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Search for", input.Pattern)
}

func skillPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input skillArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Load skill", input.Name)
}

func todoPresentation(json.RawMessage) agent.ToolPresentation {
	return agent.ToolPresentation{Name: "Update the todo list"}
}

func questionPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input questionArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Ask", input.Question)
}

func webFetchPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input webFetchArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Fetch", input.URL)
}

func memorySearchPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input memorySearchArgs
	_ = json.Unmarshal(arguments, &input)
	return presentation("Search memory for", input.Query)
}

func memoryWritePresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input memoryWriteArgs
	_ = json.Unmarshal(arguments, &input)
	return presentation("Remember", input.Content)
}

func memoryDeletePresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input memoryDeleteArgs
	_ = json.Unmarshal(arguments, &input)
	return presentation("Delete workspace memory", input.ID)
}

func subagentStartPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input subagentStartArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Start subagent", input.Name)
}

func subagentSendPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input subagentMessageArguments
	_ = json.Unmarshal(arguments, &input)
	return subagentPresentation("Message subagent", input.ID)
}

func subagentStopPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input subagentIDArguments
	_ = json.Unmarshal(arguments, &input)
	return subagentPresentation("Stop subagent", input.ID)
}

func subagentListPresentation(json.RawMessage) agent.ToolPresentation {
	return agent.ToolPresentation{Name: "List subagents"}
}

func subagentWaitPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input subagentWaitArguments
	_ = json.Unmarshal(arguments, &input)
	switch len(input.IDs) {
	case 0:
		return agent.ToolPresentation{Name: "Wait for any subagent"}
	case 1:
		return subagentPresentation("Wait for subagent", &input.IDs[0])
	default:
		return agent.ToolPresentation{Name: fmt.Sprintf("Wait for %d subagents", len(input.IDs))}
	}
}

func subagentReportPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input subagentReportArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Report", input.Message)
}

// A language-query presentation names the position it asks about, because the
// file alone does not say which symbol the model is following.
func lspPositionPresentation(name string, arguments json.RawMessage) agent.ToolPresentation {
	var input lspPositionArguments
	_ = json.Unmarshal(arguments, &input)
	if input.Path == nil || input.Line == nil || input.Column == nil {
		return presentation(name, nil)
	}
	where := fmt.Sprintf("%s:%d:%d", *input.Path, *input.Line, *input.Column)
	return presentation(name, &where)
}

func lspDefinitionPresentation(arguments json.RawMessage) agent.ToolPresentation {
	return lspPositionPresentation("Find the definition of", arguments)
}

func lspReferencesPresentation(arguments json.RawMessage) agent.ToolPresentation {
	return lspPositionPresentation("Find references to", arguments)
}

func lspDocumentSymbolsPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input lspPathArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Outline", input.Path)
}

func lspWorkspaceSymbolsPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input lspQueryArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Find symbol", input.Query)
}

func lspDiagnosticsPresentation(arguments json.RawMessage) agent.ToolPresentation {
	var input lspPathArguments
	_ = json.Unmarshal(arguments, &input)
	return presentation("Check", input.Path)
}
