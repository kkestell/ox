package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
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
