package claude

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
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
	"github.com/kkestell/ox/internal/children"
	"github.com/kkestell/ox/internal/process"
)

// startLog records what a starting agent reports.
type startLog struct {
	updates recorder
	errors  []error
	tokens  []string // each token passed to OnResumable
}

func (l *startLog) options() agent.Options {
	return agent.Options{
		OnUpdate: l.updates.record, OnConfigured: func(agent.Settings) {},
		OnError:     func(err error) { l.errors = append(l.errors, err) },
		OnResumable: func(token string) { l.tokens = append(l.tokens, token) },
	}
}

// failingStart returns a starting agent holding a prompt, with its ID and
// what the agent reports after the prompt's acceptance. Its process waits to
// be stopped, which the test's cleanup does.
func failingStart(t *testing.T) (*Agent, string, *startLog) {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	events, done := make(chan agent.Event), make(chan struct{})
	proc, _, err := process.Start(cmd, "claude", "ox-session", syscall.SIGKILL, parse, events, done)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	log := new(startLog)
	a := newAgent(newWriter(io.Discard), events, done, proc, log.options())
	id, err := sendTestPrompt(a, "hello")
	if err != nil {
		t.Fatal(err)
	}
	log.updates = nil
	return a, id, log
}

// dropped checks that the start ended, dropping the prompt id and reporting
// reason in an exited update that names it.
func (l *startLog) dropped(t *testing.T, a agent.Agent, id, reason string) {
	t.Helper()
	u := l.updates
	if !a.Exited() || len(u) != 2 || !cancelled(u[:1], []string{id}) ||
		u[1].Type != "exited" || u[1].MessageID != id || u[1].Error != reason {
		t.Fatalf("the start did not drop the prompt and report %q: %+v", reason, u)
	}
}

// handleUntilEnded handles a's events until it ends.
func handleUntilEnded(t *testing.T, a agent.Agent, within time.Duration) {
	t.Helper()
	timeout := time.After(within)
	for !a.Exited() {
		select {
		case e := <-a.Events():
			a.Handle(e)
		case <-timeout:
			t.Fatalf("the start did not end within %v", within)
		}
	}
}

// Claude reports a startup failure as an error result or plain text before
// it exits, sometimes before reading initialize, or refuses initialize. The
// prompt that started it is dropped with Claude's reason. When Claude no
// longer has the conversation to resume, the reason is the lost
// conversation, and the agent gives up the conversation.
func TestStartupFailureKeepsClaudesReason(t *testing.T) {
	const missing = "No conversation found with session ID: 1111"
	gone := message{Type: "result", Subtype: "error_during_execution", IsError: true, Errors: []string{missing}}
	exit := process.Exit{Err: errors.New("exit status 1")}
	for name, c := range map[string]struct {
		output []agent.Event
		reason string
	}{
		"error result": {[]agent.Event{gone, exit}, lostConversation},
		"error result before initialize response": {[]agent.Event{gone, initialized}, lostConversation},
		"other error result": {[]agent.Event{
			message{Type: "result", Subtype: "error_during_execution", IsError: true, Errors: []string{"API Error: Connection refused"}}, exit,
		}, "API Error: Connection refused"},
		"plain text":                    {[]agent.Event{plainText(missing), exit}, missing},
		"plain text after error result": {[]agent.Event{gone, plainText("Not logged in"), exit}, "Not logged in"},
		"startup timeout after plain text": {[]agent.Event{plainText("Not logged in · Please run /login"), startupTimeout{}},
			"no startup response within one minute\nNot logged in · Please run /login"},
		"startup timeout after lost conversation": {[]agent.Event{gone, startupTimeout{}}, lostConversation},
		"stderr":    {[]agent.Event{process.Exit{Err: exit.Err, Stderr: "error: bad option"}}, "error: bad option"},
		"no reason": {[]agent.Event{exit}, "Claude exited: exit status 1"},
		"refused initialize": {[]agent.Event{
			message{Type: "control_response", Response: controlReply{Subtype: "error", RequestID: "initialize", Error: "bad flags"}},
		}, "Claude refused initialization (error): bad flags"},
	} {
		t.Run(name, func(t *testing.T) {
			a, id, log := failingStart(t)
			for _, e := range c.output {
				a.Handle(e)
			}
			log.dropped(t, a, id, c.reason)
			lost := c.reason == lostConversation
			if lost != (len(log.tokens) == 1 && log.tokens[0] == "") || !lost && len(log.tokens) != 0 {
				t.Fatalf("only Claude's documented result gives up the conversation: %q", log.tokens)
			}
			if lost && (len(log.errors) != 1 || !strings.Contains(log.errors[0].Error(), missing)) {
				t.Fatalf("Claude's reason for the lost conversation was not logged: %v", log.errors)
			}
		})
	}

	// Claude's own stdout, read from spawn to exit.
	for name, c := range map[string]struct{ printf, reason string }{
		"line at EOF":           {missing, missing},
		"blank line after text": {`Not logged in\n\n`, "Not logged in"},
	} {
		t.Run(name, func(t *testing.T) {
			script(t, "#!/bin/sh\nprintf '"+c.printf+"'\nexit 1\n")
			log := new(startLog)
			options := log.options()
			options.Workspace = t.TempDir()
			a, err := Start(options)
			if err != nil {
				t.Fatal(err)
			}
			id, err := sendTestPrompt(a.(*Agent), "hello")
			if err != nil {
				t.Fatal(err)
			}
			log.updates = nil
			handleUntilEnded(t, a, 5*time.Second)
			log.dropped(t, a, id, c.reason)
			if len(log.tokens) != 0 {
				t.Fatalf("plain text gave up the conversation: %q", log.tokens)
			}
		})
	}
}

// A start that Claude neither answers nor fails ends after the startup
// limit, and its process is asked to exit.
func TestStartupLimit(t *testing.T) {
	saved := startupLimit
	startupLimit = 200 * time.Millisecond
	t.Cleanup(func() { startupLimit = saved })
	script(t, "#!/bin/sh\nexec sleep 60\n")
	log := new(startLog)
	options := log.options()
	options.Workspace = t.TempDir()
	a, err := Start(options)
	if err != nil {
		t.Fatal(err)
	}
	id, err := sendTestPrompt(a.(*Agent), "hello")
	if err != nil {
		t.Fatal(err)
	}
	log.updates = nil
	handleUntilEnded(t, a, 5*time.Second)
	log.dropped(t, a, id, "no startup response within one minute")
	waited := make(chan struct{})
	go func() {
		children.WaitAll()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		children.KillAll()
		t.Fatal("Claude was not asked to exit after the startup limit")
	}
}

// A child that inherits Claude's stdout keeps the pipe open after Claude
// exits. Claude's last line, here without a newline, still arrives first.
func TestExitReportedWhileAChildHoldsStdout(t *testing.T) {
	pid := filepath.Join(t.TempDir(), "pid")
	standIn(t, "sleep 15 2>/dev/null &\necho $! > "+pid+"\nprintf '{\"type\":\"result\"}'\nexit 3\n")
	t.Cleanup(func() {
		if data, err := os.ReadFile(pid); err == nil {
			exec.Command("kill", strings.TrimSpace(string(data))).Run()
		}
	})
	events := make(chan agent.Event)
	if _, _, err := startProcess(t.TempDir(), "ox-session", newUUID(), false, agent.Settings{}, agent.MCPServer{}, events, nil); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	var result bool
	for {
		select {
		case event := <-events:
			if m, ok := event.(message); ok && m.Type == "result" {
				result = true
			}
			if exit, ok := event.(process.Exit); ok {
				if !result {
					t.Fatal("Claude's last line was dropped")
				}
				if exit.Err == nil || !strings.Contains(exit.Err.Error(), "exit status 3") {
					t.Fatalf("unexpected exit: %v", exit.Err)
				}
				return
			}
		case <-timeout:
			t.Fatal("Claude's exit was not reported while its child held stdout")
		}
	}
}

// The session cannot read events while it writes a prompt. Claude may write
// more than a pipe holds before reading the prompt, then exit while the
// session is still busy. Every buffered event must arrive before the exit.
func TestOutputDrainsBeforeSlowDelivery(t *testing.T) {
	standIn(t, `
i=0
while [ "$i" -lt 256 ]; do
  printf '{"type":"%s","result":"%02048d"}\n' "$i" 0
  i=$((i + 1))
done
read prompt
printf '%s\n' '{"type":"result"}'
printf '%s\n' 'Claude diagnostic' >&2
exit 3
`)
	events := make(chan agent.Event)
	p, in, err := startProcess(t.TempDir(), "ox-session", newUUID(), false, agent.Settings{}, agent.MCPServer{}, events, nil)
	if err != nil {
		t.Fatal(err)
	}
	drained := false
	t.Cleanup(func() {
		if !drained {
			p.Terminate(0)
			go func() {
				for e := range events {
					if _, ok := e.(process.Exit); ok {
						return
					}
				}
			}()
		}
	})
	sent := make(chan error, 1)
	go func() { sent <- in.Prompt(newUUID(), strings.Repeat("x", 256<<10), nil) }()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt write stalled behind unread Claude stdout")
	}
	select {
	case <-p.Reaped():
	case <-time.After(5 * time.Second):
		t.Fatal("Claude could not exit with unread output")
	}
	time.Sleep(1100 * time.Millisecond) // let the pipe deadline pass before accepting delivery
	timeout := time.After(5 * time.Second)
	if m, ok := (<-events).(message); !ok || m.Type != "control_response" {
		t.Fatalf("the initialize response did not arrive first: %+v", m)
	}
	for i := range 258 {
		select {
		case e := <-events:
			if _, ok := e.(process.Exit); ok {
				drained = true
			}
			switch {
			case i < 256:
				if m, ok := e.(message); !ok || m.Type != strconv.Itoa(i) || len(m.Result) != 2048 {
					t.Fatalf("output was lost or reordered at %d", i)
				}
			case i == 256:
				if m, ok := e.(message); !ok || m.Type != "result" {
					t.Fatalf("the final result did not arrive: %+v", e)
				}
			default:
				if exit, ok := e.(process.Exit); !ok || exit.Err == nil || exit.Stderr != "Claude diagnostic" {
					t.Fatalf("the exit or its diagnostic did not arrive: %+v", e)
				}
			}
		case <-timeout:
			t.Fatalf("delivery stalled at output %d", i)
		}
	}
}

// Claude prints some refusals, such as an invalid option, only on stderr. A
// child that inherits stderr delays the reason by at most a second, and a
// reason longer than what is kept loses only its start.
func TestStartupFailureOnStderr(t *testing.T) {
	const refusal = "error: option '--permission-mode <mode>' argument '' is invalid."
	for name, before := range map[string]string{
		"exits":              "",
		"child holds stderr": "sleep 15 >/dev/null &\necho $! > pid\n",
		"long line":          "printf '%03000d' 0 >&2\n",
	} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			t.Cleanup(func() {
				if data, err := os.ReadFile(filepath.Join(workspace, "pid")); err == nil {
					exec.Command("kill", strings.TrimSpace(string(data))).Run()
				}
			})
			script(t, "#!/bin/sh\necho \"warning: a warning\" >&2\n"+before+"echo \""+refusal+"\" >&2\nexit 1\n")
			log := new(startLog)
			options := log.options()
			options.Workspace = workspace
			a, err := Start(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sendTestPrompt(a.(*Agent), "hello"); err != nil {
				t.Fatal(err)
			}
			handleUntilEnded(t, a, 3*time.Second)
			if exited := log.updates[len(log.updates)-1]; exited.Type != "exited" || !strings.Contains(exited.Error, refusal) {
				t.Fatalf("startup failure lost Claude's stderr: %+v", log.updates)
			}
		})
	}
}

// standIn puts a claude script on PATH that writes Claude's answer to
// initialize, without waiting for the request, and then runs rest.
func standIn(t *testing.T, rest string) {
	t.Helper()
	script(t, "#!/bin/sh\n"+
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'`+"\n"+rest)
}

// script puts a claude script on PATH.
func script(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Claude starts without a variable that runs it with another effort, model,
// provider, or account than Ox chooses, and keeps the user's subscription
// login.
func TestClaudeStartsWithOxsChoices(t *testing.T) {
	dropped := []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
		"CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", "ANTHROPIC_OX_TEST",
	}
	for _, name := range dropped {
		t.Setenv(name, "set")
	}
	kept := []string{"CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "OX_TEST_KEPT", "CLAUDE_CODE_EFFORT_LEVEL_X"}
	for _, name := range kept {
		t.Setenv(name, "kept")
	}
	env := filepath.Join(t.TempDir(), "env")
	standIn(t, "env > "+env+"\n")
	events := make(chan agent.Event, 1)
	if _, _, err := startProcess(t.TempDir(), "ox-session", newUUID(), false, agent.Settings{}, agent.MCPServer{}, events, nil); err != nil {
		t.Fatal(err)
	}
	for e := range events {
		if _, exited := e.(process.Exit); exited {
			break
		}
	}
	data, err := os.ReadFile(env)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	for _, name := range dropped {
		if slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, name+"=") }) {
			t.Errorf("Claude's environment holds %s:\n%s", name, data)
		}
	}
	for _, name := range kept {
		if !slices.Contains(lines, name+"=kept") {
			t.Errorf("Claude's environment lacks %s=kept:\n%s", name, data)
		}
	}
}

// Every Claude process, new or resumed, has Ox's MCP server, Ox's
// instructions, and no other messaging.
func TestClaudeRunsAsAnOxSession(t *testing.T) {
	mcp := agent.MCPServer{
		Command: "/x/ox-mcp", Env: map[string]string{"OX_SOCKET": "/s", "OX_SESSION_ID": "id"}, Approved: []string{"list_models"},
	}
	for _, resume := range []bool{false, true} {
		argv := filepath.Join(t.TempDir(), "argv")
		cwd := filepath.Join(t.TempDir(), "cwd")
		standIn(t, "pwd -P > "+cwd+"\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > "+argv+"\n")
		events := make(chan agent.Event, 1)
		workspace := t.TempDir()
		if _, _, err := startProcess(workspace, "ox-session", newUUID(), resume, agent.Settings{}, mcp, events, nil); err != nil {
			t.Fatal(err)
		}
		for e := range events {
			if _, exited := e.(process.Exit); exited {
				break
			}
		}
		data, err := os.ReadFile(cwd)
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.EvalSymlinks(workspace)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(data)); got != want {
			t.Errorf("resume %v: cwd %q, want %q", resume, got, want)
		}
		data, err = os.ReadFile(argv)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Split(string(data), "\x00")
		i := slices.Index(args, "--mcp-config")
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("resume %v: no --mcp-config in %q", resume, args)
		}
		var config struct {
			MCPServers map[string]struct {
				Command string            `json:"command"`
				Env     map[string]string `json:"env"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(args[i+1]), &config); err != nil {
			t.Fatalf("resume %v: --mcp-config %q: %v", resume, args[i+1], err)
		}
		ox := config.MCPServers["ox"]
		if ox.Command != mcp.Command || !maps.Equal(ox.Env, mcp.Env) {
			t.Errorf("resume %v: --mcp-config %s", resume, args[i+1])
		}
		if !slices.Contains(args, "--allowedTools=mcp__ox__list_models") {
			t.Errorf("resume %v: list_models is not pre-approved in %q", resume, args)
		}
		if !slices.Contains(args, "--thinking-display=summarized") {
			t.Errorf("resume %v: thinking is not displayed in %q", resume, args)
		}
		if !slices.Contains(args, "--disallowedTools=SendMessage,ListAgents,Agent,EnterWorktree,ExitWorktree") {
			t.Errorf("resume %v: built-in messaging, subagents, and worktrees are not disallowed in %q", resume, args)
		}
		var settings struct {
			CrossSessionInbound string `json:"crossSessionInbound"`
		}
		j := slices.IndexFunc(args, func(a string) bool { return strings.HasPrefix(a, "--settings=") })
		if j < 0 {
			t.Fatalf("resume %v: no --settings in %q", resume, args)
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(args[j], "--settings=")), &settings); err != nil || settings.CrossSessionInbound != "refuse" {
			t.Errorf("resume %v: %s", resume, args[j])
		}
		k := slices.Index(args, "--append-system-prompt")
		if k < 0 || k+1 >= len(args) {
			t.Fatalf("resume %v: no --append-system-prompt in %q", resume, args)
		}
		for _, want := range []string{"Lead with the big idea up front.", "You are Ox session `ox-session` in `" + workspace + "`"} {
			if !strings.Contains(args[k+1], want) {
				t.Errorf("resume %v: instructions lack %q: %s", resume, want, args[k+1])
			}
		}
	}
}

// Claude runs at its default effort, with only a warning, when given an
// effort it does not know.
func TestUnknownEffortFailsToStart(t *testing.T) {
	standIn(t, "exec sleep 15\n")
	for effort, known := range map[string]bool{"": true, "high": true, "hgih": false} {
		p, input, err := startProcess(t.TempDir(), "ox-session", newUUID(), false, agent.Settings{Effort: effort}, agent.MCPServer{}, make(chan agent.Event, 1), nil)
		if p != nil {
			input.w.(io.Closer).Close()
			p.Terminate(0)
		}
		if (err == nil) != known {
			t.Errorf("effort %q started: %v", effort, err)
		}
	}
}
