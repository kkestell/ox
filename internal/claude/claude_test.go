package claude

import (
	"bufio"
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
)

// Claude reports a startup failure as an error result or plain text before
// it exits, sometimes before reading initialize.
func TestStartupFailureKeepsClaudesReason(t *testing.T) {
	for name, output := range map[string]string{
		"error result": `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["No conversation found with session ID: 1111"]}` + "\n",
		"error result before initialize response": `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["No conversation found with session ID: 1111"]}` + "\n" +
			`{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}` + "\n",
		"plain text":  "No conversation found with session ID: 1111\n",
		"line at EOF": "No conversation found with session ID: 1111",
	} {
		t.Run(name, func(t *testing.T) {
			for _, input := range []io.Writer{io.Discard, closedPipe(t)} {
				err := initialize(input, bufio.NewReader(strings.NewReader(output)), func() string { return "" })
				if err == nil || !strings.Contains(err.Error(), "No conversation found with session ID: 1111") {
					t.Fatalf("startup failure lost Claude's reason: %v", err)
				}
				if errors.Is(err, agent.ErrNoConversation) != strings.HasPrefix(name, "error result") {
					t.Fatalf("only Claude's documented result means the conversation is gone: %v", err)
				}
			}
		})
	}
}

func closedPipe(t *testing.T) io.Writer {
	r, w := io.Pipe()
	r.Close()
	return w
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
	if _, _, err := startProcess(t.TempDir(), "ox-session", newUUID(), false, agent.Settings{}, agent.MCPServer{}, events); err != nil {
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
			if exit, ok := event.(processExit); ok {
				if !result {
					t.Fatal("Claude's last line was dropped")
				}
				if exit.err == nil || !strings.Contains(exit.err.Error(), "exit status 3") {
					t.Fatalf("unexpected exit: %v", exit.err)
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
	p, in, err := startProcess(t.TempDir(), "ox-session", newUUID(), false, agent.Settings{}, agent.MCPServer{}, events)
	if err != nil {
		t.Fatal(err)
	}
	drained := false
	t.Cleanup(func() {
		if !drained {
			p.cmd.Process.Kill()
			go func() {
				for e := range events {
					if _, ok := e.(processExit); ok {
						return
					}
				}
			}()
		}
	})
	sent := make(chan error, 1)
	go func() { sent <- in.Prompt(newUUID(), strings.Repeat("x", 256<<10)) }()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt write stalled behind unread Claude stdout")
	}
	for deadline := time.Now().Add(5 * time.Second); !errors.Is(syscall.Kill(p.cmd.Process.Pid, 0), syscall.ESRCH); {
		if time.Now().After(deadline) {
			t.Fatal("Claude could not exit with unread output")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(1100 * time.Millisecond) // let the pipe deadline pass before accepting delivery
	timeout := time.After(5 * time.Second)
	for i := range 258 {
		select {
		case e := <-events:
			if _, ok := e.(processExit); ok {
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
				if exit, ok := e.(processExit); !ok || exit.err == nil || exit.stderr != "Claude diagnostic" {
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
			start := time.Now()
			_, _, err := startProcess(workspace, "ox-session", newUUID(), false, agent.Settings{}, agent.MCPServer{}, make(chan agent.Event))
			if err == nil || !strings.Contains(err.Error(), refusal) {
				t.Fatalf("startup failure lost Claude's stderr: %v", err)
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Fatalf("startup failure took %v", elapsed)
			}
		})
	}
}

// standIn puts a claude script on PATH that answers the handshake and then
// runs rest.
func standIn(t *testing.T, rest string) {
	script(t, "#!/bin/sh\nread line\n"+
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'`+"\n"+rest)
}

// script puts a claude script on PATH.
func script(t *testing.T, script string) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Claude bills an API key it inherits instead of the user's subscription,
// so it starts without one.
func TestClaudeStartsWithoutAnAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token-test")
	t.Setenv("OX_TEST_KEPT", "kept")
	env := filepath.Join(t.TempDir(), "env")
	standIn(t, "env > "+env+"\n")
	events := make(chan agent.Event, 1)
	if _, _, err := startProcess(t.TempDir(), "ox-session", newUUID(), false, agent.Settings{}, agent.MCPServer{}, events); err != nil {
		t.Fatal(err)
	}
	for e := range events {
		if _, exited := e.(processExit); exited {
			break
		}
	}
	data, err := os.ReadFile(env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ANTHROPIC_") || !strings.Contains(string(data), "OX_TEST_KEPT=kept") {
		t.Fatalf("Claude's environment:\n%s", data)
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
		if _, _, err := startProcess(workspace, "ox-session", newUUID(), resume, agent.Settings{}, mcp, events); err != nil {
			t.Fatal(err)
		}
		for e := range events {
			if _, exited := e.(processExit); exited {
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
		if !slices.Contains(args, "--disallowedTools=SendMessage,ListAgents") {
			t.Errorf("resume %v: built-in messaging is not disallowed in %q", resume, args)
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
		p, input, err := startProcess(t.TempDir(), "ox-session", newUUID(), false, agent.Settings{Effort: effort}, agent.MCPServer{}, make(chan agent.Event, 1))
		if p != nil {
			input.w.(io.Closer).Close()
			p.cmd.Process.Kill()
		}
		if (err == nil) != known {
			t.Errorf("effort %q started: %v", effort, err)
		}
	}
}
