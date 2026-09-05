package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/acp"
)

var runtimeBinary string

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "ox-integration-")
	if err != nil {
		panic(err)
	}

	runtimeBinary = filepath.Join(tempDir, "ox")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-o", runtimeBinary, "../cmd/ox")
	if output, err := command.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build runtime: %v\n%s", err, output))
	}
	code := m.Run()
	if err := os.RemoveAll(tempDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}

func TestInitializeAndStdoutPurity(t *testing.T) {
	process := startRuntime(t, "debug")

	process.write(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{},"_meta":{"test":true}}}`)
	response := process.read(t)

	if response["jsonrpc"] != "2.0" || response["id"] != float64(1) {
		t.Fatalf("unexpected response: %#v", response)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result: %#v", response)
	}
	if result["protocolVersion"] != float64(1) {
		t.Fatalf("protocol version = %#v", result["protocolVersion"])
	}
	authMethods, ok := result["authMethods"].([]any)
	if !ok || len(authMethods) != 1 ||
		authMethods[0].(map[string]any)["id"] != "openrouter" {
		t.Fatalf("authMethods = %#v", result["authMethods"])
	}
	capabilities, ok := result["agentCapabilities"].(map[string]any)
	if !ok || capabilities["auth"] == nil {
		t.Fatalf("agentCapabilities = %#v", result["agentCapabilities"])
	}
	sessionCapabilities, ok := capabilities["sessionCapabilities"].(map[string]any)
	if capabilities["loadSession"] != true || !ok ||
		sessionCapabilities["list"] == nil ||
		sessionCapabilities["delete"] == nil ||
		sessionCapabilities["resume"] == nil ||
		sessionCapabilities["close"] == nil {
		t.Fatalf("session lifecycle capabilities = %#v", capabilities)
	}
	if err := process.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	for process.stdout.Scan() {
		var message map[string]any
		if err := json.Unmarshal(process.stdout.Bytes(), &message); err != nil {
			t.Fatalf("stdout was not JSON-RPC: %q: %v", process.stdout.Text(), err)
		}
	}
	if err := process.stdout.Err(); err != nil {
		t.Fatal(err)
	}
	if err := process.command.Wait(); err != nil {
		t.Fatalf("runtime exit: %v\nstderr:\n%s", err, process.stderr.String())
	}
}

func TestUnsupportedVersionReturnsNormalResult(t *testing.T) {
	process := startRuntime(t, "")
	defer process.stop(t)

	process.write(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":999}}`)
	response := process.read(t)

	if _, present := response["error"]; present {
		t.Fatalf("initialize returned an error: %#v", response)
	}
	result := response["result"].(map[string]any)
	if result["protocolVersion"] != float64(1) {
		t.Fatalf("protocol version = %#v", result["protocolVersion"])
	}
}

func TestAuthenticationMethodsAndSecretPurity(t *testing.T) {
	process := startRuntime(t, "debug")
	const secret = "never-print-this-key"

	process.write(t, `{"jsonrpc":"2.0","id":1,"method":"authenticate","params":{"methodId":"unknown"}}`)
	response := process.read(t)
	if errorCode(response) != float64(-32602) {
		t.Fatalf("unknown method response = %#v", response)
	}

	process.write(t, `{"jsonrpc":"2.0","id":2,"method":"authenticate","params":{"methodId":"openrouter"}}`)
	response = process.read(t)
	if errorCode(response) != float64(acp.ErrCodeAuthRequired) {
		t.Fatalf("missing credential response = %#v", response)
	}

	process.write(t, `{"jsonrpc":"2.0","id":3,"method":"authenticate","params":{"methodId":"openrouter","_meta":{"ignored":"`+secret+`"}}}`)
	response = process.read(t)
	if errorCode(response) != float64(acp.ErrCodeAuthRequired) {
		t.Fatalf("metadata credential response = %#v", response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("authenticate response leaked key: %s", encoded)
	}

	process.write(t, `{"jsonrpc":"2.0","id":4,"method":"logout","params":{}}`)
	response = process.read(t)
	if _, ok := response["result"].(map[string]any); !ok {
		t.Fatalf("logout response = %#v", response)
	}

	process.write(t, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":5,"method":"session/new","params":{"cwd":%q,"mcpServers":[]}}`,
		t.TempDir(),
	))
	response = process.read(t)
	if errorCode(response) != float64(acp.ErrCodeAuthRequired) {
		t.Fatalf("post-logout session response = %#v", response)
	}

	process.stop(t)
	if strings.Contains(process.stderr.String(), secret) {
		t.Fatalf("stderr leaked key:\n%s", process.stderr.String())
	}
}

func TestMalformedInputProducesParseErrorAndRuntimeStaysAlive(t *testing.T) {
	process := startRuntime(t, "")
	defer process.stop(t)

	process.write(t, "{not json")
	response := process.read(t)
	errorObject, ok := response["error"].(map[string]any)
	if !ok || errorObject["code"] != float64(-32700) {
		t.Fatalf("expected parse error, got %#v", response)
	}

	process.write(t, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":1}}`)
	response = process.read(t)
	if response["id"] != float64(2) || response["result"] == nil {
		t.Fatalf("runtime did not remain alive: %#v", response)
	}
}

func errorCode(response map[string]any) float64 {
	errorObject, _ := response["error"].(map[string]any)
	code, _ := errorObject["code"].(float64)
	return code
}

func TestClosingStdinStopsRuntimeCleanly(t *testing.T) {
	process := startRuntime(t, "")
	if err := process.stdin.Close(); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- process.command.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runtime exit: %v\nstderr:\n%s", err, process.stderr.String())
		}
	case <-time.After(5 * time.Second):
		_ = process.command.Process.Kill()
		t.Fatal("runtime did not exit after stdin closed")
	}
}

type runtimeProcess struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	stderr  strings.Builder
}

func startRuntime(t *testing.T, logLevel string) *runtimeProcess {
	t.Helper()

	arguments := []string{"--no-keyring"}
	if logLevel != "" {
		arguments = append(arguments, "--log-level", logLevel)
	}
	command := exec.CommandContext(t.Context(), runtimeBinary, arguments...)
	command.Env = append(
		os.Environ(),
		// An empty config home keeps the developer's own settings file out of
		// the process tests.
		"XDG_CONFIG_HOME="+t.TempDir(),
		"XDG_DATA_HOME="+t.TempDir(),
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	process := &runtimeProcess{
		command: command,
		stdin:   stdin,
		stdout:  bufio.NewScanner(stdout),
	}
	command.Stderr = &process.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	return process
}

func (p *runtimeProcess) write(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (p *runtimeProcess) read(t *testing.T) map[string]any {
	t.Helper()

	line := make(chan string, 1)
	go func() {
		if p.stdout.Scan() {
			line <- p.stdout.Text()
		}
		close(line)
	}()

	select {
	case value, ok := <-line:
		if !ok {
			t.Fatalf("runtime stdout closed\nstderr:\n%s", p.stderr.String())
		}
		var response map[string]any
		if err := json.Unmarshal([]byte(value), &response); err != nil {
			t.Fatalf("stdout was not JSON-RPC: %q: %v", value, err)
		}
		return response
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for runtime\nstderr:\n%s", p.stderr.String())
		return nil
	}
}

func (p *runtimeProcess) stop(t *testing.T) {
	t.Helper()
	if p.command.ProcessState != nil {
		return
	}
	if err := p.stdin.Close(); err != nil {
		t.Error(err)
	}
	if err := p.command.Wait(); err != nil {
		t.Errorf("runtime exit: %v\nstderr:\n%s", err, p.stderr.String())
	}
}
