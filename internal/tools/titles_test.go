package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/agent"
)

func TestBuiltInToolPresentationsSeparateNamesAndArguments(t *testing.T) {
	cases := map[string]struct {
		arguments string
		want      agent.ToolPresentation
	}{
		"edit_file":             {`{"path":"main.go"}`, agent.ToolPresentation{Name: "Edit", Arguments: "main.go"}},
		"glob":                  {`{"pattern":"**/*.go"}`, agent.ToolPresentation{Name: "Find", Arguments: "**/*.go"}},
		"grep":                  {`{"pattern":"ToolCall"}`, agent.ToolPresentation{Name: "Search for", Arguments: "ToolCall"}},
		"lsp_definition":        {`{"path":"main.go","line":4,"column":8}`, agent.ToolPresentation{Name: "Find the definition of", Arguments: "main.go:4:8"}},
		"lsp_diagnostics":       {`{"path":"main.go"}`, agent.ToolPresentation{Name: "Check", Arguments: "main.go"}},
		"lsp_document_symbols":  {`{"path":"main.go"}`, agent.ToolPresentation{Name: "Outline", Arguments: "main.go"}},
		"lsp_references":        {`{"path":"main.go","line":4,"column":8}`, agent.ToolPresentation{Name: "Find references to", Arguments: "main.go:4:8"}},
		"lsp_workspace_symbols": {`{"query":"ToolCall"}`, agent.ToolPresentation{Name: "Find symbol", Arguments: "ToolCall"}},
		"memory_delete":         {`{"id":"fact-1"}`, agent.ToolPresentation{Name: "Delete workspace memory", Arguments: "fact-1"}},
		"memory_search":         {`{"query":"tool titles"}`, agent.ToolPresentation{Name: "Search memory for", Arguments: "tool titles"}},
		"memory_write":          {`{"content":"Titles belong to Ox"}`, agent.ToolPresentation{Name: "Remember", Arguments: "Titles belong to Ox"}},
		"question":              {`{"question":"Continue?"}`, agent.ToolPresentation{Name: "Ask", Arguments: "Continue?"}},
		"read_file":             {`{"path":"main.go"}`, agent.ToolPresentation{Name: "Read", Arguments: "main.go"}},
		"shell":                 {`{"command":"go test ./..."}`, agent.ToolPresentation{Name: "Run", Arguments: "go test ./..."}},
		"skill":                 {`{"name":"review"}`, agent.ToolPresentation{Name: "Load skill", Arguments: "review"}},
		"subagent_list":         {`{}`, agent.ToolPresentation{Name: "List subagents"}},
		"subagent_report":       {`{"message":"Tests passed"}`, agent.ToolPresentation{Name: "Report", Arguments: "Tests passed"}},
		"subagent_send":         {`{"id":"abcdefgh1234"}`, agent.ToolPresentation{Name: "Message subagent", Arguments: "abcdefgh"}},
		"subagent_start":        {`{"name":"tests"}`, agent.ToolPresentation{Name: "Start subagent", Arguments: "tests"}},
		"subagent_stop":         {`{"id":"abcdefgh1234"}`, agent.ToolPresentation{Name: "Stop subagent", Arguments: "abcdefgh"}},
		"subagent_wait":         {`{"ids":["one","two"]}`, agent.ToolPresentation{Name: "Wait for 2 subagents"}},
		"todo":                  {`{}`, agent.ToolPresentation{Name: "Update the todo list"}},
		"web_fetch":             {`{"url":"https://example.test"}`, agent.ToolPresentation{Name: "Fetch", Arguments: "https://example.test"}},
		"write_file":            {`{"path":"main.go"}`, agent.ToolPresentation{Name: "Write", Arguments: "main.go"}},
	}

	tools := All()
	if len(cases) != len(tools) {
		t.Fatalf("title cases = %d, tools = %d", len(cases), len(tools))
	}
	for _, tool := range tools {
		t.Run(tool.Name, func(t *testing.T) {
			if tool.Presentation == nil {
				t.Fatal("presentation function is nil")
			}
			test, ok := cases[tool.Name]
			if !ok {
				t.Fatal("title case is missing")
			}
			if got := tool.Presentation(json.RawMessage(test.arguments)); got != test.want {
				t.Fatalf("presentation = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestBuiltInToolPresentationsHandleUnusableSubjects(t *testing.T) {
	for _, test := range []struct {
		name         string
		presentation func(json.RawMessage) agent.ToolPresentation
		arguments    string
		want         agent.ToolPresentation
	}{
		{"malformed shell input", shellPresentation, `{`, agent.ToolPresentation{Name: "Run"}},
		{"empty shell command", shellPresentation, `{"command":"  "}`, agent.ToolPresentation{Name: "Run"}},
		{"multiline shell command", shellPresentation, `{"command":"go test ./...\nignored"}`, agent.ToolPresentation{Name: "Run", Arguments: "go test ./... …"}},
		{"malformed file input", readPresentation, `{`, agent.ToolPresentation{Name: "Read"}},
		{"empty file path", readPresentation, `{"path":"  "}`, agent.ToolPresentation{Name: "Read"}},
		{"multiline file path", readPresentation, `{"path":"main.go\nignored"}`, agent.ToolPresentation{Name: "Read", Arguments: "main.go …"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.presentation(json.RawMessage(test.arguments)); got != test.want {
				t.Fatalf("presentation = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestToolPresentationArgumentsAreBounded(t *testing.T) {
	long := strings.Repeat("界", titleLimit+1)
	if got, want := shellPresentation(json.RawMessage(`{"command":"`+long+`"}`)), (agent.ToolPresentation{Name: "Run", Arguments: strings.Repeat("界", titleLimit) + "…"}); got != want {
		t.Fatalf("presentation = %#v, want %#v", got, want)
	}
}
