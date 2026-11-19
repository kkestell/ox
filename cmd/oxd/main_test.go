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
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/claude"
	"github.com/kkestell/ox/internal/config"
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

func TestConfigOffersOnlyClaudesEfforts(t *testing.T) {
	builtIn, err := config.Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	harnesses := map[string]agent.Start{"claude": claude.Start}
	if err := checkConfig(builtIn, harnesses); err != nil {
		t.Fatalf("the built-in configuration was refused: %v", err)
	}
	misspelled := &config.Config{Harnesses: map[string]config.Harness{"claude": {
		Models: []config.Model{{ID: "claude-haiku-5-5", Efforts: []config.Option{{ID: "hgih"}}}},
	}}}
	if err := checkConfig(misspelled, harnesses); err == nil {
		t.Error("a Claude effort level Claude does not take was accepted")
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

// Shutdown waits for each Claude to exit, which takes up to Ox's kill
// timeout when Claude ignores SIGTERM. A second interrupt stops oxd at once.
func TestSecondInterruptStopsShutdown(t *testing.T) {
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
	standIn := "#!/bin/sh\ntrap '' TERM\nread line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\nexec sleep 20\n"
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
	socket := filepath.Join(dir, "oxd.sock")
	oxd := exec.Command(filepath.Join(bin, "oxd"), "-socket="+socket, "-db="+filepath.Join(dir, "ox.db"), "-config="+filepath.Join(dir, "none.json"))
	oxd.Env = append(os.Environ(), "OXD_TEST_MAIN=1", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	lines := bufio.NewScanner(conn)
	call := func(id int, method string, params map[string]string) map[string]string {
		request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		fmt.Fprintf(conn, "%s\n", request)
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

	oxd.Process.Signal(syscall.SIGINT)
	select {
	case <-exited:
		t.Fatal("shutdown finished without waiting for Claude")
	case <-time.After(time.Second):
	}
	oxd.Process.Signal(syscall.SIGINT)
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("a second interrupt did not stop oxd")
	}
}
