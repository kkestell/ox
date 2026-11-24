package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/claude"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/store"
	"github.com/kkestell/ox/internal/ur"
)

const haiku = "claude-haiku-5-5"

// live is one section of TestLive: its t, a client that reports to it, and
// the daemon's workspace and store.
type live struct {
	t         *testing.T
	c         *client
	workspace string
	st        *store.Store
}

// liveRequest is the part of a Claude approval request the sections read.
type liveRequest struct {
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input"`
}

func allow(r liveRequest) any {
	return map[string]any{"behavior": "allow", "updatedInput": r.Input}
}

// denyBash allows every call but Bash, which it denies with an interrupt and
// records in denied.
func denyBash(denied *bool) func(liveRequest) any {
	return func(r liveRequest) any {
		if r.ToolName == "Bash" {
			*denied = true
			return map[string]any{"behavior": "deny", "message": "no", "interrupt": true}
		}
		return allow(r)
	}
}

// refuse fails the section for any call that asks for approval.
func (l *live) refuse(r liveRequest) any {
	l.t.Errorf("%s asked for approval", r.ToolName)
	return map[string]any{"behavior": "deny", "message": "no", "interrupt": true}
}

func (l *live) create(mode string) string {
	l.t.Helper()
	var created struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(l.c.call("session/create", map[string]string{
		"workspace_id": l.workspace, "harness": "claude", "model": haiku, "effort": "low", "permission_mode": mode,
	}), &created)
	return created.SessionID
}

// turn sends text to session id, answers each approval with answer, and
// returns the tools Claude called once the session is idle.
func (l *live) turn(id, text string, answer func(liveRequest) any) []string {
	l.t.Helper()
	l.c.forget(id)
	l.c.call("session/send", map[string]string{"session_id": id, "text": text})
	var tools []string
	for {
		var u agent.Update
		l.c.next(func(m message) bool {
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
			var r liveRequest
			json.Unmarshal(u.Request, &r)
			l.c.call("session/approve", map[string]any{"session_id": id, "request_id": u.RequestID, "response": answer(r)})
		default:
			return tools
		}
	}
}

func (l *live) idle(id string) {
	l.t.Helper()
	for l.c.update(id, "state").State != "idle" {
	}
}

// parked reports whether session id has parked since the client last
// forgot it.
func (l *live) parked(id string) bool {
	for _, m := range l.c.received(id) {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		if m.Method == "session/update" && u.Type == "parked" {
			return true
		}
	}
	return false
}

func (l *live) resumeToken(id string) string {
	l.t.Helper()
	workspaces, err := l.st.Workspaces()
	if err != nil {
		l.t.Fatal(err)
	}
	for _, w := range workspaces {
		for _, record := range w.Sessions {
			if record.ID == id {
				return record.ResumeToken
			}
		}
	}
	l.t.Fatalf("session %s not found in the database", id)
	return ""
}

// listModels has session id list Ox's models without asking.
func (l *live) listModels(id string) {
	l.t.Helper()
	tools := l.turn(id, "Call the mcp__ox__list_models tool now, then reply with just ok.", l.refuse)
	if !slices.Contains(tools, "mcp__ox__list_models") {
		l.t.Fatalf("Haiku did not call mcp__ox__list_models (%v); run again", tools)
	}
	for _, m := range l.c.received(id) {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		if u.Type == "tool_result" && !u.Result.IsError && strings.Contains(u.Result.Text, haiku) {
			return
		}
	}
	l.t.Errorf("list_models returned no catalog: %v", l.c.received(id))
}

const steerText = "Also reply with the word steered."

// steer sends a prompt that waits on a Bash approval, then sends a second
// prompt while it waits, and returns the approval and the second prompt's ID.
func (l *live) steer(id string) (agent.Update, string) {
	l.t.Helper()
	l.c.forget(id)
	l.c.call("session/send", map[string]string{"session_id": id, "text": "Use the Bash tool to run `sleep 3 && touch steer.txt`, then reply with just the word done."})
	asked := l.c.update(id, "approval")
	var queued struct {
		MessageID string `json:"message_id"`
	}
	json.Unmarshal(l.c.call("session/send", map[string]string{"session_id": id, "text": steerText}), &queued)
	var u agent.Update
	l.c.next(func(m message) bool {
		if m.Method != "session/update" || m.sessionID() != id {
			return false
		}
		u = agent.Update{}
		json.Unmarshal(m.Params, &u)
		return u.Type == "prompt_queued" && u.MessageID == queued.MessageID
	})
	if u.Text != steerText {
		l.t.Errorf("prompt_queued carried %q", u.Text)
	}
	if busy := l.c.request("session/send", map[string]string{"session_id": id, "text": "again"}); busy.Error == nil || busy.Error.Code != sessionBusy {
		l.t.Errorf("a second prompt was queued: %+v", busy.Error)
	}
	return asked, queued.MessageID
}

// message has a session in mode list the sessions and message a new parked
// one, without asking, and the recipient starts and takes it.
func (l *live) message(mode string) {
	l.t.Helper()
	from, to := l.create(mode), l.create("manual")
	tools := l.turn(from, "Do both steps. Step 1: load and call the mcp__ox__list_sessions tool. "+
		"Step 2: load and call the mcp__ox__send_message tool with to set to "+to+
		` and text "Reply with just the word ok, without calling any tool." Then reply with just ok.`, l.refuse)
	if !slices.Contains(tools, "mcp__ox__list_sessions") || !slices.Contains(tools, "mcp__ox__send_message") {
		l.t.Fatalf("Haiku did not call list_sessions and send_message (%v); run again: %s", tools, describe(l.c.received(from)))
	}
	listedRecipient := false
	for _, m := range l.c.received(from) {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		if u.Type == "tool_result" && !u.Result.IsError && strings.Contains(u.Result.Text, `"session_id":"`+to+`"`) {
			listedRecipient = true
		}
	}
	if !listedRecipient {
		l.t.Errorf("list_sessions did not list the recipient: %v", l.c.received(from))
	}
	if u := l.c.update(to, "prompt"); u.From != from {
		l.t.Errorf("the recipient took a prompt from %q, not %q", u.From, from)
	}
	l.idle(to)
}

func describe(messages []message) string {
	var b strings.Builder
	for _, m := range messages {
		b.Write(m.Params)
		b.WriteString("\n")
	}
	return b.String()
}

// keepPlansIn has Claude write the plan files of sessions in workspace under
// it, where the test removes them, rather than in the user's ~/.claude/plans.
func keepPlansIn(t *testing.T, workspace string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(workspace, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"plansDirectory": ".claude/plans"}`)
	if err := os.WriteFile(filepath.Join(workspace, ".claude", "settings.json"), settings, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLive runs sections against real Claude Code with Haiku. Each section
// creates the sessions it uses, so -run TestLive/<section> runs one alone.
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
	keepPlansIn(t, dir)
	// A stored session whose conversation Claude does not have.
	const lost = "lost-session"
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &store.Session{
		ID: lost, Harness: "claude", Model: haiku, Effort: "low", PermissionMode: "manual",
		ResumeToken: "00000000-0000-4000-8000-000000000000", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
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
	// serve starts a daemon that outlives the section that starts it.
	top := t
	serve := func(t *testing.T, park time.Duration) (*Server, net.Listener) {
		t.Helper()
		s, err := New(st, cfg, park, map[string]agent.Harness{"claude": claude.Harness}, mcp)
		if err != nil {
			t.Fatal(err)
		}
		top.Cleanup(s.Close)
		listener, err := net.Listen("unix", mcp.Socket)
		if err != nil {
			t.Fatal(err)
		}
		top.Cleanup(func() { listener.Close() })
		go s.Serve(listener)
		return s, listener
	}
	s, listener := serve(t, parkAfter)
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(connect(t, s).call("workspace/create", map[string]string{"path": dir}), &workspace)
	open := func(t *testing.T) *live {
		t.Helper()
		c := connect(t, s)
		c.timeout = 3 * time.Minute
		return &live{t: t, c: c, workspace: workspace.WorkspaceID, st: st}
	}
	section := func(name string, run func(l *live)) {
		t.Run(name, func(t *testing.T) { run(open(t)) })
	}
	// restart replaces the daemon, which the sections after l's keep, and
	// returns l's section on the new one.
	restart := func(l *live, park time.Duration) *live {
		l.t.Helper()
		s.Close()
		listener.Close()
		s, listener = serve(l.t, park)
		return open(l.t)
	}

	section("failed start", func(l *live) {
		var lostSend struct {
			MessageID string `json:"message_id"`
		}
		json.Unmarshal(l.c.call("session/send", map[string]string{"session_id": lost, "text": "Reply with just the word ok."}), &lostSend)
		if u := l.c.update(lost, "prompt_cancelled"); u.MessageID != lostSend.MessageID {
			l.t.Errorf("resuming a missing conversation did not drop the prompt: %+v", u)
		}
		if u := l.c.update(lost, "exited"); !strings.Contains(u.Error, "could not resume the earlier conversation") {
			l.t.Errorf("resuming a missing conversation did not say so: %+v", u)
		}
		l.c.update(lost, "parked")
		if token := l.resumeToken(lost); token != "" {
			l.t.Errorf("the missing conversation's token was kept: %q", token)
		}
		misspelled := l.create("acceptEdit")
		l.c.call("session/send", map[string]string{"session_id": misspelled, "text": "Reply with just the word ok."})
		if u := l.c.update(misspelled, "exited"); !strings.Contains(u.Error, "'acceptEdit' is invalid") {
			l.t.Errorf("a start Claude refused on stderr failed without its reason: %+v", u)
		} else {
			l.t.Logf("exited: %s", u.Error)
		}
	})

	section("resume after stop", func(l *live) {
		var direct []agent.Update
		var conversation string
		options := agent.Options{
			Workspace: dir, Settings: agent.Settings{Model: haiku, Effort: "low", PermissionMode: "manual"},
			MCP: agent.MCPServer{
				Command: mcp.Forwarder, Env: map[string]string{"OX_SOCKET": mcp.Socket, "OX_SESSION_ID": "direct"},
				Approved: approvedTools(),
			},
			OnUpdate:     func(u agent.Update) { direct = append(direct, u) },
			OnResumable:  func(token string) { conversation = token },
			OnConfigured: func(agent.Settings) {},
			OnError:      func(err error) { l.t.Log(err) },
		}
		// ask sends text to a and handles its events until Claude is idle,
		// and returns the turn's result.
		ask := func(a agent.Agent, text string) string {
			l.t.Helper()
			id := rand.Text()
			if err := a.Send(agent.Input{MessageID: id, Text: text, Content: text}); err != nil {
				l.t.Fatal(err)
			}
			timeout := time.After(3 * time.Minute)
			var reply string
			for seen := len(direct); ; {
				select {
				case e := <-a.Events():
					a.Handle(e)
				case <-timeout:
					l.t.Fatal("Claude did not finish the turn")
				}
				for ; seen < len(direct); seen++ {
					switch u := direct[seen]; {
					case u.Type == agent.UpdateResult:
						reply = u.Result.Text
					case u.Type == agent.UpdateExited:
						l.t.Fatalf("Claude exited: %s", u.Error)
					case u.Type == agent.UpdateState && u.State == agent.StateIdle && u.MessageID == id:
						return reply
					}
				}
			}
		}
		first, err := claude.Start(options)
		if err != nil {
			l.t.Fatal(err)
		}
		ask(first, "Remember the word heliotrope. Reply with just ok.")
		if conversation == "" {
			l.t.Fatal("the conversation was not resumable")
		}
		first.Stop()
		exiting := exec.Command("pgrep", "-f", "session-id="+conversation).Run() == nil
		options.Resume = conversation
		second, err := claude.Start(options)
		if err != nil {
			l.t.Fatal(err)
		}
		if reply := ask(second, "What word did I ask you to remember? Reply with just the word."); !strings.Contains(strings.ToLower(reply), "heliotrope") {
			l.t.Errorf("the resumed conversation answered %q", reply)
		}
		second.Stop()
		if !exiting {
			l.t.Errorf("the stopped process had exited before the next one started")
		}
	})

	section("resume after restart", func(l *live) {
		// The first approval can be pending when the daemon restarts.
		pendingID := l.create("manual")
		const marker = "unfinished-ox-test-49"
		l.c.call("session/send", map[string]string{"session_id": pendingID, "text": "Use Bash to run `touch " + marker + "`."})
		if approval := l.c.update(pendingID, "approval"); !strings.Contains(string(approval.Request), marker) {
			l.t.Fatalf("unexpected approval: %s", approval.Request)
		}
		pendingToken := l.resumeToken(pendingID)
		if pendingToken == "" {
			l.t.Fatal("the first prompt left no resume token while waiting on an approval")
		}
		configDir := os.Getenv("CLAUDE_CONFIG_DIR")
		if configDir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				l.t.Fatal(err)
			}
			configDir = filepath.Join(home, ".claude")
		}
		files, err := filepath.Glob(filepath.Join(configDir, "projects", "*", pendingToken+".jsonl"))
		if err != nil || len(files) != 1 {
			l.t.Fatalf("no transcript for %s: %v, %v", pendingToken, files, err)
		}
		transcript, err := os.ReadFile(files[0])
		if err != nil || !strings.Contains(string(transcript), "Use Bash to run `touch "+marker+"`.") {
			l.t.Fatalf("Claude did not write the first prompt before the approval: %v", err)
		}

		// /clear replaces the resume token even with an empty result.
		cleared := l.create("manual")
		l.turn(cleared, "Reply with just the word ok.", allow)
		old := l.resumeToken(cleared)
		l.turn(cleared, "/clear", allow)
		clearedToken := l.resumeToken(cleared)
		if old == "" || clearedToken == "" || clearedToken == old {
			l.t.Fatalf("/clear did not replace the resume token: %q -> %q", old, clearedToken)
		}

		l = restart(l, parkAfter)
		l.turn(pendingID, "What exact filename did my first prompt ask to create? Reply with just the filename; do not call tools.", allow)
		if r := l.c.update(pendingID, "result").Result; r.IsError || !strings.Contains(r.Text, marker) || l.resumeToken(pendingID) != pendingToken {
			l.t.Fatalf("the reply did not resume the unfinished conversation: %+v", r)
		}
		l.turn(cleared, "Reply with just the word ok.", allow)
		if r := l.c.update(cleared, "result").Result; r.IsError || l.resumeToken(cleared) != clearedToken {
			l.t.Fatalf("the cleared conversation did not resume: %+v", r)
		}
	})

	section("local command output", func(l *live) {
		// /cost output is visible as reply text even without deltas from Claude.
		id := l.create("manual")
		l.turn(id, "Reply with just the word ok.", allow)
		l.turn(id, "/cost", allow)
		delta, result := l.c.update(id, "text_delta"), l.c.update(id, "result")
		if result.Result.IsError || delta.Text == "" || delta.Text != result.Result.Text || delta.MessageID != result.MessageID {
			l.t.Fatalf("local command output was not published as the reply: %+v, %+v", delta, result)
		}
	})

	section("image prompt", func(l *live) {
		picture := image.NewRGBA(image.Rect(0, 0, 64, 64))
		draw.Draw(picture, picture.Bounds(), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, picture); err != nil {
			l.t.Fatal(err)
		}
		id := l.create("manual")
		var sent struct {
			MessageID string `json:"message_id"`
		}
		json.Unmarshal(l.c.call("session/send", map[string]any{
			"session_id": id, "text": "What color is this image? Reply with one word.",
			"images": []string{base64.StdEncoding.EncodeToString(encoded.Bytes())},
		}), &sent)
		if u := l.c.update(id, "prompt"); u.MessageID != sent.MessageID || len(u.Images) != 1 {
			l.t.Errorf("Claude took %+v, not the image prompt with its image", u)
		}
		if u := l.c.update(id, "result"); u.Result.IsError {
			l.t.Errorf("the image prompt's turn failed: %s", u.Result.Text)
		}
	})

	section("configure keeps other settings", func(l *live) {
		a := l.create("acceptEdits")
		l.turn(a, "Reply with just the word ok.", allow)
		l.c.call("session/configure", map[string]string{"session_id": a, "effort": "high"})
		configured := l.c.next(func(m message) bool { return m.Method == "session/configured" && m.sessionID() == a })
		var settings agent.Settings
		json.Unmarshal(configured.Params, &settings)
		if settings != (agent.Settings{Model: haiku, Effort: "high", PermissionMode: "acceptEdits"}) {
			l.t.Errorf("configuring effort changed other settings: %+v", settings)
		}
		l.turn(a, "Reply with just the word again.", allow)
	})

	section("interrupt", func(l *live) {
		a := l.create("acceptEdits")
		l.turn(a, "Reply with just the word ok.", allow)

		// An interrupt ends a generating turn with an error result.
		l.c.forget(a)
		var sent struct {
			MessageID string `json:"message_id"`
		}
		json.Unmarshal(l.c.call("session/send", map[string]string{"session_id": a, "text": "Write the numbers from 1 to 2000 in words, one per line."}), &sent)
		if u := l.c.update(a, "text_delta"); u.MessageID != sent.MessageID {
			l.t.Errorf("the prompt's reply was attributed to %q, not %q", u.MessageID, sent.MessageID)
		}
		l.c.call("session/interrupt", map[string]string{"session_id": a})
		if u := l.c.update(a, "result"); !u.Result.IsError || u.Result.Text != "Interrupted" || u.MessageID != sent.MessageID {
			l.t.Errorf("an interrupted turn ended with %+v", u)
		}
		l.idle(a)

		// An interrupt right after a send stops the prompt.
		l.c.forget(a)
		l.c.call("session/send", map[string]string{"session_id": a, "text": "Write the numbers from 1 to 2000 in words, one per line."})
		l.c.call("session/interrupt", map[string]string{"session_id": a})
		if r := l.c.update(a, "result").Result; !r.IsError || r.Text != "Interrupted" {
			l.t.Errorf("a prompt interrupted on arrival ended with %+v", r)
		}
		l.idle(a)
	})

	section("steering", func(l *live) {
		// A prompt sent while Claude works joins the turn.
		steered := l.create("manual")
		u, queued := l.steer(steered)
		for taken := false; ; {
			if u.Type == "approval" {
				var r liveRequest
				json.Unmarshal(u.Request, &r)
				l.c.call("session/approve", map[string]any{"session_id": steered, "request_id": u.RequestID, "response": allow(r)})
			}
			l.c.next(func(m message) bool {
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
					l.t.Error("Claude did not take the prompt sent while it worked")
				}
				break
			}
		}
	})

	section("stop drops untaken prompt", func(l *live) {
		stopped := l.create("manual")
		_, dropped := l.steer(stopped)
		l.c.call("session/interrupt", map[string]string{"session_id": stopped})
		if u := l.c.update(stopped, "prompt_cancelled"); u.MessageID != dropped || u.Text != steerText {
			l.t.Errorf("prompt_cancelled named %+v, not %s", u, dropped)
		}
		l.idle(stopped)
		for _, m := range l.c.received(stopped) {
			var u agent.Update
			json.Unmarshal(m.Params, &u)
			if u.Type == "prompt" && u.MessageID == dropped {
				l.t.Error("Claude took the prompt Stop dropped")
			}
		}
	})

	section("own mode change", func(l *live) {
		// A mode Claude changes itself reaches the client.
		planned := l.create("plan")
		tools := l.turn(planned, "Load and call the ExitPlanMode tool now with the one-line plan: reply ok. Then reply with just ok.", allow)
		if !slices.Contains(tools, "ExitPlanMode") {
			l.t.Fatalf("Haiku did not call ExitPlanMode (%v); run again", tools)
		}
		moved := l.c.next(func(m message) bool { return m.Method == "session/configured" && m.sessionID() == planned })
		var movedTo agent.Settings
		json.Unmarshal(moved.Params, &movedTo)
		if movedTo.PermissionMode != "manual" {
			l.t.Errorf("approving ExitPlanMode left the mode %q", movedTo.PermissionMode)
		}
	})

	section("list models", func(l *live) {
		// Claude lists Ox's models without asking, in every mode, and once
		// resumed.
		listed := l.create("manual")
		l.listModels(listed)
		l.listModels(l.create("plan"))
		l.c.update(listed, "parked")
		l.listModels(listed)
	})

	section("background work", func(l *live) {
		// Background work shows as background until it ends, then Claude's
		// own turn runs.
		worker := l.create("manual")
		l.c.call("session/send", map[string]string{"session_id": worker,
			"text": "Run the Bash command `sleep 8` with run_in_background set to true. Do not wait for it or check on it. Then reply with just the word started."})
		// states is the session's published states, each change once, from
		// the first background on.
		var states []string
		for !slices.Equal(states, []string{"background", "idle", "running", "idle"}) {
			var u agent.Update
			l.c.next(func(m message) bool {
				if m.Method != "session/update" || m.sessionID() != worker {
					return false
				}
				u = agent.Update{}
				json.Unmarshal(m.Params, &u)
				return u.Type == "approval" || u.Type == "state"
			})
			switch {
			case u.Type == "approval":
				var r liveRequest
				json.Unmarshal(u.Request, &r)
				l.c.call("session/approve", map[string]any{"session_id": worker, "request_id": u.RequestID, "response": allow(r)})
			case u.State == "background" || len(states) > 0:
				if len(states) == 0 || states[len(states)-1] != u.State {
					states = append(states, u.State)
				}
				if len(states) > 4 {
					l.t.Fatalf("background work published %v", states)
				}
			}
		}
		if snapshot := l.c.call("session/get", map[string]string{"session_id": worker}); !strings.Contains(string(snapshot), `"state":"idle"`) {
			l.t.Errorf("after its background work the session is not idle: %s", snapshot)
		}
	})

	section("message parked session", func(l *live) {
		// Claude lists sessions and messages a parked one.
		l.message("manual")
		l.message("auto")
	})

	section("read long transcript", func(l *live) {
		// Claude reads another session's long transcript without asking, in
		// plan mode.
		const endMarker = "END-MARKER-7731"
		long := l.create("manual")
		l.turn(long, strings.Repeat("lorem ipsum dolor sit amet ", 1420)+"The marker is "+endMarker+". Reply with just ok.", l.refuse)
		reader := l.create("plan")
		tools := l.turn(reader, "Load and call the mcp__ox__read_session tool with to set to "+long+
			`. Then reply with just the marker that ends the user prompt in the transcript, the word after "The marker is".`, l.refuse)
		if !slices.Contains(tools, "mcp__ox__read_session") {
			l.t.Fatalf("Haiku did not call read_session (%v); run again: %s", tools, describe(l.c.received(reader)))
		}
		read, replied := false, false
		for _, m := range l.c.received(reader) {
			var u agent.Update
			json.Unmarshal(m.Params, &u)
			switch {
			case u.Type == "result":
				replied = replied || strings.Contains(u.Result.Text, endMarker)
			case u.Type == "tool_result" && !u.Result.IsError && strings.Contains(u.Result.Text, endMarker) && len(u.Result.Text) >= 38000:
				read = true
				l.t.Logf("read_session's result reached Claude as %d characters", len(u.Result.Text))
			}
		}
		if !read {
			l.t.Errorf("read_session did not return the whole transcript: %v", l.c.received(reader))
		}
		if !replied {
			l.t.Errorf("Claude did not reply with the end of the transcript: %v", l.c.received(reader))
		}
	})

	section("plain-words messaging", func(l *live) {
		// Asked in plain words, Claude messages another session through Ox.
		from, to := l.create("acceptEdits"), l.create("manual")
		tools := l.turn(from, "Exchange a message with the other agent, session "+to+
			": ask it to reply with just the word ok, without calling any tool. Then reply with just ok.", l.refuse)
		if slices.Contains(tools, "SendMessage") || slices.Contains(tools, "ListAgents") {
			l.t.Fatalf("Claude used its own messaging tools: %v", tools)
		}
		if !slices.Contains(tools, "mcp__ox__send_message") {
			l.t.Fatalf("Haiku called no messaging tool (%v); run again: %s", tools, describe(l.c.received(from)))
		}
		if u := l.c.update(to, "prompt"); u.From != from {
			l.t.Errorf("the recipient took a prompt from %q, not %q", u.From, from)
		}
		l.idle(to)
		tools = l.turn(from, "Load and call the ListAgents tool. Then reply with just ok.", l.refuse)
		if slices.Contains(tools, "ListAgents") {
			l.t.Errorf("Claude called ListAgents: %v", tools)
		}
	})

	section("plan-mode message asks", func(l *live) {
		// Haiku in plan mode usually declines to call a tool that is not
		// read-only; when it does call send_message, the call asks first.
		planner, recipient := l.create("plan"), l.create("manual")
		askedToSend := false
		tools := l.turn(planner, "Load and call the mcp__ox__send_message tool with to set to "+recipient+
			` and text "Reply with just the word ok." Then reply with just ok.`, func(r liveRequest) any {
			if r.ToolName != "mcp__ox__send_message" {
				l.t.Errorf("%s asked for approval in plan mode", r.ToolName)
			}
			askedToSend = true
			return allow(r)
		})
		switch {
		case askedToSend:
			if u := l.c.update(recipient, "prompt"); u.From != planner {
				l.t.Errorf("the recipient took a prompt from %q, not %q", u.From, planner)
			}
			l.idle(recipient)
		case slices.Contains(tools, "mcp__ox__send_message"):
			l.t.Error("send_message ran without asking in plan mode")
		default:
			l.t.Logf("Haiku declined to call send_message in plan mode (%v)", tools)
			for _, m := range l.c.received(recipient) {
				if m.Method == "session/update" {
					l.t.Errorf("a message reached the recipient without approval: %s", m.Params)
				}
			}
		}
	})

	section("refused message", func(l *live) {
		// A refused message reaches Claude as a tool error.
		unknown := l.create("manual")
		tools := l.turn(unknown, `Load and call the mcp__ox__send_message tool with to set to "no-such-session" and text "hi". Then reply with just ok.`, l.refuse)
		if !slices.Contains(tools, "mcp__ox__send_message") {
			l.t.Fatalf("Haiku did not call send_message (%v); run again", tools)
		}
		refused := false
		for _, m := range l.c.received(unknown) {
			var u agent.Update
			json.Unmarshal(m.Params, &u)
			if u.Type == "tool_result" && u.Result.IsError && strings.Contains(u.Result.Text, "session not found") {
				refused = true
			}
		}
		if !refused {
			l.t.Errorf("the refusal did not reach Claude as an error: %v", l.c.received(unknown))
		}
	})

	section("child sessions", func(l *live) {
		// Claude starts a child session without asking, and the child starts
		// one of its own on its parent's request.
		parent, childDir := l.create("manual"), l.t.TempDir()
		tools := l.turn(parent, "Load and call the mcp__ox__create_session tool with name helper, title Helper, dir "+childDir+
			`, and message "Load and call the mcp__ox__create_session tool with name leaf, title Leaf, and message 'Reply with just the word ok, without calling any tool.' Then reply with just ok." Then reply with just ok.`, l.refuse)
		if !slices.Contains(tools, "mcp__ox__create_session") {
			l.t.Fatalf("Haiku did not call create_session (%v); run again: %s", tools, describe(l.c.received(parent)))
		}
		var started struct {
			SessionID string `json:"session_id"`
		}
		for _, m := range l.c.received(parent) {
			var u agent.Update
			json.Unmarshal(m.Params, &u)
			if u.Type == "tool_result" && !u.Result.IsError && strings.Contains(u.Result.Text, `"session_id"`) {
				json.Unmarshal([]byte(u.Result.Text), &started)
			}
		}
		if started.SessionID == "" {
			l.t.Fatalf("create_session returned no child: %s", describe(l.c.received(parent)))
		}
		var childCreated struct {
			Child *store.Child `json:"child"`
		}
		for _, m := range l.c.received(started.SessionID) {
			if m.Method == "session/created" {
				json.Unmarshal(m.Params, &childCreated)
			}
		}
		if childCreated.Child == nil || childCreated.Child.Directory != childDir || childCreated.Child.ParentID != parent {
			l.t.Errorf("session/created carried %+v", childCreated.Child)
		}
		if u := l.c.update(started.SessionID, "prompt"); u.From != parent {
			l.t.Errorf("the child took a prompt from %q, not %q", u.From, parent)
		}
		if u := l.c.update(started.SessionID, "result"); u.Result.IsError {
			l.t.Errorf("the child's turn failed: %s", u.Result.Text)
		}
		// The grandchild is published before create_session answers the child.
		var leaf string
		for _, m := range l.c.created() {
			var created struct {
				Child *store.Child `json:"child"`
			}
			json.Unmarshal(m.Params, &created)
			if created.Child != nil && created.Child.ParentID == started.SessionID {
				leaf = m.sessionID()
			}
		}
		if leaf == "" {
			l.t.Fatalf("the child started no child: %s", describe(l.c.received(started.SessionID)))
		}
		l.idle(started.SessionID)
		l.idle(leaf)
		l.c.call("session/destroy", map[string]string{"session_id": leaf})
		l.c.call("session/destroy", map[string]string{"session_id": started.SessionID})
	})

	section("child reports", func(l *live) {
		// A child's final reply reaches its parent as a report, unless the
		// child's last step before it was a message to the parent. Each case
		// has its own parent, and Haiku may add turns of its own, so each
		// checks only the reports of its child.
		const noAnswer = "Hello, nothing to answer."
		// child has a new parent start a child with message and returns both.
		child := func(name, message string) (string, string) {
			l.t.Helper()
			parent := l.create("manual")
			tools := l.turn(parent, "Load and call the mcp__ox__create_session tool with name "+name+", title "+name+
				`, and message "`+message+`" Then reply with just ok.`, l.refuse)
			if !slices.Contains(tools, "mcp__ox__create_session") {
				l.t.Fatalf("Haiku did not call create_session (%v); run again: %s", tools, describe(l.c.received(parent)))
			}
			for _, m := range l.c.created() {
				var created struct {
					Child *store.Child `json:"child"`
				}
				json.Unmarshal(m.Params, &created)
				if created.Child != nil && created.Child.ParentID == parent && created.Child.Name == name {
					return parent, m.sessionID()
				}
			}
			l.t.Fatalf("create_session started no child %s: %s", name, describe(l.c.received(parent)))
			return "", ""
		}
		// reportsUntil consumes parent's updates until it takes a report from
		// child with text, and returns every report it took from child.
		reportsUntil := func(parent, child, text string) []string {
			l.t.Helper()
			var reports []string
			for {
				u := l.c.update(parent, agent.UpdatePrompt)
				for _, r := range u.Reports {
					if r.From == child {
						reports = append(reports, r.Text)
					}
				}
				if slices.Contains(reports, text) {
					return reports
				}
			}
		}
		// told has a new parent start a child that messages it, then calls
		// the tools in between, then replies, and returns the parent, the
		// child, and the child's reply once it is idle.
		told := func(name, between string) (string, string, string) {
			l.t.Helper()
			parent, id := child(name, "Step 1: load and call the mcp__ox__send_message tool with to set to your parent session, "+
				"and text '"+noAnswer+"'. "+between+"Then reply with just the word done, without calling any other tool.")
			reply := l.c.update(id, agent.UpdateResult).Result.Text
			l.idle(id)
			var got struct {
				History []agent.Update `json:"history"`
			}
			json.Unmarshal(l.c.call("session/get", map[string]string{"session_id": parent}), &got)
			if !slices.ContainsFunc(got.History, func(u agent.Update) bool { return u.Type == agent.UpdatePrompt && u.From == id }) {
				l.t.Fatalf("the child sent its parent no message; run again: %s", describe(l.c.received(id)))
			}
			return parent, id, reply
		}

		parent, done := child("done", "Reply with just the word done, without calling any tool.")
		reply := l.c.update(done, agent.UpdateResult).Result.Text
		if reports := reportsUntil(parent, done, reply); len(reports) != 1 {
			l.t.Errorf("the parent took reports %q before the child's reply %q", reports, reply)
		}

		parent, quiet, reply := told("quiet", "")
		l.turn(quiet, "Reply with just the word again, without calling any tool.", l.refuse)
		again := l.c.update(quiet, agent.UpdateResult).Result.Text
		if reports := reportsUntil(parent, quiet, again); slices.Contains(reports, reply) {
			l.t.Errorf("a child that told its parent in its last step reported %q", reply)
		}

		parent, later, reply := told("later", "Step 2: after send_message returns, load and call the mcp__ox__list_sessions tool. ")
		// The list_sessions call is a later step: its tool_use follows the
		// message's tool_result. Calls in one step would send no report.
		sendID, answered, stepped := "", false, false
		for _, m := range l.c.received(later) {
			var u agent.Update
			json.Unmarshal(m.Params, &u)
			switch {
			case u.Type == agent.UpdateToolUse && u.ToolName == "mcp__ox__send_message":
				sendID = u.ToolUseID
			case u.Type == agent.UpdateToolResult && sendID != "" && u.ToolUseID == sendID:
				answered = true
			case u.Type == agent.UpdateToolUse && u.ToolName == "mcp__ox__list_sessions":
				stepped = answered
			}
		}
		if !stepped {
			l.t.Fatalf("Haiku did not call list_sessions in a step after send_message; run again: %s", describe(l.c.received(later)))
		}
		reportsUntil(parent, later, reply)
	})

	section("no subagents", func(l *live) {
		// Claude has no subagent tool; Ox's child sessions take its place.
		session := l.create("manual")
		tools := l.turn(session, "Load and call the Agent tool to start a general-purpose subagent with the prompt "+
			`"Reply with just the word ok." Then reply with just ok.`, l.refuse)
		if slices.Contains(tools, "Agent") || slices.Contains(tools, "Task") {
			l.t.Errorf("Claude started a subagent: %v", tools)
		}
	})

	section("parking", func(l *live) {
		// Pending schedules keep a session running, even after a failed turn.
		var denied bool
		failed := l.create("manual")
		tools := l.turn(failed, "Step 1: load and call the CronCreate tool with cron '*/30 * * * *' and prompt 'say hi'. "+
			"Step 2: only after CronCreate succeeds, run the Bash command `touch marker.txt`. Do both steps.", denyBash(&denied))
		if !slices.Contains(tools, "CronCreate") || !slices.Contains(tools, "Bash") {
			l.t.Fatalf("Haiku did not call CronCreate and then Bash (%v); run again", tools)
		}
		if !denied {
			l.t.Fatal("Bash did not ask for approval; no interrupting denial was sent")
		}
		completed := l.create("manual")
		tools = l.turn(completed, "Use the CronCreate tool to create a recurring job with cron '*/30 * * * *' and prompt 'say hi'. "+
			"Then reply with just the word ok.", allow)
		if !slices.Contains(tools, "CronCreate") {
			l.t.Fatalf("Haiku did not call CronCreate (%v); run again", tools)
		}
		// The control, with nothing scheduled, became idle last, so once it
		// parks the others have been idle for the whole park period.
		control := l.create("manual")
		l.turn(control, "Reply with just the word ok.", allow)
		l.c.update(control, "parked")
		if l.parked(failed) {
			l.t.Error("a failed turn that created a cron let its session park")
		}
		if l.parked(completed) {
			l.t.Error("a completed turn that created a cron let its session park")
		}
	})

	section("restored schedules", func(l *live) {
		// A resumed session keeps the schedules --resume restored, through an
		// interrupted first turn and after its next completed one.
		scheduled, control := l.create("manual"), l.create("manual")
		tools := l.turn(scheduled, "Use the CronCreate tool to create a recurring job with cron '*/30 * * * *' and prompt 'say hi'. "+
			"Then reply with just the word ok.", allow)
		if !slices.Contains(tools, "CronCreate") {
			l.t.Fatalf("Haiku did not call CronCreate (%v); run again", tools)
		}
		l.turn(control, "Reply with just the word ok.", allow)
		l = restart(l, 2*time.Second)

		// Deny the first tool on resume so Claude sends no Stop schedule report.
		var denied bool
		l.turn(scheduled, "Run the Bash command `touch resumed.txt` now.", denyBash(&denied))
		if !denied {
			l.t.Fatal("the first turn on resume did not reach an interrupting denial")
		}
		l.turn(control, "Reply with just the word ok.", allow)
		l.c.update(control, "parked")
		if l.parked(scheduled) {
			l.t.Error("the interrupted first turn let the resumed session park")
		}

		// The next completed turn's Stop report lists the restored job.
		l.turn(scheduled, "Reply with just the word ok.", allow)
		l.turn(control, "Reply with just the word ok.", allow)
		l.c.update(control, "parked")
		if l.parked(scheduled) {
			l.t.Error("a completed turn on resume let a session with a restored job park")
		}
	})

	section("reasoning", func(l *live) {
		var created struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(l.c.call("session/create", map[string]string{
			"workspace_id": l.workspace, "harness": "claude", "model": haiku, "effort": "high", "permission_mode": "manual",
		}), &created)
		id := created.SessionID
		l.c.call("session/send", map[string]string{"session_id": id, "text": "Without any tool, work out 17 * 23 + 19 * 29 step by step, then reply with just the number."})
		reasoned := false
		for {
			var u agent.Update
			l.c.next(func(m message) bool {
				if m.Method != "session/update" || m.sessionID() != id {
					return false
				}
				u = agent.Update{}
				json.Unmarshal(m.Params, &u)
				return u.Type == "reasoning_delta" || u.Type == "result"
			})
			if u.Type == "result" {
				break
			}
			reasoned = reasoned || u.Text != ""
		}
		if !reasoned {
			l.t.Errorf("Haiku at effort high streamed no reasoning before its result: %s", describe(l.c.received(id)))
		}
		l.idle(id)
	})
}

// TestLiveUr chats with the built-in Ur model through OpenRouter, parks the
// session between two turns that share the conversation, runs an approved
// shell command, and interrupts a running one.
func TestLiveUr(t *testing.T) {
	if os.Getenv("OX_LIVE") == "" {
		t.Skip("set OX_LIVE=1 to run against OpenRouter, as make live does")
	}
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		t.Fatal("OPENROUTER_API_KEY is not set; make live loads it from .env")
	}
	cfg, err := config.Load(filepath.Join(t.TempDir(), "none.json")) // the built-in configuration
	if err != nil {
		t.Fatal(err)
	}
	harnesses := map[string]agent.Harness{"ur": ur.Harness(ur.Endpoint, t.TempDir(), cfg.Harnesses["ur"].Providers())}
	s, err := New(openStore(t), cfg, time.Second, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	c.timeout = time.Minute
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	dir := t.TempDir()
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": dir}), &workspace)
	var created struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "ur", "model": "deepseek/deepseek-v4.1-flash-1017", "effort": "low", "permission_mode": "manual",
	}), &created)
	id := created.SessionID
	turn := func(text string) string {
		t.Helper()
		c.forget(id)
		c.call("session/send", map[string]string{"session_id": id, "text": text})
		c.update(id, "text_delta")
		result := c.update(id, "result").Result
		if result.IsError {
			t.Fatalf("%q failed: %s", text, result.Text)
		}
		for c.update(id, "state").State != "idle" {
		}
		return result.Text
	}
	turn("Remember the number 7341. Reply with just the word ok.")
	c.update(id, "parked")
	if reply := turn("What number did I ask you to remember? Reply with just the number."); !strings.Contains(reply, "7341") {
		t.Errorf("the second turn forgot the first: %q", reply)
	}

	// In manual mode the command waits for approval, then runs. The prompt
	// needs some thought, so the model often reasons before its call.
	c.forget(id)
	c.call("session/send", map[string]string{"session_id": id, "text": "Work out which of 391, 403, and 409 is prime, " +
		"then use the shell tool to run `echo ox-<that number>`, then reply with its output."})
	approval := c.update(id, "approval")
	var asked struct {
		ToolName string `json:"tool_name"`
	}
	if json.Unmarshal(approval.Request, &asked); asked.ToolName != "shell" {
		t.Fatalf("approval request %s", approval.Request)
	}
	c.call("session/approve", map[string]any{"session_id": id, "request_id": approval.RequestID, "response": json.RawMessage(`{"behavior":"allow"}`)})
	if ran := c.update(id, "tool_result").Result; ran.IsError || !strings.Contains(ran.Text, "ox-409") {
		t.Errorf("the command's result %+v", ran)
	}
	// When the model reasoned before its call, the follow-up request returned
	// that reasoning, and the turn's success shows OpenRouter accepted it.
	if result := c.update(id, "result").Result; result.IsError {
		t.Fatalf("the shell turn failed: %s", result.Text)
	}
	for c.update(id, "state").State != "idle" {
	}
	reasoned := false
	for _, m := range c.received(id) {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		if u.Type == "tool_use" {
			break
		}
		reasoned = reasoned || (u.Type == "reasoning_delta" && u.Text != "")
	}
	if !reasoned {
		t.Log("the model did not reason before its call, so its follow-up request returned no reasoning")
	}

	// In auto mode the command runs at once, and an interrupt ends it.
	c.call("session/configure", map[string]string{"session_id": id, "permission_mode": "auto"})
	c.forget(id)
	c.call("session/send", map[string]string{"session_id": id, "text": "Use the shell tool to run exactly `echo $$ > running; sleep 30`."})
	c.update(id, "tool_use")
	pidFile := filepath.Join(dir, "running")
	var pid int
	for deadline := time.Now().Add(30 * time.Second); pid == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the command never started")
		}
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
	}
	interrupted := time.Now()
	c.call("session/interrupt", map[string]string{"session_id": id})
	if result := c.update(id, "result").Result; !result.IsError || result.Text != "Interrupted" {
		t.Errorf("the interrupted turn's result %+v", result)
	}
	for c.update(id, "state").State != "idle" {
	}
	if elapsed := time.Since(interrupted); elapsed > 10*time.Second {
		t.Errorf("the interrupt took %s", elapsed)
	}
	// The interrupt killed the command's process group.
	for deadline := time.Now().Add(5 * time.Second); !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the command's shell %d survived the interrupt", pid)
		}
	}
	// The next request answers the stopped call, or the provider refuses it.
	turn("Reply with just the word ok.")
}
