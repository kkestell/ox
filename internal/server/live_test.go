package server

import (
	"crypto/rand"
	"encoding/json"
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
	"github.com/kkestell/ox/internal/store"
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
	s, err := New(st, cfg, parkAfter, map[string]agent.Harness{"claude": claude.Harness}, mcp)
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
	restart := func(park time.Duration) {
		t.Helper()
		s.Close()
		listener.Close()
		restarted, err := New(st, cfg, park, map[string]agent.Harness{"claude": claude.Harness}, mcp)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(restarted.Close)
		s, listener = restarted, serve(restarted)
		c = connect(t, s)
		c.timeout = 3 * time.Minute
	}

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

	resumeToken := func(id string) string {
		t.Helper()
		workspaces, err := st.Workspaces()
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range workspaces {
			for _, record := range w.Sessions {
				if record.ID == id {
					return record.ResumeToken
				}
			}
		}
		t.Fatalf("session %s not found in the database", id)
		return ""
	}

	t.Log("a failed start reports Claude's reason")
	var lostSend struct {
		MessageID string `json:"message_id"`
	}
	json.Unmarshal(c.call("session/send", map[string]string{"session_id": lost, "text": "Reply with just the word ok."}), &lostSend)
	if u := c.update(lost, "prompt_cancelled"); u.MessageID != lostSend.MessageID {
		t.Errorf("resuming a missing conversation did not drop the prompt: %+v", u)
	}
	if u := c.update(lost, "exited"); !strings.Contains(u.Error, "could not resume the earlier conversation") {
		t.Errorf("resuming a missing conversation did not say so: %+v", u)
	}
	c.update(lost, "parked")
	if token := resumeToken(lost); token != "" {
		t.Errorf("the missing conversation's token was kept: %q", token)
	}
	misspelled := create("acceptEdit")
	c.call("session/send", map[string]string{"session_id": misspelled, "text": "Reply with just the word ok."})
	if u := c.update(misspelled, "exited"); !strings.Contains(u.Error, "'acceptEdit' is invalid") {
		t.Errorf("a start Claude refused on stderr failed without its reason: %+v", u)
	} else {
		t.Logf("exited: %s", u.Error)
	}

	t.Log("a conversation resumes at once after its process is stopped")
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
		OnError:      func(err error) { t.Log(err) },
	}
	// ask sends text to a and handles its events until Claude is idle, and
	// returns the turn's result.
	ask := func(a agent.Agent, text string) string {
		t.Helper()
		id := rand.Text()
		if err := a.Send(agent.Input{MessageID: id, Text: text, Content: text}); err != nil {
			t.Fatal(err)
		}
		timeout := time.After(3 * time.Minute)
		var reply string
		for seen := len(direct); ; {
			select {
			case e := <-a.Events():
				a.Handle(e)
			case <-timeout:
				t.Fatal("Claude did not finish the turn")
			}
			for ; seen < len(direct); seen++ {
				switch u := direct[seen]; {
				case u.Type == agent.UpdateResult:
					reply = u.Result.Text
				case u.Type == agent.UpdateExited:
					t.Fatalf("Claude exited: %s", u.Error)
				case u.Type == agent.UpdateState && u.State == agent.StateIdle && u.MessageID == id:
					return reply
				}
			}
		}
	}
	first, err := claude.Start(options)
	if err != nil {
		t.Fatal(err)
	}
	ask(first, "Remember the word heliotrope. Reply with just ok.")
	if conversation == "" {
		t.Fatal("the conversation was not resumable")
	}
	first.Stop()
	exiting := exec.Command("pgrep", "-f", "session-id="+conversation).Run() == nil
	options.Resume = conversation
	second, err := claude.Start(options)
	if err != nil {
		t.Fatal(err)
	}
	if reply := ask(second, "What word did I ask you to remember? Reply with just the word."); !strings.Contains(strings.ToLower(reply), "heliotrope") {
		t.Errorf("the resumed conversation answered %q", reply)
	}
	second.Stop()
	if !exiting {
		t.Errorf("the stopped process had exited before the next one started")
	}

	t.Log("the first approval can be pending when the daemon restarts")
	pendingID := create("manual")
	const marker = "unfinished-ox-test-49"
	c.call("session/send", map[string]string{"session_id": pendingID, "text": "Use Bash to run `touch " + marker + "`."})
	if approval := c.update(pendingID, "approval"); !strings.Contains(string(approval.Request), marker) {
		t.Fatalf("unexpected approval: %s", approval.Request)
	}
	pendingToken := resumeToken(pendingID)
	if pendingToken == "" {
		t.Fatal("the first prompt left no resume token while waiting on an approval")
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		configDir = filepath.Join(home, ".claude")
	}
	files, err := filepath.Glob(filepath.Join(configDir, "projects", "*", pendingToken+".jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("no transcript for %s: %v, %v", pendingToken, files, err)
	}
	transcript, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(transcript), "Use Bash to run `touch "+marker+"`.") {
		t.Fatalf("Claude did not write the first prompt before the approval: %v", err)
	}

	t.Log("/clear replaces the resume token even with an empty result")
	cleared := create("manual")
	turn(cleared, "Reply with just the word ok.", allow)
	old := resumeToken(cleared)
	turn(cleared, "/clear", allow)
	clearedToken := resumeToken(cleared)
	if old == "" || clearedToken == "" || clearedToken == old {
		t.Fatalf("/clear did not replace the resume token: %q -> %q", old, clearedToken)
	}

	restart(parkAfter)
	turn(pendingID, "What exact filename did my first prompt ask to create? Reply with just the filename; do not call tools.", allow)
	if r := c.update(pendingID, "result").Result; r.IsError || !strings.Contains(r.Text, marker) || resumeToken(pendingID) != pendingToken {
		t.Fatalf("the reply did not resume the unfinished conversation: %+v", r)
	}
	turn(cleared, "Reply with just the word ok.", allow)
	if r := c.update(cleared, "result").Result; r.IsError || resumeToken(cleared) != clearedToken {
		t.Fatalf("the cleared conversation did not resume: %+v", r)
	}

	t.Log("/cost output is visible as reply text even without deltas from Claude")
	turn(cleared, "/cost", allow)
	delta, result := c.update(cleared, "text_delta"), c.update(cleared, "result")
	if result.Result.IsError || delta.Text == "" || delta.Text != result.Result.Text || delta.MessageID != result.MessageID {
		t.Fatalf("local command output was not published as the reply: %+v, %+v", delta, result)
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

	t.Log("Claude lists sessions and messages a parked one")
	describe := func(messages []message) string {
		var b strings.Builder
		for _, m := range messages {
			b.Write(m.Params)
			b.WriteString("\n")
		}
		return b.String()
	}
	// message has a session in mode list the sessions and message a new
	// parked one, without asking, and the recipient starts and takes it.
	message := func(mode string) {
		t.Helper()
		from, to := create(mode), create("manual")
		tools := turn(from, "Do both steps. Step 1: load and call the mcp__ox__list_sessions tool. "+
			"Step 2: load and call the mcp__ox__send_message tool with to set to "+to+
			` and text "Reply with just the word ok, without calling any tool." Then reply with just ok.`, refuse)
		if !slices.Contains(tools, "mcp__ox__list_sessions") || !slices.Contains(tools, "mcp__ox__send_message") {
			t.Fatalf("Haiku did not call list_sessions and send_message (%v); run again: %s", tools, describe(c.received(from)))
		}
		listedRecipient := false
		for _, m := range c.received(from) {
			var u agent.Update
			json.Unmarshal(m.Params, &u)
			if u.Type == "tool_result" && !u.Result.IsError && strings.Contains(u.Result.Text, `"session_id":"`+to+`"`) {
				listedRecipient = true
			}
		}
		if !listedRecipient {
			t.Errorf("list_sessions did not list the recipient: %v", c.received(from))
		}
		if u := c.update(to, "prompt"); u.From != from {
			t.Errorf("the recipient took a prompt from %q, not %q", u.From, from)
		}
		idle(to)
	}
	message("manual")
	message("auto")

	t.Log("Claude reads another session's long transcript without asking, in plan mode")
	const endMarker = "END-MARKER-7731"
	long := create("manual")
	turn(long, strings.Repeat("lorem ipsum dolor sit amet ", 1420)+"The marker is "+endMarker+". Reply with just ok.", refuse)
	reader := create("plan")
	tools = turn(reader, "Load and call the mcp__ox__read_session tool with to set to "+long+
		`. Then reply with just the marker that ends the user prompt in the transcript, the word after "The marker is".`, refuse)
	if !slices.Contains(tools, "mcp__ox__read_session") {
		t.Fatalf("Haiku did not call read_session (%v); run again: %s", tools, describe(c.received(reader)))
	}
	read, replied := false, false
	for _, m := range c.received(reader) {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		switch {
		case u.Type == "result":
			replied = replied || strings.Contains(u.Result.Text, endMarker)
		case u.Type == "tool_result" && !u.Result.IsError && strings.Contains(u.Result.Text, endMarker) && len(u.Result.Text) >= 38000:
			read = true
			t.Logf("read_session's result reached Claude as %d characters", len(u.Result.Text))
		}
	}
	if !read {
		t.Errorf("read_session did not return the whole transcript: %v", c.received(reader))
	}
	if !replied {
		t.Errorf("Claude did not reply with the end of the transcript: %v", c.received(reader))
	}

	t.Log("asked in plain words, Claude messages another session through Ox")
	from, to := create("acceptEdits"), create("manual")
	tools = turn(from, "Exchange a message with the other agent, session "+to+
		": ask it to reply with just the word ok, without calling any tool. Then reply with just ok.", refuse)
	if slices.Contains(tools, "SendMessage") || slices.Contains(tools, "ListAgents") {
		t.Fatalf("Claude used its own messaging tools: %v", tools)
	}
	if !slices.Contains(tools, "mcp__ox__send_message") {
		t.Fatalf("Haiku called no messaging tool (%v); run again: %s", tools, describe(c.received(from)))
	}
	if u := c.update(to, "prompt"); u.From != from {
		t.Errorf("the recipient took a prompt from %q, not %q", u.From, from)
	}
	idle(to)
	tools = turn(from, "Load and call the ListAgents tool. Then reply with just ok.", refuse)
	if slices.Contains(tools, "ListAgents") {
		t.Errorf("Claude called ListAgents: %v", tools)
	}

	t.Log("in plan mode, a message asks first")
	// Haiku in plan mode usually declines to call a tool that is not
	// read-only; when it does call send_message, the call asks first.
	planner, recipient := create("plan"), create("manual")
	askedToSend := false
	tools = turn(planner, "Load and call the mcp__ox__send_message tool with to set to "+recipient+
		` and text "Reply with just the word ok." Then reply with just ok.`, func(r request) any {
		if r.ToolName != "mcp__ox__send_message" {
			t.Errorf("%s asked for approval in plan mode", r.ToolName)
		}
		askedToSend = true
		return allow(r)
	})
	switch {
	case askedToSend:
		if u := c.update(recipient, "prompt"); u.From != planner {
			t.Errorf("the recipient took a prompt from %q, not %q", u.From, planner)
		}
		idle(recipient)
	case slices.Contains(tools, "mcp__ox__send_message"):
		t.Error("send_message ran without asking in plan mode")
	default:
		t.Logf("Haiku declined to call send_message in plan mode (%v)", tools)
		for _, m := range c.received(recipient) {
			if m.Method == "session/update" {
				t.Errorf("a message reached the recipient without approval: %s", m.Params)
			}
		}
	}

	t.Log("a refused message reaches Claude as a tool error")
	unknown := create("manual")
	tools = turn(unknown, `Load and call the mcp__ox__send_message tool with to set to "no-such-session" and text "hi". Then reply with just ok.`, refuse)
	if !slices.Contains(tools, "mcp__ox__send_message") {
		t.Fatalf("Haiku did not call send_message (%v); run again", tools)
	}
	refused := false
	for _, m := range c.received(unknown) {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		if u.Type == "tool_result" && u.Result.IsError && strings.Contains(u.Result.Text, "session not found") {
			refused = true
		}
	}
	if !refused {
		t.Errorf("the refusal did not reach Claude as an error: %v", c.received(unknown))
	}

	t.Log("Claude starts a child session without asking")
	parent, childDir := create("manual"), t.TempDir()
	tools = turn(parent, "Load and call the mcp__ox__create_session tool with name helper, title Helper, dir "+childDir+
		`, and message "Reply with just the word ok, without calling any tool." Then reply with just ok.`, refuse)
	if !slices.Contains(tools, "mcp__ox__create_session") {
		t.Fatalf("Haiku did not call create_session (%v); run again: %s", tools, describe(c.received(parent)))
	}
	var started struct {
		SessionID string `json:"session_id"`
	}
	for _, m := range c.received(parent) {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		if u.Type == "tool_result" && !u.Result.IsError && strings.Contains(u.Result.Text, `"session_id"`) {
			json.Unmarshal([]byte(u.Result.Text), &started)
		}
	}
	if started.SessionID == "" {
		t.Fatalf("create_session returned no child: %s", describe(c.received(parent)))
	}
	var childCreated struct {
		Child *child `json:"child"`
	}
	for _, m := range c.received(started.SessionID) {
		if m.Method == "session/created" {
			json.Unmarshal(m.Params, &childCreated)
		}
	}
	if childCreated.Child == nil || childCreated.Child.Directory != childDir || childCreated.Child.ParentID != parent {
		t.Errorf("session/created carried %+v", childCreated.Child)
	}
	if u := c.update(started.SessionID, "prompt"); u.From != parent {
		t.Errorf("the child took a prompt from %q, not %q", u.From, parent)
	}
	if u := c.update(started.SessionID, "result"); u.Result.IsError {
		t.Errorf("the child's turn failed: %s", u.Result.Text)
	}
	idle(started.SessionID)
	c.call("session/destroy", map[string]string{"session_id": started.SessionID})

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
	restart(2 * time.Second)
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
