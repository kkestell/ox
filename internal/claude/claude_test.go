package claude

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// Claude reports a startup failure as an error result or plain text before
// it exits, sometimes before reading initialize.
func TestStartupFailureKeepsClaudesReason(t *testing.T) {
	for name, output := range map[string]string{
		"error result": `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["No conversation found with session ID: 1111"]}` + "\n",
		"plain text":   "No conversation found with session ID: 1111\n",
		"line at EOF":  "No conversation found with session ID: 1111",
	} {
		t.Run(name, func(t *testing.T) {
			for _, input := range []io.Writer{io.Discard, closedPipe(t)} {
				err := initialize(input, bufio.NewReader(strings.NewReader(output)), func() string { return "" })
				if err == nil || !strings.Contains(err.Error(), "No conversation found with session ID: 1111") {
					t.Fatalf("startup failure lost Claude's reason: %v", err)
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
	if _, _, err := startProcess(t.TempDir(), newUUID(), false, agent.Settings{}, events); err != nil {
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
			_, _, err := startProcess(workspace, newUUID(), false, agent.Settings{}, make(chan agent.Event))
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

// Claude runs at its default effort, with only a warning, when given an
// effort it does not know.
func TestUnknownEffortFailsToStart(t *testing.T) {
	standIn(t, "exec sleep 15\n")
	for effort, known := range map[string]bool{"": true, "high": true, "hgih": false} {
		p, input, err := startProcess(t.TempDir(), newUUID(), false, agent.Settings{Effort: effort}, make(chan agent.Event, 1))
		if p != nil {
			input.w.(io.Closer).Close()
			p.cmd.Process.Kill()
		}
		if (err == nil) != known {
			t.Errorf("effort %q started: %v", effort, err)
		}
	}
}
