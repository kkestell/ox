package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
)

const (
	maxHeaderBytes  = 8 << 10
	maxMessageBytes = 4 << 20
)

type encodingKind string

const (
	encodingUTF8  encodingKind = "utf-8"
	encodingUTF16 encodingKind = "utf-16"
	encodingUTF32 encodingKind = "utf-32"
)

type publishedDiagnostics struct {
	URI         string
	Version     *int
	Diagnostics json.RawMessage
}

type document struct {
	version int
	text    string
}

type client struct {
	name            string
	cmd             *exec.Cmd
	rpc             *jrpc2.Client
	documentsMu     sync.Mutex
	documents       map[string]document
	publications    chan publishedDiagnostics
	encoding        encodingKind
	pullDiagnostics bool
	queryTimeout    time.Duration

	deadOnce sync.Once
	dead     chan struct{}
	deadMu   sync.Mutex
	deadErr  error
	waitDone chan struct{}
}

func startClient(ctx context.Context, manager *Manager, definition Definition) (*client, error) {
	command := exec.CommandContext(manager.ctx, definition.Command, definition.Args...)
	command.Dir = manager.root
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	client := &client{
		name: definition.Name, cmd: command,
		documents:    make(map[string]document),
		publications: make(chan publishedDiagnostics, 32), encoding: encodingUTF16,
		queryTimeout: manager.queryTimeout, dead: make(chan struct{}), waitDone: make(chan struct{}),
	}
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	client.rpc = jrpc2.NewClient(&boundedChannel{
		reader: bufio.NewReaderSize(stdout, maxHeaderBytes+1), stdout: stdout, stdin: stdin,
	}, &jrpc2.ClientOptions{
		OnNotify: client.notification,
		OnCallback: func(context.Context, *jrpc2.Request) (any, error) {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "method not supported")
		},
		OnCancel: func(rpc *jrpc2.Client, response *jrpc2.Response) {
			_ = rpc.Notify(context.Background(), "$/cancelRequest", map[string]any{"id": json.RawMessage(response.ID())})
		},
		OnStop: func(_ *jrpc2.Client, err error) {
			client.markDead(err)
			_ = command.Process.Kill()
		},
	})
	go func() {
		err := command.Wait()
		client.markDead(err)
		close(client.waitDone)
	}()

	initCtx, cancel := boundedContext(ctx, manager.initializeTimeout)
	defer cancel()
	result, err := client.request(initCtx, "initialize", map[string]any{
		"processId":        os.Getpid(),
		"rootUri":          pathToURI(manager.root),
		"workspaceFolders": []map[string]any{{"uri": pathToURI(manager.root), "name": "workspace"}},
		"capabilities": map[string]any{
			"general": map[string]any{"positionEncodings": []string{"utf-8", "utf-16", "utf-32"}},
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didOpen": true, "didChange": true},
				"publishDiagnostics": map[string]any{"versionSupport": true},
				"diagnostic":         map[string]any{},
			},
		},
	})
	if err != nil {
		_ = client.forceClose()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	var initialized struct {
		Capabilities struct {
			PositionEncoding   string          `json:"positionEncoding"`
			TextDocumentSync   json.RawMessage `json:"textDocumentSync"`
			DiagnosticProvider json.RawMessage `json:"diagnosticProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(result, &initialized); err != nil {
		_ = client.forceClose()
		return nil, fmt.Errorf("decode initialize result: %w", err)
	}
	if initialized.Capabilities.PositionEncoding != "" {
		client.encoding = encodingKind(initialized.Capabilities.PositionEncoding)
	}
	switch client.encoding {
	case encodingUTF8, encodingUTF16, encodingUTF32:
	default:
		_ = client.forceClose()
		return nil, fmt.Errorf("server selected unsupported position encoding %q", client.encoding)
	}
	if !supportsSynchronization(initialized.Capabilities.TextDocumentSync) {
		_ = client.forceClose()
		return nil, errors.New("server does not support text-document synchronization")
	}
	provider := strings.TrimSpace(string(initialized.Capabilities.DiagnosticProvider))
	client.pullDiagnostics = provider != "" && provider != "null" && provider != "false"
	if err := client.notify("initialized", map[string]any{}); err != nil {
		_ = client.forceClose()
		return nil, fmt.Errorf("send initialized: %w", err)
	}
	return client, nil
}

func supportsSynchronization(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var kind int
	if json.Unmarshal(raw, &kind) == nil {
		return kind == 1 || kind == 2
	}
	var options struct {
		Change int `json:"change"`
	}
	return json.Unmarshal(raw, &options) == nil && (options.Change == 1 || options.Change == 2)
}

func boundedContext(parent context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
	if duration <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, duration)
}

type boundedChannel struct {
	reader *bufio.Reader
	stdout io.ReadCloser
	stdin  io.WriteCloser
}

func (c *boundedChannel) Recv() ([]byte, error) { return readMessage(c.reader) }

func (c *boundedChannel) Send(body []byte) error {
	if len(body) > maxMessageBytes {
		return errors.New("LSP message exceeds limit")
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	_, err := c.stdin.Write(append([]byte(header), body...))
	return err
}

func (c *boundedChannel) Close() error {
	return errors.Join(c.stdin.Close(), c.stdout.Close())
}

func readMessage(reader *bufio.Reader) (json.RawMessage, error) {
	length := -1
	headerBytes := 0
	for {
		raw, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return nil, errors.New("LSP header exceeds limit")
		}
		line := string(raw)
		if err != nil {
			return nil, err
		}
		headerBytes += len(line)
		if headerBytes > maxHeaderBytes {
			return nil, errors.New("LSP header exceeds limit")
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, errors.New("malformed LSP header")
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 0 || parsed > maxMessageBytes {
				return nil, errors.New("invalid LSP Content-Length")
			}
			length = parsed
		}
	}
	if length < 0 {
		return nil, errors.New("missing LSP Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, errors.New("invalid LSP JSON message")
	}
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.JSONRPC != "2.0" {
		return nil, errors.New("malformed LSP JSON-RPC envelope")
	}
	return body, nil
}

func (c *client) notification(request *jrpc2.Request) {
	if request.Method() != "textDocument/publishDiagnostics" {
		return
	}
	var value struct {
		URI         string          `json:"uri"`
		Version     *int            `json:"version"`
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if err := request.UnmarshalParams(&value); err != nil || value.URI == "" || len(value.Diagnostics) == 0 {
		c.markDead(errors.New("malformed publishDiagnostics notification"))
		_ = c.cmd.Process.Kill()
		return
	}
	select {
	case c.publications <- publishedDiagnostics(value):
	default:
	}
}

func (c *client) notify(method string, params any) error {
	return c.rpc.Notify(context.Background(), method, params)
}

func (c *client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	response, err := c.rpc.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	result := json.RawMessage(response.ResultString())
	if len(result) == 0 {
		return nil, errors.New("LSP response omitted result")
	}
	return result, nil
}

func (c *client) markDead(err error) {
	c.deadOnce.Do(func() {
		if err == nil {
			err = errors.New("language server exited")
		}
		c.deadMu.Lock()
		c.deadErr = err
		c.deadMu.Unlock()
		close(c.dead)
	})
}

func (c *client) failure() error {
	c.deadMu.Lock()
	defer c.deadMu.Unlock()
	if c.deadErr == nil {
		return errors.New("language server is not running")
	}
	return fmt.Errorf("language server is not running: %w", c.deadErr)
}

func (c *client) query(ctx context.Context, method string, params any) (json.RawMessage, error) {
	queryCtx, cancel := boundedContext(ctx, c.queryTimeout)
	defer cancel()
	return c.request(queryCtx, method, params)
}

func (c *client) syncDocument(path, text string) (string, int, error) {
	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	doc := c.documents[path]
	doc.version++
	uri := pathToURI(path)
	var err error
	if doc.version == 1 {
		err = c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": languageID(path), "version": doc.version, "text": text,
		}})
	} else {
		err = c.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": doc.version},
			"contentChanges": []map[string]any{{"text": text}},
		})
	}
	if err != nil {
		return "", 0, err
	}
	doc.text = text
	c.documents[path] = doc
	return uri, doc.version, nil
}

func languageID(path string) string {
	return strings.TrimPrefix(strings.ToLower(filepathExtension(path)), ".")
}

func filepathExtension(path string) string {
	index := strings.LastIndexByte(path, '.')
	if index < strings.LastIndexAny(path, `/\\`) {
		return ""
	}
	if index < 0 {
		return ""
	}
	return path[index:]
}

func (c *client) locations(ctx context.Context, method, path, text string, position Position, includeDeclaration bool) (json.RawMessage, error) {
	uri, _, err := c.syncDocument(path, text)
	if err != nil {
		return nil, err
	}
	protocolPosition, err := encodePosition(text, position, c.encoding)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"textDocument": map[string]any{"uri": uri}, "position": protocolPosition}
	if method == "textDocument/references" {
		params["context"] = map[string]any{"includeDeclaration": includeDeclaration}
	}
	return c.query(ctx, method, params)
}

func (c *client) documentSymbols(ctx context.Context, path, text string) (json.RawMessage, error) {
	uri, _, err := c.syncDocument(path, text)
	if err != nil {
		return nil, err
	}
	return c.query(ctx, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}})
}

func (c *client) workspaceSymbols(ctx context.Context, query string) (json.RawMessage, error) {
	return c.query(ctx, "workspace/symbol", map[string]any{"query": query})
}

func (c *client) diagnostics(ctx context.Context, path, text string) (json.RawMessage, int, bool, error) {
	uri, version, err := c.syncDocument(path, text)
	if err != nil {
		return nil, 0, false, err
	}
	if c.pullDiagnostics {
		raw, err := c.query(ctx, "textDocument/diagnostic", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		})
		return raw, version, true, err
	}
	waitCtx, cancel := boundedContext(ctx, c.queryTimeout)
	defer cancel()
	seenStale := false
	seenUnversioned := false
	for {
		select {
		case publication := <-c.publications:
			if publication.URI != uri {
				continue
			}
			if publication.Version == nil {
				seenUnversioned = true
				continue
			}
			if *publication.Version != version {
				seenStale = true
				continue
			}
			var items []json.RawMessage
			if err := json.Unmarshal(publication.Diagnostics, &items); err != nil {
				return nil, version, false, errors.New("malformed published diagnostics")
			}
			if len(items) == 0 {
				return publication.Diagnostics, version, false, nil
			}
			return publication.Diagnostics, version, false, nil
		case <-waitCtx.Done():
			if seenStale && seenUnversioned {
				return nil, version, false, fmt.Errorf("diagnostics unavailable for document version %d: only stale and unversioned reports were observed", version)
			}
			if seenStale {
				return nil, version, false, fmt.Errorf("diagnostics unavailable for document version %d: only stale reports were observed", version)
			}
			if seenUnversioned {
				return nil, version, false, fmt.Errorf("diagnostics unavailable for document version %d: only unversioned reports were observed", version)
			}
			return nil, version, false, fmt.Errorf("diagnostics unavailable for document version %d: %w", version, waitCtx.Err())
		case <-c.dead:
			return nil, version, false, c.failure()
		}
	}
}

func (c *client) close(timeout time.Duration) error {
	defer c.rpc.Close()
	select {
	case <-c.dead:
		select {
		case <-c.waitDone:
		case <-time.After(timeout):
			_ = c.cmd.Process.Kill()
			<-c.waitDone
		}
		return nil
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	_, shutdownErr := c.request(ctx, "shutdown", nil)
	cancel()
	exitErr := c.notify("exit", nil)
	select {
	case <-c.waitDone:
	case <-time.After(timeout):
		_ = c.cmd.Process.Kill()
		<-c.waitDone
	}
	return errors.Join(shutdownErr, exitErr)
}

func (c *client) forceClose() error {
	defer c.rpc.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	<-c.waitDone
	return nil
}
