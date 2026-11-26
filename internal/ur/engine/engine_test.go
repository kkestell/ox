package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/ur/wire"
)

// waitFile waits for a command to write file.
func waitFile(t *testing.T, file string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(file); err == nil && strings.HasSuffix(string(data), "\n") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never written", file)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// When oxd goes without stopping ur, ur's stdin ends, and ur kills its
// command's process group before it exits.
func TestEndOfInputEndsCommands(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	arguments, _ := json.Marshal(map[string]string{"command": "echo $$ > shell; sleep 60 & echo $! > child; sleep 60"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "shell", "arguments": string(arguments)},
		}}}}}})
		fmt.Fprintf(w, "data: %s\n\n", call)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	workspace := t.TempDir()
	t.Chdir(workspace)
	stdin, input := io.Pipe()
	output, stdout := io.Pipe()
	returned := make(chan int, 1)
	go func() {
		returned <- run([]string{"-dir=" + t.TempDir(), "-model=m", "-permission-mode=auto", "-endpoint=" + server.URL}, stdin, stdout, io.Discard)
	}()
	go json.NewEncoder(input).Encode(wire.Command{Type: wire.CommandInput, Input: &wire.Input{MessageID: "p1", Content: "go"}})
	events := json.NewDecoder(output)
	for {
		var e wire.Event
		if err := events.Decode(&e); err != nil {
			t.Fatal(err)
		}
		if e.Type == wire.EventUpdate && e.Update.Type == agent.UpdateToolUse {
			break
		}
	}
	go io.Copy(io.Discard, output)
	waitFile(t, filepath.Join(workspace, "child"))
	input.Close()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("ur kept running after its input ended")
	}
	gone(t, filepath.Join(workspace, "shell"))
	gone(t, filepath.Join(workspace, "child"))
}

// running is an engine whose turn is running, writing its events to out.
func running(t *testing.T) (*engine, *bytes.Buffer) {
	t.Helper()
	out := new(bytes.Buffer)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := &engine{
		dir: t.TempDir(), id: "conversation", workspace: t.TempDir(), out: json.NewEncoder(out), stderr: io.Discard,
		events: make(chan any), stopped: make(chan struct{}),
		turn: &turn{ctx: ctx, cancel: cancel, step: streaming{}},
	}
	return e, out
}

// Output that reaches the engine after its turn has ended, such as a delta
// read before an interrupt, belongs to no later turn: it is neither saved nor
// published, and the running turn goes on.
func TestStaleOutput(t *testing.T) {
	t.Run("delta", func(t *testing.T) {
		e, out := running(t)
		e.handle(delta{turn: &turn{}, text: "late"})
		if out.Len() != 0 || len(e.history) != 0 {
			t.Errorf("a stale delta wrote %q and saved %+v", out, e.history)
		}
	})
	t.Run("end of request", func(t *testing.T) {
		e, out := running(t)
		current := e.turn
		e.handle(ended{turn: &turn{}, err: errors.New("context canceled")})
		if out.Len() != 0 || e.turn != current {
			t.Errorf("a stale request's end wrote %q and left turn %p, not %p", out, e.turn, current)
		}
	})
	t.Run("command outcome", func(t *testing.T) {
		e, out := running(t)
		e.turn.step = &executing{calls: []toolCall{{ID: "call_2"}}}
		e.handle(ran{step: &executing{calls: []toolCall{{ID: "call_1"}}}, group: 7, outcome: outcome{"Terminated by signal: 9", true}})
		want := `{"type":"command_ended","group":7}` + "\n"
		if out.String() != want || len(e.history) != 0 {
			t.Errorf("a stale command outcome wrote %q, not %q, and saved %+v", out, want, e.history)
		}
	})
}
