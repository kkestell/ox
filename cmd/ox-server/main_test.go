package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
