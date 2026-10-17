package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"ox/internal/mcptest"
	"ox/internal/settings"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	fake "ox/internal/openroutertest"
)

func TestStartupSignalHelper(t *testing.T) {
	command := os.Getenv("OX_TEST_COMMAND")
	if command == "" {
		return
	}
	args := []string{command}
	if command == "run" {
		args = append(args, "hello")
	}
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestSignalsCancelStartupCatalogRequests(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"acp", "run"} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
			t.Run(fmt.Sprintf("%s/%s", command, sig), func(t *testing.T) {
				requested := make(chan struct{})
				cancelled := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					close(requested)
					<-r.Context().Done()
					close(cancelled)
				}))
				defer server.Close()
				cmd := exec.Command(executable, "-test.run=^TestStartupSignalHelper$")
				cmd.Dir = t.TempDir()
				cmd.Env = append(os.Environ(), "OX_TEST_COMMAND="+command, "OPENROUTER_API_KEY=test-key", "OX_OPENROUTER_ENDPOINT="+server.URL)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				defer cmd.Process.Kill()
				select {
				case <-requested:
				case err := <-done:
					t.Fatalf("exited before requesting the catalog: %v: %s", err, stderr.String())
				case <-time.After(5 * time.Second):
					t.Fatal("did not request the catalog")
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				select {
				case <-cancelled:
				case <-time.After(5 * time.Second):
					t.Fatal("signal did not cancel the catalog request")
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatalf("expected cancellation, got %v: %s", err, stderr.String())
					}
				case <-time.After(5 * time.Second):
					t.Fatal("did not exit after cancellation")
				}
			})
		}
	}
}

func TestRunOptions(t *testing.T) {
	for _, test := range []struct {
		args []string
		want runOptions
	}{
		{[]string{"hello"}, runOptions{".", "", "default", "hello"}},
		{[]string{"--dir", "some dir", "--model=provider:model", "--effort", "high", "hello"}, runOptions{"some dir", "provider:model", "high", "hello"}},
		{[]string{"--", "--help"}, runOptions{".", "", "default", "--help"}},
	} {
		got, err := parseRunOptions(test.args)
		if err != nil || got != test.want {
			t.Errorf("%q: %+v, %v", test.args, got, err)
		}
	}
	for _, args := range [][]string{nil, {" "}, {"--dir"}, {"--unknown", "hi"}, {"one", "two"}, {"hello", "--model", "model"}} {
		if _, err := parseRunOptions(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
	if _, err := parseRunOptions([]string{"--help"}); err != flag.ErrHelp {
		t.Errorf("help: %v", err)
	}
}

func TestHeadlessReportsAnAnswerWriteFailure(t *testing.T) {
	provider := fake.Start(t, fake.Status(200, fake.Catalog), fake.Text("An answer that must reach stdout."))
	t.Setenv("OPENROUTER_API_KEY", "test")
	t.Setenv("OX_OPENROUTER_ENDPOINT", provider.URL)
	t.Setenv("OX_DATA_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".config/ox")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"model":"`+fake.DefaultModel+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = output
	t.Cleanup(func() { os.Stdout = previous })
	if err := runHeadless([]string{"--dir", t.TempDir(), "hello"}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("runHeadless error = %v, want a closed-output error", err)
	}
}

func TestMCPHelper(t *testing.T) { mcptest.Run() }

func TestHeadlessMCPCallAndSignalCleanup(t *testing.T) {
	for _, phase := range []string{"success", "startup", "call"} {
		t.Run(phase, func(t *testing.T) {
			marker := t.TempDir() + "/events"
			config := mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("work", false)}, Marker: marker}
			if phase == "startup" {
				config.Startup = "wait"
			}
			if phase == "call" {
				config.Calls = map[string]string{"work": "wait"}
			}
			definition, _ := mcptest.Definition(t, config)
			provider := fake.Start(t, fake.Status(200, fake.Catalog), fake.Calls(fake.Call{ID: "one", Name: "mcp__project__work", Arguments: map[string]any{"number": 7}}), fake.Text("MCP complete"))
			home := t.TempDir()
			settingsPath := filepath.Join(home, ".config/ox/settings.json")
			if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(map[string]any{"model": fake.DefaultModel, "mcpServers": map[string]settings.MCPServer{"project": definition}})
			if err := os.WriteFile(settingsPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "-test.run=^TestStartupSignalHelper$")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "OX_TEST_COMMAND=run", "HOME="+home, "OX_DATA_DIR="+t.TempDir(), "OPENROUTER_API_KEY=test", "OX_OPENROUTER_ENDPOINT="+provider.URL)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			if phase != "success" {
				want := "start "
				if phase == "call" {
					want = "call work"
				}
				mcptest.WaitFor(t, marker, want)
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if phase == "success" {
					if err != nil || stdout.String() != "MCP complete\n" {
						t.Fatalf("run: %v stdout=%q stderr=%s", err, stdout.String(), stderr.String())
					}
					if len(provider.Bodies()) != 2 {
						t.Fatal("missing MCP result request")
					}
					body, _ := json.Marshal(provider.Bodies()[1])
					if !strings.Contains(string(body), `\"number\":7`) {
						t.Fatalf("missing result: %s", body)
					}
				} else if err == nil || !strings.Contains(stderr.String(), "cancelled") {
					t.Fatalf("signal: %v %s", err, stderr.String())
				}
			case <-time.After(6 * time.Second):
				t.Fatal("headless process did not exit")
			}
			data, _ = os.ReadFile(marker)
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "start ") {
					pid, err := strconv.Atoi(strings.TrimPrefix(line, "start "))
					if err != nil {
						t.Fatal(err)
					}
					if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
						t.Fatalf("server process survived: %d %v", pid, err)
					}
				}
			}
			if phase == "startup" && len(provider.Bodies()) != 0 {
				t.Fatal("cancelled preparation sent a model request")
			}
			if phase == "call" && strings.Count(string(data), "call work") != 1 {
				t.Fatalf("call replayed: %s", data)
			}
		})
	}
}
