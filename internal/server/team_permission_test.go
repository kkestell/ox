package server

import (
	"encoding/json"
	"strings"
	"testing"

	"ox/internal/catalog"
	"ox/internal/settings"
	"ox/internal/tools"
	"ox/internal/transcript"
)

func TestAgentPermissionShowsAllCreationDetails(t *testing.T) {
	profile := settings.AgentProfile{ID: "fast", Description: "Small tasks.", Model: "model-a", Effort: catalog.EffortHigh}
	for _, permission := range []tools.Permission{
		{Kind: tools.PermissionAgent, Agent: tools.CreateAgentArgs{Name: "worker", Message: "Implement the change", Profile: "fast", Dir: "../other"}, Profile: profile},
		{Kind: tools.PermissionAgent, Agent: tools.CreateAgentArgs{Name: "worker", Message: "Implement the change"}},
	} {
		args := permission.Agent
		arguments, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		request := permissionRequest("session", "/workspace", transcript.ToolCall{
			CallID: "create", Name: tools.CreateAgent, Arguments: string(arguments),
		}, permission)
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			ToolCall struct {
				Content []struct {
					Content struct{ Text string } `json:"content"`
				} `json:"content"`
			} `json:"toolCall"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.ToolCall.Content) != 1 {
			t.Fatalf("permission content = %s", data)
		}
		text := decoded.ToolCall.Content[0].Content.Text
		want := []string{"Name: worker", "Message:\n\n    Implement the change", "Orchestrator workspace: /workspace"}
		if args.Profile == "" {
			want = append(want, "Model: (same as orchestrator)", "Effort: (same as orchestrator)", "Directory: (new Git worktree)")
		} else {
			want = append(want, "Model: model-a", "Effort: high", "Directory: ../other")
		}
		for _, value := range want {
			if !strings.Contains(text, value) {
				t.Errorf("permission missing %q: %q", value, text)
			}
		}
	}
}
