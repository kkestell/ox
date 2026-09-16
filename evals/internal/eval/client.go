package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/kkestell/ox/internal/acp"
)

type protocolStats struct {
	Answer               strings.Builder
	CostUSD              *float64
	Permissions          int
	PermissionRejections int
}

type processClient struct {
	command             *exec.Cmd
	stdin               io.WriteCloser
	stdout              io.ReadCloser
	stderr              *os.File
	events              *eventWriter
	transport           *eventChannel
	rpc                 *jrpc2.Client
	callMu              sync.Mutex
	statsMu             sync.Mutex
	permission          string
	stats               protocolStats
	credentialDirectory string
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
	catalog, _ := json.Marshal(map[string]any{"data": []map[string]any{{
		"id": model, "context_length": 128000,
		"supported_parameters": []string{"tools", "temperature", "max_tokens"},
	}}})
	if err := os.WriteFile(filepath.Join(catalogDirectory, "models.json"), catalog, 0o600); err != nil {
		return nil, fmt.Errorf("write model cache: %w", err)
	}
	settingsDirectory := filepath.Join(private, "config", "ox")
	if err := os.MkdirAll(settingsDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create settings directory: %w", err)
	}
	settings, _ := json.Marshal(map[string]any{
		"models": map[string]any{model: map[string]any{}},
	})
	if err := os.WriteFile(filepath.Join(settingsDirectory, "settings.json"), settings, 0o600); err != nil {
		return nil, fmt.Errorf("write settings file: %w", err)
	}
	credentialDirectory, err := os.MkdirTemp("", "ox-eval-credential-")
	if err != nil {
		return nil, fmt.Errorf("create credential directory: %w", err)
	}
	keepCredential := false
	defer func() {
		if !keepCredential {
			_ = os.RemoveAll(credentialDirectory)
		}
	}()
	credentialPath := filepath.Join(credentialDirectory, "credential")
	if err := os.WriteFile(credentialPath, []byte(strings.TrimSpace(credential)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write credential file: %w", err)
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
	command := exec.Command(
		binary,
		"--trace", tracePath,
		"--model", model,
		"--openrouter-base-url", baseURL,
		"--credential-file", credentialPath,
		"--no-keyring",
	)
	command.Dir = workspace
	command.Env = evaluationEnvironment(private)
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
		events: &eventWriter{file: eventsFile}, credentialDirectory: credentialDirectory,
	}
	keepCredential = true
	client.transport = &eventChannel{Channel: channel.Line(stdout, stdin), events: client.events, client: client}
	client.rpc = jrpc2.NewClient(client.transport, &jrpc2.ClientOptions{
		OnCallback: client.respondToRequest,
	})
	return client, nil
}

func evaluationEnvironment(private string) []string {
	result := sanitizedEnvironment()
	result = append(result,
		"HOME="+filepath.Join(private, "home"),
		"XDG_CACHE_HOME="+filepath.Join(private, "cache"),
		"XDG_CONFIG_HOME="+filepath.Join(private, "config"),
		"XDG_DATA_HOME="+filepath.Join(private, "data"),
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

type eventChannel struct {
	channel.Channel
	events *eventWriter
	client *processClient
	mu     sync.Mutex
	err    error
}

func (c *eventChannel) Send(raw []byte) error {
	c.events.write("sent", raw)
	err := c.Channel.Send(raw)
	c.recordError(err)
	return err
}

func (c *eventChannel) Recv() ([]byte, error) {
	raw, err := c.Channel.Recv()
	c.recordError(err)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.JSONRPC != "2.0" {
		err := fmt.Errorf("invalid JSON-RPC from Ox: %q", raw)
		c.recordError(err)
		return nil, err
	}
	c.events.write("received", raw)
	// jrpc2 dispatches received messages concurrently. Capture updates in wire
	// order so a prompt response cannot overtake its final answer or cost update.
	if envelope.Method == "session/update" {
		c.client.captureUpdate(envelope.Params)
	}
	return raw, nil
}

func (c *eventChannel) recordError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
}

func (c *eventChannel) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *processClient) call(ctx context.Context, method string, params any, permission string, sessionID string, cancelAfter time.Duration) (json.RawMessage, bool, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	c.statsMu.Lock()
	c.permission = permission
	c.statsMu.Unlock()
	callCtx, cancelCall := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelCall()
	type outcome struct {
		raw json.RawMessage
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		response, err := c.rpc.Call(callCtx, method, params)
		if err != nil && c.transport.failure() != nil {
			err = c.transport.failure()
		}
		var raw json.RawMessage
		if err == nil {
			raw = json.RawMessage(response.ResultString())
			if len(raw) == 0 {
				err = errors.New("response omitted result")
			}
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", method, err)
		}
		done <- outcome{raw, err}
	}()
	var timer *time.Timer
	var cancel <-chan time.Time
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
		case result := <-done:
			return result.raw, cancelled, result.err
		case <-cancel:
			if !cancelled && sessionID != "" {
				cancelled = true
				if err := c.notify("session/cancel", acp.CancelNotification{SessionID: sessionID}); err != nil {
					return nil, cancelled, err
				}
			}
			cancel = nil
		case <-deadline:
			if sessionID == "" {
				return nil, cancelled, ctx.Err()
			}
			if !cancelled {
				cancelled = true
				_ = c.notify("session/cancel", acp.CancelNotification{SessionID: sessionID})
			}
			deadline = nil
			grace = time.After(2 * time.Second)
		case <-grace:
			return nil, cancelled, ctx.Err()
		}
	}
}

func (c *processClient) respondToRequest(_ context.Context, message *jrpc2.Request) (any, error) {
	if message.Method() != "session/request_permission" {
		return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unsupported evaluation client method")
	}
	var request acp.RequestPermissionRequest
	if err := message.UnmarshalParams(&request); err != nil {
		return nil, fmt.Errorf("decode permission request: %w", err)
	}
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	outcome := permissionOutcome(request.Options, c.permission)
	c.stats.Permissions++
	if permissionRejected(request.Options, outcome) {
		c.stats.PermissionRejections++
	}
	return acp.RequestPermissionResponse{Outcome: outcome}, nil
}

func permissionRejected(options []acp.PermissionOption, outcome acp.RequestPermissionOutcome) bool {
	if outcome.Outcome != "selected" {
		return false
	}
	for _, option := range options {
		if option.OptionID == outcome.OptionID {
			return option.Kind == acp.PermissionOptionRejectOnce || option.Kind == acp.PermissionOptionRejectAlways
		}
	}
	return false
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
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
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

func (c *processClient) notify(method string, params any) error {
	return c.rpc.Notify(context.Background(), method, params)
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
	closeErr := errors.Join(
		c.rpc.Close(),
		closeUnlessClosed(c.stdout),
		closeUnlessClosed(c.stderr),
		closeUnlessClosed(c.events.file),
		os.RemoveAll(c.credentialDirectory),
	)
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
