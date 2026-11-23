package ur

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// shell runs command in a temporary workspace, which it returns.
func shell(t *testing.T, ctx context.Context, arguments string) (outcome, string) {
	t.Helper()
	c, err := parseShell(arguments)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	return runShell(ctx, workspace, c), workspace
}

// gone reports whether the process whose PID the file holds has ended.
func gone(t *testing.T, file string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	// A killed child is reaped by init once its parent shell is gone; give it
	// a moment.
	deadline := time.Now().Add(2 * time.Second)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("background child %d still runs", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestShellOutputAndStatus(t *testing.T) {
	o, workspace := shell(t, t.Context(), `{"command":"pwd; printf out; printf err >&2; read v || echo no-input"}`)
	real, _ := filepath.EvalSymlinks(workspace)
	if o.isError || !strings.HasPrefix(o.text, "Exit code: 0\n\n") {
		t.Errorf("outcome %+v", o)
	}
	for _, want := range []string{real, "out", "err", "no-input"} {
		if !strings.Contains(o.text, want) {
			t.Errorf("output %q lacks %q", o.text, want)
		}
	}

	if o, _ := shell(t, t.Context(), `{"command":"exit 7"}`); !o.isError || o.text != "Exit code: 7\n\n(no output)" {
		t.Errorf("exit 7: %+v", o)
	}
	if o, _ := shell(t, t.Context(), `{"command":"kill -TERM $$"}`); !o.isError || !strings.HasPrefix(o.text, "Terminated by signal: 15\n") {
		t.Errorf("kill: %+v", o)
	}
}

func TestShellWithoutKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret")
	o, _ := shell(t, t.Context(), `{"command":"echo \"key=${OPENROUTER_API_KEY-unset}\""}`)
	if !strings.Contains(o.text, "key=unset") {
		t.Errorf("the command saw the key: %q", o.text)
	}
}

func TestShellLongOutput(t *testing.T) {
	// 4 KiB of a, 16 KiB of b, 4 KiB of c.
	o, _ := shell(t, t.Context(), `{"command":"head -c 4096 /dev/zero | tr '\\0' a; head -c 16384 /dev/zero | tr '\\0' b; head -c 4096 /dev/zero | tr '\\0' c"}`)
	want := "Exit code: 0\n\n" + strings.Repeat("a", 4096) + strings.Repeat("b", 4096) +
		"\n[... 8192 bytes omitted ...]\n" + strings.Repeat("b", 4096) + strings.Repeat("c", 4096)
	if o.text != want {
		t.Errorf("long output kept %d bytes, not the first and last 8 KiB", len(o.text))
	}
}

func TestShellTimeout(t *testing.T) {
	start := time.Now()
	o, workspace := shell(t, t.Context(), `{"command":"sleep 60 & echo $! > child; sleep 60","timeout_seconds":1}`)
	if !o.isError || !strings.HasPrefix(o.text, "Timed out after 1 seconds\n") {
		t.Errorf("outcome %+v", o)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("a 1-second timeout took %s", elapsed)
	}
	gone(t, filepath.Join(workspace, "child"))
}

func TestShellCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, workspace := shell(t, ctx, `{"command":"sleep 60 & echo $! > child; sleep 60"}`)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("a canceled command took %s", elapsed)
	}
	gone(t, filepath.Join(workspace, "child"))
}

// A background child holding the output pipe holds the call at most a second,
// and is killed.
func TestShellLeftChild(t *testing.T) {
	start := time.Now()
	o, workspace := shell(t, t.Context(), `{"command":"sleep 60 & echo $! > child; echo done"}`)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("a command leaving a child took %s", elapsed)
	}
	if o.isError || !strings.Contains(o.text, "done") {
		t.Errorf("outcome %+v", o)
	}
	gone(t, filepath.Join(workspace, "child"))
}

func TestShellArguments(t *testing.T) {
	for _, arguments := range []string{
		`{"command":`,
		`{}`,
		`{"command":"touch wrong","cwd":"/"}`,
		`{"command":"touch wrong","timeout_seconds":0}`,
		`{"command":"touch wrong","timeout_seconds":601}`,
		`{"command":"touch wrong","timeout_seconds":"1"}`,
		`{"command":7}`,
	} {
		if _, err := parseShell(arguments); err == nil || !strings.HasPrefix(err.Error(), "arguments: ") {
			t.Errorf("parseShell(%s): %v", arguments, err)
		}
	}
	c, err := parseShell(`{"command":"true"}`)
	if err != nil || c.timeout != 2*time.Minute {
		t.Errorf("default timeout: %+v, %v", c, err)
	}
}
