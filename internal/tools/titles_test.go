package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

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
