package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestWorkspaceSkillLoadsExplicitlyWithoutGrantingExecution(t *testing.T) {
	model := startModel(t,
		toolResponse("skill-load", "skill", `{"name":"review"}`),
		toolResponse("reference-read", "read_file", `{"path":".agents/skills/review/references/checks.md"}`),
		toolResponse("script-run", "shell", `{"cmd":"sh .agents/skills/review/scripts/run.sh"}`),
		sse(evText("done"), evFinishReason("stop")),
	)
	child := start(t,
		withModel(model),
		withFile(".agents/skills/review/SKILL.md", `---
name: review
description: Review completed work.
allowed-tools: shell
---
Follow the review instructions.
Read references/checks.md before running the script.
`),
		withFile(".agents/skills/review/references/checks.md", "Check the result carefully.\n"),
		withFile(".agents/skills/review/scripts/run.sh", "printf 'ran\\n' > skill-ran.txt\n"),
	)
	initialize(t, child)
	defer child.stop()
	session := newSession(t, child, child.cwd)
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("use the review skill"),
	})
	permission := child.serverRequest()
	requested := permissionRequest(t, permission, "script-run")
	if requested.ToolCall.Name != "shell" {
		t.Fatalf("permission tool = %q", requested.ToolCall.Name)
	}
	child.respond(permission, acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Outcome: "selected", OptionID: "reject_once",
	}})
	response := promptResponse(t, child.result(child.await(turn)))
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	if _, err := os.Stat(filepath.Join(child.cwd, "skill-ran.txt")); !os.IsNotExist(err) {
		t.Fatalf("skill script ran without permission: %v", err)
	}

	requests := model.requests()
	if len(requests) != 4 {
		t.Fatalf("model requests = %d, want 4", len(requests))
	}
	system := requests[0].Messages[0].Content[0].Text
	for _, want := range []string{
		`"name":"review"`, `"description":"Review completed work."`,
		`"location":".agents/skills/review/SKILL.md"`,
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("skill catalog missing %q: %q", want, system)
		}
	}
	if strings.Contains(system, "Follow the review instructions") || strings.Contains(system, "allowed-tools") {
		t.Fatalf("initial prompt exposed skill body or extra metadata: %q", system)
	}
	if !requestContainsTool(requests[0], "skill") {
		t.Fatalf("request tools omit skill: %#v", requests[0].Tools)
	}
	if !requestContainsText(requests[1], "Follow the review instructions") {
		t.Fatal("loaded skill body did not enter model context")
	}
	if !requestContainsText(requests[2], "Check the result carefully") {
		t.Fatal("referenced file did not enter model context")
	}
	if !requestContainsText(requests[3], "rejected") {
		t.Fatal("shell rejection did not enter model context")
	}
}

func TestLoadedWorkspaceSkillSurvivesProcessRestart(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		toolResponse("skill-load", "skill", `{"name":"resume"}`),
		sse(evText("first done"), evFinishReason("stop")),
		sse(evText("second done"), evFinishReason("stop")),
	)
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	first := start(t, options...)
	initialize(t, first)
	skillPath := filepath.Join(first.cwd, ".agents", "skills", "resume", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: resume\ndescription: Resume work.\n---\nDurable skill body.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := newSession(t, first, first.cwd)
	prompt(t, first, session, "load the skill")
	_ = updates(t, first, session)
	first.request("session/close", acp.CloseSessionRequest{SessionID: session})
	cwd := first.cwd
	first.stop()

	second := start(t, options...)
	initialize(t, second)
	loadSession(t, second, session, cwd)
	_ = updates(t, second, session)
	prompt(t, second, session, "continue")
	_ = updates(t, second, session)
	second.stop()

	requests := model.requests()
	if len(requests) != 3 || !requestContainsText(requests[2], "Durable skill body.") {
		t.Fatalf("restarted model requests lost loaded skill: %#v", requests)
	}
}

func requestContainsTool(request modelRequest, name string) bool {
	for _, raw := range request.Tools {
		var tool struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if err := json.Unmarshal(raw, &tool); err == nil && tool.Function.Name == name {
			return true
		}
	}
	return false
}

func requestContainsText(request modelRequest, text string) bool {
	for _, message := range request.Messages {
		for _, content := range message.Content {
			if strings.Contains(content.Text, text) {
				return true
			}
		}
	}
	return false
}
