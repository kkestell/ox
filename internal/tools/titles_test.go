package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuiltInToolTitlesAreComplete(t *testing.T) {
	cases := map[string]struct {
		arguments string
		want      string
	}{
		"edit_file":             {`{"path":"main.go"}`, "Edit main.go"},
		"glob":                  {`{"pattern":"**/*.go"}`, "Find **/*.go"},
		"grep":                  {`{"pattern":"ToolCall"}`, "Search for ToolCall"},
		"lsp_definition":        {`{"path":"main.go","line":4,"column":8}`, "Find the definition of main.go:4:8"},
		"lsp_diagnostics":       {`{"path":"main.go"}`, "Check main.go"},
		"lsp_document_symbols":  {`{"path":"main.go"}`, "Outline main.go"},
		"lsp_references":        {`{"path":"main.go","line":4,"column":8}`, "Find references to main.go:4:8"},
		"lsp_workspace_symbols": {`{"query":"ToolCall"}`, "Find symbol ToolCall"},
		"memory_delete":         {`{"id":"fact-1"}`, "Delete workspace memory fact-1"},
		"memory_search":         {`{"query":"tool titles"}`, "Search memory for tool titles"},
		"memory_write":          {`{"content":"Titles belong to Ox"}`, "Remember: Titles belong to Ox"},
		"question":              {`{"question":"Continue?"}`, "Ask: Continue?"},
		"read_file":             {`{"path":"main.go"}`, "Read main.go"},
		"shell":                 {`{"command":"go test ./..."}`, "Run go test ./..."},
		"skill":                 {`{"name":"review"}`, "Load skill review"},
		"subagent_list":         {`{}`, "List subagents"},
		"subagent_report":       {`{"message":"Tests passed"}`, "Report: Tests passed"},
		"subagent_send":         {`{"id":"abcdefgh1234"}`, "Message subagent abcdefgh"},
		"subagent_start":        {`{"name":"tests"}`, "Start subagent tests"},
		"subagent_stop":         {`{"id":"abcdefgh1234"}`, "Stop subagent abcdefgh"},
		"subagent_wait":         {`{"ids":["one","two"]}`, "Wait for 2 subagents"},
		"todo":                  {`{}`, "Update the todo list"},
		"web_fetch":             {`{"url":"https://example.test"}`, "Fetch https://example.test"},
		"write_file":            {`{"path":"main.go"}`, "Write main.go"},
	}

	tools := All()
	if len(cases) != len(tools) {
		t.Fatalf("title cases = %d, tools = %d", len(cases), len(tools))
	}
	for _, tool := range tools {
		t.Run(tool.Name, func(t *testing.T) {
			if tool.Title == nil {
				t.Fatal("title function is nil")
			}
			test, ok := cases[tool.Name]
			if !ok {
				t.Fatal("title case is missing")
			}
			if got := tool.Title(json.RawMessage(test.arguments)); got != test.want {
				t.Fatalf("title = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuiltInToolTitlesHandleUnusableSubjects(t *testing.T) {
	for _, test := range []struct {
		name      string
		title     func(json.RawMessage) string
		arguments string
		want      string
	}{
		{"malformed shell input", shellTitle, `{`, "Run a shell command"},
		{"empty shell command", shellTitle, `{"command":"  "}`, "Run a shell command"},
		{"multiline shell command", shellTitle, `{"command":"go test ./...\nignored"}`, "Run go test ./... …"},
		{"malformed file input", readTitle, `{`, "Read a file"},
		{"empty file path", readTitle, `{"path":"  "}`, "Read a file"},
		{"multiline file path", readTitle, `{"path":"main.go\nignored"}`, "Read main.go …"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.title(json.RawMessage(test.arguments)); got != test.want {
				t.Fatalf("title = %q, want %q", got, test.want)
			}
		})
	}
}

func TestToolTitleSubjectsAreBounded(t *testing.T) {
	long := strings.Repeat("界", titleLimit+1)
	if got, want := shellTitle(json.RawMessage(`{"command":"`+long+`"}`)), "Run "+strings.Repeat("界", titleLimit)+"…"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}
