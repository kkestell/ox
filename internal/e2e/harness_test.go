package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	readTimeout     = 5 * time.Second
	shutdownTimeout = 5 * time.Second
)

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`

	raw []byte
}

type startConfig struct {
	environment map[string]string
}

type startOption func(*startConfig)

func withEnvironment(name, value string) startOption {
	return func(config *startConfig) {
		config.environment[name] = value
	}
}

type process struct {
	t       *testing.T
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *os.File
	reader  *bufio.Reader
	stderr  bytes.Buffer
	cwd     string

	pending      []message
	nextID       int
	stopped      bool
	stopErr      error
	stopReported bool
}

// call is a request that has been sent but not yet answered.
type call struct {
	id     int
	method string
}

func start(t *testing.T, options ...startOption) *process {
	t.Helper()

	scratch := t.TempDir()
	config := startConfig{environment: map[string]string{
		"HOME":               scratch,
		"XDG_CONFIG_HOME":    filepath.Join(scratch, "config"),
		"OX_LOG_LEVEL":       "debug",
		"OX_MODEL":           "test/model",
		"OPENROUTER_API_KEY": "test-key",
		"GORACE":             "halt_on_error=1",
	}}
	for _, option := range options {
		option(&config)
	}

	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create ox stdout pipe: %v", err)
	}

	command := exec.Command(oxBinary)
	command.Dir = scratch
	command.Env = environment(config.environment)
	command.Stdout = stdoutWriter

	stdin, err := command.StdinPipe()
	if err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		t.Fatalf("create ox stdin pipe: %v", err)
	}

	child := &process{
		t:       t,
		command: command,
		stdin:   stdin,
		stdout:  stdout,
		reader:  bufio.NewReader(stdout),
		cwd:     scratch,
		nextID:  1,
	}
	command.Stderr = &child.stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		t.Fatalf("start ox: %v", err)
	}
	if err := stdoutWriter.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = stdin.Close()
		_ = stdout.Close()
		t.Fatalf("close parent ox stdout writer: %v", err)
	}

	t.Cleanup(child.cleanup)
	return child
}

func environment(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[name]; !replaced {
			environment = append(environment, entry)
		}
	}
	for name, value := range overrides {
		environment = append(environment, name+"="+value)
	}
	return environment
}

func (p *process) request(method string, params any) json.RawMessage {
	p.t.Helper()
	return p.result(p.await(p.begin(method, params)))
}

func (p *process) requestError(method string, params any) rpcError {
	p.t.Helper()
	return p.failure(p.await(p.begin(method, params)))
}

// begin sends a request without waiting for its response, so a test can act
// while the handler runs.
func (p *process) begin(method string, params any) call {
	p.t.Helper()

	id := p.nextID
	p.nextID++
	p.writeJSON(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	return call{id: id, method: method}
}

func (p *process) await(sent call) message {
	p.t.Helper()

	wantID := strconv.Itoa(sent.id)
	return p.readUntil("response to "+sent.method, func(candidate message) bool {
		return string(candidate.ID) == wantID && candidate.Method == ""
	})
}

func (p *process) result(response message) json.RawMessage {
	p.t.Helper()
	if response.Error != nil {
		p.t.Fatalf("request returned JSON-RPC error %d (%s): %s",
			response.Error.Code, response.Error.Message, response.raw)
	}
	if response.Result == nil {
		p.t.Fatalf("response has no result: %s", response.raw)
	}
	return response.Result
}

func (p *process) failure(response message) rpcError {
	p.t.Helper()
	if response.Error == nil {
		p.t.Fatalf("request succeeded unexpectedly: %s", response.raw)
	}
	return *response.Error
}

func (p *process) notify(method string, params any) {
	p.t.Helper()
	p.writeJSON(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", Method: method, Params: params})
}

func (p *process) notification(method string) message {
	p.t.Helper()
	return p.readUntil("notification "+method, func(candidate message) bool {
		return candidate.Method == method && len(candidate.ID) == 0
	})
}

// notifications removes and returns the buffered notifications for method, in
// arrival order. Reading a response buffers everything that preceded it, so a
// caller that already has the response has every notification the agent sent
// before it.
func (p *process) notifications(method string) []message {
	p.t.Helper()
	var found []message
	remaining := p.pending[:0]
	for _, candidate := range p.pending {
		if candidate.Method == method && len(candidate.ID) == 0 {
			found = append(found, candidate)
			continue
		}
		remaining = append(remaining, candidate)
	}
	p.pending = remaining
	return found
}

//lint:ignore U1000 This harness operation is reserved for methods that call client methods.
func (p *process) serverRequest() message {
	p.t.Helper()
	return p.readUntil("agent-to-client request", func(candidate message) bool {
		return candidate.Method != "" && len(candidate.ID) != 0
	})
}

//lint:ignore U1000 This harness operation answers the future agent-to-client requests above.
func (p *process) respond(request message, result any) {
	p.t.Helper()
	if request.Method == "" || len(request.ID) == 0 {
		p.t.Fatalf("cannot respond to non-request: %s", request.raw)
	}
	p.writeJSON(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{JSONRPC: "2.0", ID: request.ID, Result: result})
}

func (p *process) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, strings.TrimSuffix(line, "\n")+"\n"); err != nil {
		p.t.Fatalf("write to ox: %v", err)
	}
}

func (p *process) writeJSON(value any) {
	p.t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		p.t.Fatalf("encode JSON-RPC message: %v", err)
	}
	p.send(string(encoded))
}

func (p *process) readUntil(what string, predicate func(message) bool) message {
	p.t.Helper()
	for index, candidate := range p.pending {
		if predicate(candidate) {
			p.pending = append(p.pending[:index], p.pending[index+1:]...)
			return candidate
		}
	}

	for {
		candidate, err := p.readMessage()
		if err != nil {
			p.t.Fatalf("read %s: %v", what, err)
		}
		if predicate(candidate) {
			return candidate
		}
		if candidate.Method != "" && len(candidate.ID) != 0 {
			p.t.Fatalf("unexpected agent-to-client request while waiting for %s: %s",
				what, candidate.raw)
		}
		p.pending = append(p.pending, candidate)
	}
}

func (p *process) readMessage() (message, error) {
	if err := p.stdout.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return message{}, fmt.Errorf("set stdout read deadline: %w", err)
	}
	line, err := p.reader.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return message{}, io.EOF
		}
		return message{}, fmt.Errorf("read stdout line: %w", err)
	}

	var decoded message
	if err := json.Unmarshal(bytes.TrimSpace(line), &decoded); err != nil {
		return message{}, fmt.Errorf("stdout was not JSON-RPC: %q: %w", line, err)
	}
	decoded.raw = bytes.TrimSpace(bytes.Clone(line))
	if decoded.JSONRPC != "2.0" {
		return message{}, fmt.Errorf("stdout has invalid JSON-RPC version: %s", decoded.raw)
	}
	if decoded.Method == "" && len(decoded.ID) == 0 {
		return message{}, fmt.Errorf("stdout has invalid JSON-RPC shape: %s", decoded.raw)
	}
	return decoded, nil
}

func (p *process) stop() {
	p.t.Helper()
	p.stopReported = true
	if err := p.shutdown(); err != nil {
		p.t.Fatalf("stop ox: %v", err)
	}
}

func (p *process) shutdown() error {
	if p.stopped {
		return p.stopErr
	}
	p.stopped = true

	var problems []error
	if err := p.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		problems = append(problems, fmt.Errorf("close ox stdin: %w", err))
	}

	for _, pending := range p.pending {
		problems = append(problems, fmt.Errorf("unconsumed stdout message: %s", pending.raw))
	}
	p.pending = nil
	for {
		remaining, err := p.readMessage()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("drain ox stdout: %w", err))
			_ = p.command.Process.Kill()
			break
		}
		problems = append(problems, fmt.Errorf("unconsumed stdout message: %s", remaining.raw))
	}

	killed := make(chan bool, 1)
	timer := time.AfterFunc(shutdownTimeout, func() {
		killed <- p.command.Process.Kill() == nil
	})
	waitErr := p.command.Wait()
	if !timer.Stop() && <-killed {
		problems = append(problems, errors.New("ox did not exit before shutdown deadline"))
	} else if waitErr != nil {
		problems = append(problems, fmt.Errorf("ox exit: %w", waitErr))
	}
	if err := p.stdout.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		problems = append(problems, fmt.Errorf("close ox stdout: %w", err))
	}

	p.stopErr = errors.Join(problems...)
	return p.stopErr
}

func (p *process) cleanup() {
	if err := p.shutdown(); err != nil && !p.stopReported {
		p.t.Errorf("stop ox: %v", err)
	}
	if p.t.Failed() {
		p.t.Logf("ox stderr:\n%s", p.stderr.String())
	}
}
