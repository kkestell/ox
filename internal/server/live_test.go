package server

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
	// A misspelled mode, which Claude refuses on stderr.
	h := cfg.Harnesses["claude"]
	h.PermissionModes = append(h.PermissionModes, config.Option{ID: "acceptEdit", Name: "Misspelled"})
	cfg.Harnesses["claude"] = h
	st := openStore(t)
	dir := t.TempDir()
	// t.TempDir is too long for a socket path on macOS.
	bin, err := os.MkdirTemp("", "oxlive")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(bin) })
	mcp := MCP{Forwarder: filepath.Join(bin, "ox-mcp"), Socket: filepath.Join(bin, "oxd.sock")}
	if out, err := exec.Command("go", "build", "-o", mcp.Forwarder, "../../cmd/ox-mcp").CombinedOutput(); err != nil {
		t.Fatalf("build ox-mcp: %v\n%s", err, out)
	}
	s, err := New(st, cfg, parkAfter, map[string]agent.Start{"claude": claude.Start}, mcp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	serve := func(s *Server) net.Listener {
		listener, err := net.Listen("unix", mcp.Socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		go s.Serve(listener)
		return listener
	}
	listener := serve(s)
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

	t.Log("a failed start reports Claude's reason")
	missing := "00000000-0000-4000-8000-000000000000"
	_, err = claude.Start(agent.Options{
		Workspace: dir, Resume: missing, Settings: agent.Settings{Model: haiku, Effort: "low", PermissionMode: "manual"},
		MCP: agent.MCPServer{
			Command: mcp.Forwarder, Env: map[string]string{"OX_SOCKET": mcp.Socket, "OX_SESSION_ID": missing},
			Approved: approvedTools,
		},
		OnUpdate: func(agent.Update) {}, OnError: func(error) {},
	})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("resuming a missing conversation failed without Claude's reason: %v", err)
	} else if !errors.Is(err, agent.ErrNoConversation) {
		t.Errorf("resuming a missing conversation was not recognized as one: %v", err)
	}
	r := c.request("session/send", map[string]string{"session_id": create("acceptEdit"), "text": "Reply with just the word ok."})
	if r.Error == nil || !strings.Contains(r.Error.Message, "'acceptEdit' is invalid") {
		t.Errorf("a start Claude refused on stderr failed without its reason: %+v", r.Error)
	} else {
		t.Logf("session/send: %s", r.Error.Message)
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

	t.Log("an interrupt ends a generating turn with an error result")
	c.forget(a)
	var sent struct {
		MessageID string `json:"message_id"`
	}
	json.Unmarshal(c.call("session/send", map[string]string{"session_id": a, "text": "Write the numbers from 1 to 2000 in words, one per line."}), &sent)
	if u := c.update(a, "text_delta"); u.MessageID != sent.MessageID {
		t.Errorf("the prompt's reply was attributed to %q, not %q", u.MessageID, sent.MessageID)
	}
	c.call("session/interrupt", map[string]string{"session_id": a})
	if u := c.update(a, "result"); !u.Result.IsError || u.Result.Text != "Interrupted" || u.MessageID != sent.MessageID {
		t.Errorf("an interrupted turn ended with %+v", u)
	}
	idle(a)

	t.Log("an interrupt right after a send stops the prompt")
	c.forget(a)
	c.call("session/send", map[string]string{"session_id": a, "text": "Write the numbers from 1 to 2000 in words, one per line."})
	c.call("session/interrupt", map[string]string{"session_id": a})
	if r := c.update(a, "result").Result; !r.IsError || r.Text != "Interrupted" {
		t.Errorf("a prompt interrupted on arrival ended with %+v", r)
	}
	idle(a)

	// steer sends a prompt that waits on a Bash approval, then sends a
	// second prompt while it waits, and returns the approval and the second
	// prompt's ID.
	const steerText = "Also reply with the word steered."
	steer := func(id string) (agent.Update, string) {
		t.Helper()
		c.forget(id)
		c.call("session/send", map[string]string{"session_id": id, "text": "Use the Bash tool to run `sleep 3 && touch steer.txt`, then reply with just the word done."})
		asked := c.update(id, "approval")
		var queued struct {
			MessageID string `json:"message_id"`
		}
		json.Unmarshal(c.call("session/send", map[string]string{"session_id": id, "text": steerText}), &queued)
		var u agent.Update
		c.next(func(m message) bool {
			if m.Method != "session/update" || m.sessionID() != id {
				return false
			}
			u = agent.Update{}
			json.Unmarshal(m.Params, &u)
			return u.Type == "prompt_queued" && u.MessageID == queued.MessageID
		})
		if u.Text != steerText {
			t.Errorf("prompt_queued carried %q", u.Text)
		}
		if busy := c.request("session/send", map[string]string{"session_id": id, "text": "again"}); busy.Error == nil || busy.Error.Code != sessionBusy {
			t.Errorf("a second prompt was queued: %+v", busy.Error)
		}
		return asked, queued.MessageID
	}

	t.Log("a prompt sent while Claude works joins the turn")
	steered := create("manual")
	u, queued := steer(steered)
	for taken := false; ; {
		if u.Type == "approval" {
			var r request
			json.Unmarshal(u.Request, &r)
			c.call("session/approve", map[string]any{"session_id": steered, "request_id": u.RequestID, "response": allow(r)})
		}
		c.next(func(m message) bool {
			if m.Method != "session/update" || m.sessionID() != steered {
				return false
			}
			u = agent.Update{}
			json.Unmarshal(m.Params, &u)
			return u.Type == "prompt" || u.Type == "approval" || (u.Type == "state" && u.State == "idle")
		})
		if u.Type == "prompt" {
			taken = taken || u.MessageID == queued
		}
		if u.Type == "state" {
			if !taken {
				t.Error("Claude did not take the prompt sent while it worked")
			}
			break
		}
	}

	t.Log("Stop drops a prompt Claude has not taken")
	stopped := create("manual")
	_, dropped := steer(stopped)
	c.call("session/interrupt", map[string]string{"session_id": stopped})
	if u := c.update(stopped, "prompt_cancelled"); u.MessageID != dropped || u.Text != steerText {
		t.Errorf("prompt_cancelled named %+v, not %s", u, dropped)
	}
	idle(stopped)
	for _, m := range c.received(stopped) {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		if u.Type == "prompt" && u.MessageID == dropped {
			t.Error("Claude took the prompt Stop dropped")
		}
	}

	t.Log("a session starting Claude holds up only its own requests")
	b := create("acceptEdits")
	start := time.Now()
	sending := c.send("session/send", map[string]string{"session_id": b, "text": "Reply with just the word ok."})
	c.call("session/get", map[string]string{"session_id": a})
	answered := time.Since(start)
	if r := c.response(sending); r.Error != nil {
		t.Fatalf("send: %+v", r.Error)
	}
	if started := time.Since(start); answered > 100*time.Millisecond || answered >= started {
		t.Errorf("session/get took %v while another session took %v to start", answered, started)
	}
	idle(b)

	t.Log("a destroyed session's last notification is session/destroyed")
	d := create("acceptEdits")
	sending = c.send("session/send", map[string]string{"session_id": d, "text": "Reply with just the word ok."})
	destroyed := c.send("session/destroy", map[string]string{"session_id": d})
	c.response(sending) // succeeds or finds the session gone, depending on which runs first
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

	t.Log("a mode Claude changes itself reaches the client")
	planned := create("plan")
	tools := turn(planned, "Load and call the ExitPlanMode tool now with the one-line plan: reply ok. Then reply with just ok.", allow)
	if !slices.Contains(tools, "ExitPlanMode") {
		t.Fatalf("Haiku did not call ExitPlanMode (%v); run again", tools)
	}
	moved := c.next(func(m message) bool { return m.Method == "session/configured" && m.sessionID() == planned })
	var movedTo agent.Settings
	json.Unmarshal(moved.Params, &movedTo)
	if movedTo.PermissionMode != "manual" {
		t.Errorf("approving ExitPlanMode left the mode %q", movedTo.PermissionMode)
	}

	t.Log("Claude lists Ox's models without asking, in every mode")
	refuse := func(r request) any {
		t.Errorf("%s asked for approval", r.ToolName)
		return map[string]any{"behavior": "deny", "message": "no", "interrupt": true}
	}
	listModels := func(id string) {
		t.Helper()
		tools := turn(id, "Call the mcp__ox__list_models tool now, then reply with just ok.", refuse)
		if !slices.Contains(tools, "mcp__ox__list_models") {
			t.Fatalf("Haiku did not call mcp__ox__list_models (%v); run again", tools)
		}
		for _, m := range c.received(id) {
			var u agent.Update
			json.Unmarshal(m.Params, &u)
			if u.Type == "tool_result" && !u.Result.IsError && strings.Contains(u.Result.Text, haiku) {
				return
			}
		}
		t.Errorf("list_models returned no catalog: %v", c.received(id))
	}
	listed := create("manual")
	listModels(listed)
	listModels(create("plan"))

	t.Log("pending schedules keep a session running, even after a failed turn")
	deniedBash := false
	denyBash := func(r request) any {
		if r.ToolName == "Bash" {
			deniedBash = true
			return map[string]any{"behavior": "deny", "message": "no", "interrupt": true}
		}
		return allow(r)
	}
	failed := create("manual")
	tools = turn(failed, "Step 1: load and call the CronCreate tool with cron '*/30 * * * *' and prompt 'say hi'. "+
		"Step 2: only after CronCreate succeeds, run the Bash command `touch marker.txt`. Do both steps.", denyBash)
	if !slices.Contains(tools, "CronCreate") || !slices.Contains(tools, "Bash") {
		t.Fatalf("Haiku did not call CronCreate and then Bash (%v); run again", tools)
	}
	if !deniedBash {
		t.Fatal("Bash did not ask for approval; no interrupting denial was sent")
	}
	completed := create("manual")
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

	t.Log("a resumed session lists Ox's models")
	if !parked(listed) {
		t.Fatal("the session that listed models did not park")
	}
	listModels(listed)

	t.Log("a resumed session keeps restored schedules through an interrupted first turn")
	s.Close()
	listener.Close()
	restarted, err := New(st, cfg, 2*time.Second, map[string]agent.Start{"claude": claude.Start}, mcp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	serve(restarted)
	c = connect(t, restarted)
	c.timeout = 3 * time.Minute
	// Use the session whose completed turn created a recurring job. Deny
	// the first tool on resume so Claude never sends a Stop schedule report.
	deniedBash = false
	turn(completed, "Run the Bash command `touch resumed.txt` now.", denyBash)
	if !deniedBash {
		t.Fatal("the first turn on resume did not reach an interrupting denial")
	}
	time.Sleep(4 * time.Second)
	if parked(completed) {
		t.Error("the interrupted first turn let the resumed session park")
	}
}
