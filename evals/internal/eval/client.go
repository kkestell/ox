package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kkestell/ox/internal/acp"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	raw     json.RawMessage
}

type protocolStats struct {
	Answer      strings.Builder
	CostUSD     *float64
	Permissions int
}

type processClient struct {
	command  *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   *os.File
	events   *eventWriter
	messages chan rpcMessage
	readErr  chan error
	nextID   int
	writeMu  sync.Mutex
	stats    protocolStats
}

type eventWriter struct {
	mu   sync.Mutex
	file *os.File
}

func (w *eventWriter) write(direction string, raw []byte) {
	var message json.RawMessage = append([]byte(nil), raw...)
	line, err := json.Marshal(struct {
		TimestampMS int64           `json:"timestamp_ms"`
		Direction   string          `json:"direction"`
		Message     json.RawMessage `json:"message"`
	}{time.Now().UnixMilli(), direction, message})
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.file.Write(append(line, '\n'))
}

func startProcess(binary, workspace, private, model, baseURL, credential string, ordinal int) (*processClient, error) {
	for _, directory := range []string{"home", "config", "cache", "data"} {
		if err := os.MkdirAll(filepath.Join(private, directory), 0o700); err != nil {
			return nil, fmt.Errorf("create process directory: %w", err)
		}
	}
	catalogDirectory := filepath.Join(private, "cache", "ox")
	if err := os.MkdirAll(catalogDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create model cache: %w", err)
	}
	catalog, _ := json.Marshal(map[string]any{"data": []map[string]any{{"id": model, "context_length": 128000}}})
	if err := os.WriteFile(filepath.Join(catalogDirectory, "models.json"), catalog, 0o600); err != nil {
		return nil, fmt.Errorf("write model cache: %w", err)
	}

	eventsFile, err := os.OpenFile(filepath.Join(private, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open ACP event artifact: %w", err)
	}
	stderr, err := os.OpenFile(filepath.Join(private, "stderr.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		_ = eventsFile.Close()
		return nil, fmt.Errorf("open stderr artifact: %w", err)
	}
	tracePath := filepath.Join(private, fmt.Sprintf("trace-%d.jsonl", ordinal))
	command := exec.Command(binary, "--trace", tracePath)
	command.Dir = workspace
	command.Env = evaluationEnvironment(private, model, baseURL, credential)
	command.Stderr = stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		_ = eventsFile.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("open Ox stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = eventsFile.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("open Ox stdout: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = eventsFile.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start Ox: %w", err)
	}
	client := &processClient{
		command: command, stdin: stdin, stdout: stdout, stderr: stderr,
		events: &eventWriter{file: eventsFile}, messages: make(chan rpcMessage),
		readErr: make(chan error, 1), nextID: 1,
	}
	go client.readLoop()
	return client, nil
}

func evaluationEnvironment(private, model, baseURL, credential string) []string {
	result := sanitizedEnvironment()
	result = append(result,
		"HOME="+filepath.Join(private, "home"),
		"XDG_CACHE_HOME="+filepath.Join(private, "cache"),
		"XDG_CONFIG_HOME="+filepath.Join(private, "config"),
		"XDG_DATA_HOME="+filepath.Join(private, "data"),
		"OX_KEYRING_DISABLED=1", "OX_LOG_LEVEL=info", "OX_MODEL="+model,
		"OX_OPENROUTER_BASE_URL="+baseURL, "OPENROUTER_API_KEY="+credential,
	)
	return result
}

func sanitizedEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if name == "HOME" || strings.HasPrefix(name, "XDG_") || strings.HasPrefix(name, "OX_") ||
			strings.HasSuffix(upper, "_API_KEY") || strings.HasSuffix(upper, "_TOKEN") ||
			strings.HasSuffix(upper, "_SECRET") || strings.HasSuffix(upper, "_PASSWORD") ||
			strings.HasSuffix(upper, "_CREDENTIAL") {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (c *processClient) readLoop() {
	reader := bufio.NewReader(c.stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			c.readErr <- err
			return
		}
		line = []byte(strings.TrimSpace(string(line)))
		var message rpcMessage
		if err := json.Unmarshal(line, &message); err != nil || message.JSONRPC != "2.0" {
			c.readErr <- fmt.Errorf("invalid JSON-RPC from Ox: %q", line)
			return
		}
		message.raw = append([]byte(nil), line...)
		c.events.write("received", line)
		c.messages <- message
	}
}

func (c *processClient) call(ctx context.Context, method string, params any, permission string, sessionID string, cancelAfter time.Duration) (json.RawMessage, bool, error) {
	id := c.nextID
	c.nextID++
	if err := c.send(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, method, params}); err != nil {
		return nil, false, err
	}
	var cancel <-chan time.Time
	var timer *time.Timer
	if cancelAfter > 0 {
		timer = time.NewTimer(cancelAfter)
		cancel = timer.C
		defer timer.Stop()
	}
	cancelled := false
	deadline := ctx.Done()
	var grace <-chan time.Time
	for {
		select {
		case message := <-c.messages:
			if message.Method != "" && len(message.ID) != 0 {
				if err := c.respondToRequest(message, permission); err != nil {
					return nil, cancelled, err
				}
				continue
			}
			if message.Method == "session/update" {
				c.captureUpdate(message.Params)
				continue
			}
			if string(message.ID) != strconv.Itoa(id) {
				return nil, cancelled, fmt.Errorf("unexpected response id %s while waiting for %d", message.ID, id)
			}
			if message.Error != nil {
				return nil, cancelled, fmt.Errorf("%s failed (%d): %s", method, message.Error.Code, message.Error.Message)
			}
			return message.Result, cancelled, nil
		case err := <-c.readErr:
			return nil, cancelled, fmt.Errorf("read Ox output: %w", err)
		case <-cancel:
			if !cancelled && sessionID != "" {
				cancelled = true
				if err := c.notify("session/cancel", acp.CancelNotification{SessionID: sessionID}); err != nil {
					return nil, cancelled, err
				}
			}
			cancel = nil
		case <-deadline:
			if !cancelled && sessionID != "" {
				cancelled = true
				_ = c.notify("session/cancel", acp.CancelNotification{SessionID: sessionID})
				deadline = nil
				grace = time.After(2 * time.Second)
				continue
			}
			return nil, cancelled, ctx.Err()
		case <-grace:
			return nil, cancelled, ctx.Err()
		}
	}
}

func (c *processClient) respondToRequest(message rpcMessage, permission string) error {
	if message.Method != "session/request_permission" {
		return c.send(struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Error   rpcError        `json:"error"`
		}{"2.0", message.ID, rpcError{Code: -32601, Message: "unsupported evaluation client method"}})
	}
	var request acp.RequestPermissionRequest
	if err := json.Unmarshal(message.Params, &request); err != nil {
		return fmt.Errorf("decode permission request: %w", err)
	}
	outcome := permissionOutcome(request.Options, permission)
	c.stats.Permissions++
	return c.send(struct {
		JSONRPC string                        `json:"jsonrpc"`
		ID      json.RawMessage               `json:"id"`
		Result  acp.RequestPermissionResponse `json:"result"`
	}{"2.0", message.ID, acp.RequestPermissionResponse{Outcome: outcome}})
}

func permissionOutcome(options []acp.PermissionOption, permission string) acp.RequestPermissionOutcome {
	wantAllow := permission == "allow"
	optionID := ""
	for _, option := range options {
		allow := option.Kind == acp.PermissionOptionAllowOnce || option.Kind == acp.PermissionOptionAllowAlways
		if allow == wantAllow {
			optionID = option.OptionID
			break
		}
	}
	outcome := acp.RequestPermissionOutcome{Outcome: "cancelled"}
	if optionID != "" {
		outcome = acp.RequestPermissionOutcome{Outcome: "selected", OptionID: optionID}
	}
	return outcome
}

func (c *processClient) captureUpdate(raw json.RawMessage) {
	var notification struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(raw, &notification) != nil {
		return
	}
	var kind struct {
		SessionUpdate string `json:"sessionUpdate"`
		Content       *struct {
			Text string `json:"text"`
		} `json:"content,omitempty"`
		Cost *acp.Cost `json:"cost,omitempty"`
	}
	if json.Unmarshal(notification.Update, &kind) != nil {
		return
	}
	if kind.SessionUpdate == acp.SessionUpdateAgentMessageChunk && kind.Content != nil {
		c.stats.Answer.WriteString(kind.Content.Text)
	}
	if kind.Cost != nil && kind.Cost.Currency == "USD" {
		value := kind.Cost.Amount
		c.stats.CostUSD = &value
	}
}

func (c *processClient) send(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.events.write("sent", raw)
	_, err = c.stdin.Write(append(raw, '\n'))
	return err
}

func (c *processClient) notify(method string, params any) error {
	return c.send(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", method, params})
}

func (c *processClient) stop() error {
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.command.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(3 * time.Second):
		_ = c.command.Process.Kill()
		<-done
		waitErr = errors.New("ox did not exit before shutdown deadline")
	}
	closeErr := errors.Join(closeUnlessClosed(c.stdout), closeUnlessClosed(c.stderr), closeUnlessClosed(c.events.file))
	if waitErr != nil {
		return errors.Join(fmt.Errorf("ox exit: %w", waitErr), closeErr)
	}
	return closeErr
}

func closeUnlessClosed(closer io.Closer) error {
	err := closer.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
