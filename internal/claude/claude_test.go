package claude

import (
	"bufio"
	"io"
	"os"
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
	} {
		t.Run(name, func(t *testing.T) {
			for _, input := range []io.Writer{io.Discard, closedPipe(t)} {
				err := initialize(input, bufio.NewReader(strings.NewReader(output)))
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
// exits, as the injected fault in known-issues.md did.
func TestExitReportedWhileAChildHoldsStdout(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nread line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		"sleep 15 2>/dev/null &\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	events := make(chan agent.Event)
	if _, _, err := startProcess(t.TempDir(), newUUID(), false, agent.Settings{}, events); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if exit, ok := event.(processExit); ok {
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
