package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolTitles(t *testing.T) {
	cases := []struct {
		name      string
		arguments string
		want      string
	}{
		{"shell", `{"command":"go test ./..."}`, "go test ./..."},
		{"shell", `{"command":"first\nsecond"}`, "first …"},
		{"shell", `{"timeout":5}`, "Run a shell command"},
		{"shell", `{"command":`, "Run a shell command"},
		{"read_file", `{"path":"internal/tools/shell.go"}`, "Read internal/tools/shell.go"},
		{"read_file", `{"path":""}`, "Read a file"},
		{"write_file", `{"path":"notes.md","content":"x"}`, "Write notes.md"},
		{"edit_file", `{"path":"notes.md","old_string":"a","new_string":"b"}`, "Edit notes.md"},
		{"glob", `{"pattern":"**/*.go"}`, "Find **/*.go"},
		{"grep", `{"pattern":"func All"}`, "Search for func All"},
		{"skill", `{"name":"review"}`, "Load skill review"},
		{"todo", `{"todos":[]}`, "Update the todo list"},
		{"question", `{"question":"Which provider?"}`, "Ask: Which provider?"},
		{"memory_search", `{"query":"deploy"}`, "Search memory for deploy"},
		{"memory_search", `{"query":""}`, "List recent memory"},
		{"memory_write", `{"type":"decision","content":"Ship weekly"}`, "Remember: Ship weekly"},
		{"subagent_start", `{"name":"indexer","task":"index"}`, "Start subagent indexer"},
		{"subagent_send", `{"id":"0123456789abcdef","message":"stop"}`, "Message subagent 01234567"},
		{"subagent_stop", `{"id":"0123456789abcdef"}`, "Stop subagent 01234567"},
		{"subagent_list", `{}`, "List subagents"},
		{"subagent_wait", `{}`, "Wait for any subagent"},
		{"subagent_wait", `{"ids":["0123456789abcdef"]}`, "Wait for subagent 01234567"},
		{"subagent_wait", `{"ids":["a","b","c"]}`, "Wait for 3 subagents"},
		{"subagent_report", `{"message":"phase one done"}`, "Report: phase one done"},
	}
	titles := map[string]func(json.RawMessage) string{}
	for _, tool := range All() {
		titles[tool.Name] = tool.Title
	}
	for _, testCase := range cases {
		title := titles[testCase.name]
		if title == nil {
			t.Fatalf("tool %q has no title", testCase.name)
		}
		if got := title(json.RawMessage(testCase.arguments)); got != testCase.want {
			t.Errorf("%s title(%s) = %q, want %q", testCase.name, testCase.arguments, got, testCase.want)
		}
	}
}

func TestToolTitlesAreBounded(t *testing.T) {
	long := strings.Repeat("x", 500)
	for _, tool := range All() {
		if tool.Title == nil {
			t.Errorf("tool %q has no title", tool.Name)
			continue
		}
		arguments, err := json.Marshal(map[string]any{
			"command": long, "path": long, "pattern": long, "name": long,
			"question": long, "query": long, "content": long, "message": long,
			"url": long, "id": long, "task": long,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := tool.Title(arguments)
		if length := len([]rune(got)); length > titleLimit+40 {
			t.Errorf("tool %q title length = %d", tool.Name, length)
		}
	}
}
