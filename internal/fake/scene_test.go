package fake

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

const testScene = `{"workspaces": [{"sessions": [
  {"title": "Fix retry", "prompt": "fix the retry", "turns": [
    {"usage": [{"interval_seconds": 18000, "used_percent": 30, "resets_in_minutes": 90}],
     "updates": [
       {"type": "reasoning_delta", "text": "Reading."},
       {"type": "tool_use", "tool_use_id": "t1", "tool_name": "Read", "input": {"file_path": "retry.go"}},
       {"type": "tool_result", "tool_use_id": "t1", "result": {"text": "package sync", "is_error": false}}
     ],
     "reply": "Fixed the retry.", "end": "idle"},
    {"reply": "Running the tests.", "end": "working"}
  ], "children": [
    {"name": "review", "message": "review the retry change", "turns": [{"reply": "Looks right.", "end": "idle"}]}
  ]},
  {"title": "Build themes", "prompt": "build the themes", "turns": [
    {"reply": "Building.", "end": "waiting", "approval": {"tool_name": "Bash", "input": {"command": "npm run build"}}}
  ]}
]}]}`

func loadScene(t *testing.T, text string) (*Scene, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scene.json")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadScene(path)
}

// player runs a scripted agent on the test's goroutine, as a session does.
type player struct {
	t       *testing.T
	agent   agent.Agent
	updates []agent.Update
	sent    int
}

func newPlayer(t *testing.T) *player {
	t.Helper()
	scene, err := loadScene(t, testScene)
	if err != nil {
		t.Fatal(err)
	}
	p := &player{t: t}
	p.agent, err = Scripted(scene).Start(agent.Options{
		OnUpdate:     func(u agent.Update) { p.updates = append(p.updates, u) },
		OnResumable:  func(string) {},
		OnConfigured: func(agent.Settings) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.agent.Stop)
	return p
}

// send sends content and handles events until the turn reaches state.
func (p *player) send(content, state string) {
	p.t.Helper()
	p.updates = nil
	p.sent++
	if err := p.agent.Send(agent.Input{MessageID: strconv.Itoa(p.sent), Text: content, Content: content}); err != nil {
		p.t.Fatal(err)
	}
	p.handleUntil(func() bool {
		return p.agent.Snapshot().State == state && (state != agent.StateRunning || len(p.of(agent.UpdateTextDelta)) > 0)
	})
}

func (p *player) handleUntil(done func() bool) {
	p.t.Helper()
	deadline := time.After(5 * time.Second)
	for !done() {
		select {
		case event := <-p.agent.Events():
			p.agent.Handle(event)
		case <-deadline:
			p.t.Fatalf("timed out with updates %+v", p.updates)
		}
	}
}

// of is the updates of kind since the last send.
func (p *player) of(kind string) []agent.Update {
	var updates []agent.Update
	for _, u := range p.updates {
		if u.Type == kind {
			updates = append(updates, u)
		}
	}
	return updates
}

// played is the types of the turn's updates, without its lifecycle updates.
func (p *player) played() []string {
	var types []string
	for _, u := range p.updates {
		if !slices.Contains([]string{agent.UpdatePromptQueued, agent.UpdatePrompt, agent.UpdateState}, u.Type) && (len(types) == 0 || types[len(types)-1] != u.Type) {
			types = append(types, u.Type)
		}
	}
	return types
}

// reply is the text of the turn's text deltas.
func (p *player) reply() string {
	var b strings.Builder
	for _, u := range p.of(agent.UpdateTextDelta) {
		b.WriteString(u.Text)
	}
	return b.String()
}

func TestSceneIdleTurn(t *testing.T) {
	p := newPlayer(t)
	p.send("fix the retry", agent.StateIdle)
	want := []string{agent.UpdateUsage, agent.UpdateReasoningDelta, agent.UpdateToolUse, agent.UpdateToolResult, agent.UpdateTextDelta, agent.UpdateResult}
	if got := p.played(); !slices.Equal(got, want) {
		t.Fatalf("played %v, want %v", got, want)
	}
	if usage := p.of(agent.UpdateUsage)[0].Usage[0]; usage.UsedPercent != 30 || time.Until(usage.ResetsAt) < 89*time.Minute {
		t.Errorf("usage %+v", usage)
	}
	if got := p.reply(); got != "Fixed the retry." {
		t.Errorf("reply %q", got)
	}
	if result := p.of(agent.UpdateResult)[0].Result; result.Text != "Fixed the retry." || result.IsError {
		t.Errorf("result %+v", result)
	}
}

func TestSceneWorkingTurn(t *testing.T) {
	p := newPlayer(t)
	p.send("fix the retry", agent.StateIdle)
	p.send("Reports from your child sessions", agent.StateRunning)
	p.handleUntil(func() bool { return p.reply() == "Running the tests." })
	settled := time.After(5 * step)
	for waiting := true; waiting; {
		select {
		case event := <-p.agent.Events():
			p.agent.Handle(event)
		case <-settled:
			waiting = false
		}
	}
	if results := p.of(agent.UpdateResult); len(results) > 0 || p.agent.Snapshot().State != agent.StateRunning {
		t.Fatalf("results %+v, state %s", results, p.agent.Snapshot().State)
	}
}

func TestSceneWaitingTurn(t *testing.T) {
	p := newPlayer(t)
	p.send("build the themes", agent.StateRequiresAction)
	approvals := p.agent.Snapshot().Approvals
	if len(approvals) != 1 || !strings.Contains(string(approvals[0].Request), `"tool_name":"Bash"`) {
		t.Fatalf("approvals %+v", approvals)
	}
	if err := p.agent.Approve(approvals[0].RequestID, json.RawMessage(`{"behavior":"allow"}`)); err != nil {
		t.Fatal(err)
	}
	p.handleUntil(func() bool { return p.agent.Snapshot().State == agent.StateIdle })
	if result := p.of(agent.UpdateResult)[0].Result; result.Text != "Building." {
		t.Errorf("result %+v", result)
	}
}

func TestSceneChildMessage(t *testing.T) {
	p := newPlayer(t)
	p.send("Message from Ox session abc (\"Fix retry\") in /tmp:\n\nreview the retry change\n\nThis message comes from your parent session.", agent.StateIdle)
	if got := p.reply(); got != "Looks right." {
		t.Errorf("reply %q", got)
	}
}

func TestSceneNoTurn(t *testing.T) {
	for _, inputs := range [][]string{{"hello"}, {"review the retry change", "again"}} {
		p := newPlayer(t)
		for _, input := range inputs {
			p.send(input, agent.StateIdle)
		}
		if result := p.of(agent.UpdateResult)[0].Result; !result.IsError || !strings.Contains(result.Text, "no turn") {
			t.Errorf("%v: result %+v", inputs, result)
		}
	}
}

func TestSceneAsk(t *testing.T) {
	scene, err := loadScene(t, testScene)
	if err != nil {
		t.Fatal(err)
	}
	ask := Scripted(scene).Ask
	if title, err := ask(context.Background(), "", "", "", "build the themes"); title != "Build themes" || err != nil {
		t.Errorf("title %q, %v", title, err)
	}
	if _, err := ask(context.Background(), "", "", "", "review the retry change"); err == nil {
		t.Error("titled a child's message")
	}
}

func TestLoadSceneRefuses(t *testing.T) {
	session := func(prompt, turn string) string {
		return `{"title": "s", "prompt": "` + prompt + `", "turns": [` + turn + `]}`
	}
	scene := func(sessions ...string) string {
		return `{"workspaces": [{"sessions": [` + strings.Join(sessions, ",") + `]}]}`
	}
	for name, text := range map[string]string{
		"unknown end":       scene(session("a", `{"end": "done"}`)),
		"idle approval":     scene(session("a", `{"end": "idle", "approval": {"tool_name": "Bash"}}`)),
		"waiting, no ask":   scene(session("a", `{"end": "waiting"}`)),
		"lifecycle update":  scene(session("a", `{"end": "idle", "updates": [{"type": "result"}]}`)),
		"empty prompt":      scene(session(" ", `{"end": "idle"}`)),
		"contained prompt":  scene(session("fix it", `{"end": "idle"}`), session("fix it now", `{"end": "idle"}`)),
		"contained message": `{"workspaces": [{"sessions": [{"title": "s", "prompt": "review", "children": [{"name": "c", "message": "review it", "turns": []}]}]}]}`,
	} {
		if _, err := loadScene(t, text); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}
