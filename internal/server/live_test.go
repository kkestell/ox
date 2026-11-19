package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/claude"
	"github.com/kkestell/ox/internal/config"
)

const haiku = "claude-haiku-5-5"

func TestLive(t *testing.T) {
	if os.Getenv("OX_LIVE") == "" {
		t.Skip("set OX_LIVE=1 to run against real Claude with Haiku, as make live does")
	}
	const parkAfter = 15 * time.Second
	cfg, err := config.Load(filepath.Join(t.TempDir(), "none.json")) // the built-in configuration
	if err != nil {
		t.Fatal(err)
	}
	st := openStore(t)
	dir := t.TempDir()
	s, err := New(st, cfg, parkAfter, map[string]agent.Start{"claude": claude.Start})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	c.timeout = 3 * time.Minute

	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": dir}), &workspace)
	create := func(mode string) string {
		var created struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(c.call("session/create", map[string]string{
			"workspace_id": workspace.WorkspaceID, "harness": "claude", "model": haiku, "effort": "low", "permission_mode": mode,
		}), &created)
		return created.SessionID
	}
	type request struct {
		ToolName string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
	}
	allow := func(r request) any {
		return map[string]any{"behavior": "allow", "updatedInput": r.Input}
	}
	turn := func(id, text string, answer func(request) any) []string {
		t.Helper()
		c.forget(id)
		c.call("session/send", map[string]string{"session_id": id, "text": text})
		var tools []string
		for {
			var u agent.Update
			c.next(func(m message) bool {
				if m.Method != "session/update" || m.sessionID() != id {
					return false
				}
				u = agent.Update{}
				json.Unmarshal(m.Params, &u)
				return u.Type == "tool_use" || u.Type == "approval" || (u.Type == "state" && u.State == "idle")
			})
			switch u.Type {
			case "tool_use":
				tools = append(tools, u.ToolName)
			case "approval":
				var r request
				json.Unmarshal(u.Request, &r)
				c.call("session/approve", map[string]any{"session_id": id, "request_id": u.RequestID, "response": answer(r)})
			default:
				return tools
			}
		}
	}
	idle := func(id string) {
		for c.update(id, "state").State != "idle" {
		}
	}
	lastIs := func(id, method string) bool {
		received := c.received(id)
		return len(received) > 0 && received[len(received)-1].Method == method
	}
	parked := func(id string) bool {
		for _, m := range c.received(id) {
			var u agent.Update
			json.Unmarshal(m.Params, &u)
			if m.Method == "session/update" && u.Type == "parked" {
				return true
			}
		}
		return false
	}

	t.Log("configuring one setting keeps the others")
	a := create("acceptEdits")
	turn(a, "Reply with just the word ok.", allow)
	c.call("session/configure", map[string]string{"session_id": a, "effort": "high"})
	configured := c.next(func(m message) bool { return m.Method == "session/configured" && m.sessionID() == a })
	var settings agent.Settings
	json.Unmarshal(configured.Params, &settings)
	if settings != (agent.Settings{Model: haiku, Effort: "high", PermissionMode: "acceptEdits"}) {
		t.Errorf("configuring effort changed other settings: %+v", settings)
	}
	turn(a, "Reply with just the word again.", allow)

	t.Log("a session starting Claude holds up only its own requests")
	b := create("acceptEdits")
	start := time.Now()
	sent := c.send("session/send", map[string]string{"session_id": b, "text": "Reply with just the word ok."})
	c.call("session/get", map[string]string{"session_id": a})
	answered := time.Since(start)
	if r := c.response(sent); r.Error != nil {
		t.Fatalf("send: %+v", r.Error)
	}
	if started := time.Since(start); answered > 100*time.Millisecond || answered >= started {
		t.Errorf("session/get took %v while another session took %v to start", answered, started)
	}
	idle(b)

	t.Log("a destroyed session's last notification is session/destroyed")
	d := create("acceptEdits")
	sent = c.send("session/send", map[string]string{"session_id": d, "text": "Reply with just the word ok."})
	destroyed := c.send("session/destroy", map[string]string{"session_id": d})
	c.response(sent) // succeeds or finds the session gone, depending on which runs first
	if r := c.response(destroyed); r.Error != nil {
		t.Fatalf("destroy while starting: %+v", r.Error)
	}
	configuring := c.send("session/configure", map[string]string{"session_id": b, "effort": "high"})
	destroyed = c.send("session/destroy", map[string]string{"session_id": b})
	c.response(configuring)
	if r := c.response(destroyed); r.Error != nil {
		t.Fatalf("destroy while configuring: %+v", r.Error)
	}
	time.Sleep(time.Second)
	if !lastIs(d, "session/destroyed") || !lastIs(b, "session/destroyed") {
		t.Errorf("a notification followed session/destroyed: %v, %v", c.received(d), c.received(b))
	}

	t.Log("pending schedules keep a session running, even after a failed turn")
	denyBash := func(r request) any {
		if r.ToolName == "Bash" {
			return map[string]any{"behavior": "deny", "message": "no", "interrupt": true}
		}
		return allow(r)
	}
	failed := create("default")
	tools := turn(failed, "Step 1: load and call the CronCreate tool with cron '*/30 * * * *' and prompt 'say hi'. "+
		"Step 2: only after CronCreate succeeds, run the Bash command `touch marker.txt`. Do both steps.", denyBash)
	if !slices.Contains(tools, "CronCreate") || !slices.Contains(tools, "Bash") {
		t.Fatalf("Haiku did not call CronCreate and then Bash (%v); run again", tools)
	}
	completed := create("acceptEdits")
	tools = turn(completed, "Use the CronCreate tool to create a recurring job with cron '*/30 * * * *' and prompt 'say hi'. "+
		"Then reply with just the word ok.", allow)
	if !slices.Contains(tools, "CronCreate") {
		t.Fatalf("Haiku did not call CronCreate (%v); run again", tools)
	}
	time.Sleep(parkAfter + 5*time.Second)
	if parked(failed) {
		t.Error("a failed turn that created a cron let its session park")
	}
	if parked(completed) {
		t.Error("a completed turn that created a cron let its session park")
	}
	if !parked(a) {
		t.Error("an idle session with nothing scheduled did not park")
	}
}
