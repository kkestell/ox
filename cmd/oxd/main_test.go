package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	"github.com/kkestell/ox/internal/fake"
	"github.com/kkestell/ox/internal/store"
	"github.com/kkestell/ox/internal/ur"
)

func TestListenReplacesOnlyAStaleSocket(t *testing.T) {
	// t.TempDir is too long for a socket path on macOS.
	dir, err := os.MkdirTemp("", "oxd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	stale := filepath.Join(dir, "stale.sock")
	var l *net.UnixListener
	l, err = net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	l.Close()
	if l, err := listen(stale); err != nil {
		t.Fatalf("a stale socket was not replaced: %v", err)
	} else {
		l.Close()
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, err := listen(file); err == nil {
		l.Close()
		t.Error("listen took the path of a regular file")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "kept" {
		t.Fatalf("listen changed the regular file at its path: %q, %v", data, err)
	}
}

func TestConfigOffersOnlyWhatHarnessesTake(t *testing.T) {
	builtIn, err := config.Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	refuse := func(string) error { return errors.New("refused") }
	harnesses := map[string]agent.Harness{
		"claude": claude.Harness,
		"ur":     ur.Harness(ur.Endpoint, nil),
		"picky":  {Start: fake.Start, CheckEffort: refuse, CheckPermissionMode: refuse},
		"plain":  {Start: fake.Start},
	}
	if err := checkConfig(builtIn, harnesses); err != nil {
		t.Fatalf("the built-in configuration was refused: %v", err)
	}
	plain := &config.Config{Harnesses: map[string]config.Harness{"plain": {
		Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "any"}}}},
		PermissionModes: []config.Option{{ID: "any"}},
	}}}
	if err := checkConfig(plain, harnesses); err != nil {
		t.Fatalf("a harness without checks was refused: %v", err)
	}
	for name, cfg := range map[string]*config.Config{
		"a Claude effort level Claude does not take": {Harnesses: map[string]config.Harness{"claude": {
			Models: []config.Model{{ID: "claude-haiku-5-5", Efforts: []config.Option{{ID: "hgih"}}}},
		}}},
		"an Ur effort level Ur does not take": {Harnesses: map[string]config.Harness{"ur": {
			Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "extreme"}}}},
			PermissionModes: []config.Option{{ID: "manual"}},
		}}},
		"an Ur permission mode Ur does not take": {Harnesses: map[string]config.Harness{"ur": {
			Models:          []config.Model{{ID: "m"}},
			PermissionModes: []config.Option{{ID: "manual"}, {ID: "auto"}},
		}}},
		"an effort level a harness's own check refuses": {Harnesses: map[string]config.Harness{"picky": {
			Models: []config.Model{{ID: "m", Efforts: []config.Option{{ID: "low"}}}},
		}}},
		"a permission mode a harness's own check refuses": {Harnesses: map[string]config.Harness{"picky": {
			Models:          []config.Model{{ID: "m"}},
			PermissionModes: []config.Option{{ID: "manual"}},
		}}},
		"a harness oxd does not have": {Harnesses: map[string]config.Harness{"nope": {
			Models: []config.Model{{ID: "m"}},
		}}},
		"a provider order on a Claude model": {Harnesses: map[string]config.Harness{"claude": {
			Models:          []config.Model{{ID: "claude-haiku-5-5", Providers: []string{"anthropic"}}},
			PermissionModes: []config.Option{{ID: "manual"}},
		}}},
	} {
		if err := checkConfig(cfg, harnesses); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestConfigRefusesClaudesDefaultPermissionModeAlias(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"claude": {
		PermissionModes: []config.Option{{ID: "default"}},
	}}}
	if err := checkConfig(cfg, map[string]agent.Harness{"claude": claude.Harness}); err == nil || !strings.Contains(err.Error(), "manual") {
		t.Fatalf("Claude's alias default was not refused in favor of manual: %v", err)
	}
}

// TestMain runs oxd itself when a test starts this binary as oxd.
func TestMain(m *testing.M) {
	if os.Getenv("OXD_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// Without ox-mcp beside it, every Claude would run with its ox server
// failed, so oxd refuses to start.
func TestRefusesToStartWithoutForwarder(t *testing.T) {
	// t.TempDir is too long for a socket path on macOS.
	dir, err := os.MkdirTemp("", "oxd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "oxd.sock")
	// The refusal comes before the database opens; an oxd still running at
	// the deadline started without ox-mcp, and is killed.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Go's build directory holds the test binary and no ox-mcp.
	oxd := exec.CommandContext(ctx, os.Args[0], "-socket="+socket, "-db="+filepath.Join(dir, "ox.db"), "-config="+filepath.Join(dir, "none.json"))
	oxd.Env = append(os.Environ(), "OXD_TEST_MAIN=1")
	out, err := oxd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("oxd started without ox-mcp:\n%s", out)
	}
	if err == nil || !strings.Contains(string(out), "ox-mcp") {
		t.Fatalf("oxd without ox-mcp: %v\n%s", err, out)
	}
	if _, err := os.Stat(socket); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("oxd without ox-mcp left its socket: %v", err)
	}
}

// The socket excludes a second daemon while oxd is serving or still
// stopping. A second signal kills both running and initializing children.
func TestDaemonOwnsSocketUntilShutdown(t *testing.T) {
	// t.TempDir is too long for a socket path on macOS.
	dir, err := os.MkdirTemp("", "oxd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	standIn := `#!/bin/sh
trap '' TERM
case "$*" in
  *--permission-mode=plan*) echo $$ > initializing; exec sleep 60;;
esac
echo $$ > running
read line
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'
read line
uuid=${line##*'"uuid":"'}; uuid=${uuid%%'"'*}
printf '{"type":"command_lifecycle","state":"started","command_uuid":"%s"}\n' "$uuid"
exec sleep 60
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(standIn), 0o755); err != nil {
		t.Fatal(err)
	}
	// The stand-in Claude never starts the forwarder; oxd only needs it there.
	if err := os.WriteFile(filepath.Join(bin, "ox-mcp"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "oxd"), self, 0o755); err != nil {
		t.Fatal(err)
	}
	socket, database := filepath.Join(dir, "oxd.sock"), filepath.Join(dir, "ox.db")
	command := func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, filepath.Join(bin, "oxd"), "-socket="+socket, "-db="+database, "-config="+filepath.Join(dir, "none.json"))
		cmd.Env = append(os.Environ(), "OXD_TEST_MAIN=1", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		return cmd
	}
	oxd := command(t.Context())
	if err := oxd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { oxd.Process.Kill() })
	exited := make(chan struct{})
	go func() { oxd.Wait(); close(exited) }()

	var conn net.Conn
	for deadline := time.Now().Add(10 * time.Second); conn == nil; {
		if conn, err = net.Dial("unix", socket); err != nil {
			if time.Now().After(deadline) {
				t.Fatalf("oxd did not start: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(25 * time.Second))
	lines := bufio.NewScanner(conn)
	send := func(id int, method string, params map[string]string) {
		t.Helper()
		request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := fmt.Fprintf(conn, "%s\n", request); err != nil {
			t.Fatal(err)
		}
	}
	call := func(id int, method string, params map[string]string) map[string]string {
		t.Helper()
		send(id, method, params)
		for lines.Scan() {
			var r struct {
				ID     *int              `json:"id"`
				Result map[string]string `json:"result"`
				Error  *struct{ Message string }
			}
			if json.Unmarshal(lines.Bytes(), &r) != nil || r.ID == nil || *r.ID != id {
				continue
			}
			if r.Error != nil {
				t.Fatalf("%s: %s", method, r.Error.Message)
			}
			return r.Result
		}
		t.Fatalf("%s: %v", method, lines.Err())
		return nil
	}
	workspace := call(1, "workspace/create", map[string]string{"path": dir})["workspace_id"]
	session := call(2, "session/create", map[string]string{
		"workspace_id": workspace, "harness": "claude", "model": "claude-haiku-5-5", "effort": "low", "permission_mode": "manual",
	})["session_id"]
	call(3, "session/send", map[string]string{"session_id": session, "text": "hello"})
	for lines.Scan() {
		if strings.Contains(lines.Text(), `"type":"prompt"`) {
			break
		}
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	loading := call(4, "session/create", map[string]string{
		"workspace_id": workspace, "harness": "claude", "model": "claude-haiku-5-5", "effort": "low", "permission_mode": "plan",
	})["session_id"]
	send(5, "session/send", map[string]string{"session_id": loading, "text": "hello"})
	pid := func(name string) int {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			data, _ := os.ReadFile(filepath.Join(dir, name))
			if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && p > 0 {
				t.Cleanup(func() { syscall.Kill(p, syscall.SIGKILL) })
				return p
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("Claude's %s process did not start", name)
		return 0
	}
	pids := []int{pid("running"), pid("initializing")}
	refusesSecond := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		out, err := command(ctx).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "another oxd is serving") {
			t.Fatalf("a second daemon was not refused: %v, %s", err, out)
		}
	}
	refusesSecond()
	st, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	checkHistory := func(types ...string) {
		t.Helper()
		events, err := st.Events(session)
		var got []string
		for _, e := range events {
			var u struct{ Type string }
			json.Unmarshal(e.Data, &u)
			got = append(got, u.Type)
		}
		if err != nil || !slices.Equal(got, types) {
			t.Fatalf("the live session's history is %q, not %q: %v", got, types, err)
		}
	}
	checkHistory("prompt")

	// The first stop ends the sessions at once and waits for Claude.
	oxd.Process.Signal(syscall.SIGINT)
	select {
	case <-exited:
		t.Fatal("shutdown finished without waiting for Claude")
	case <-time.After(time.Second):
	}
	refusesSecond()
	checkHistory("prompt", "exited", "parked")
	oxd.Process.Signal(syscall.SIGINT)
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("a second interrupt did not stop oxd")
	}
	for _, p := range pids {
		for deadline := time.Now().Add(3 * time.Second); !errors.Is(syscall.Kill(p, 0), syscall.ESRCH); {
			if time.Now().After(deadline) {
				t.Fatalf("Claude PID %d survived a second interrupt", p)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
