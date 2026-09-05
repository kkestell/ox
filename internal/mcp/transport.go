package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/acp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type commandTransport struct {
	command string
	args    []string
	env     []acp.EnvVariable
	dir     string
}

func (t *commandTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	command := exec.Command(t.command, t.args...)
	command.Dir = t.dir
	command.Env = append([]string(nil), os.Environ()...)
	for _, variable := range t.env {
		prefix := variable.Name + "="
		command.Env = slices.DeleteFunc(command.Env, func(value string) bool {
			return strings.HasPrefix(value, prefix)
		})
		command.Env = append(command.Env, variable.Name+"="+variable.Value)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, err
	}
	base, err := (&sdk.IOTransport{Reader: &frameReader{reader: stdout}, Writer: stdin}).Connect(ctx)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	return &processConnection{Connection: base, command: command}, nil
}

type processConnection struct {
	sdk.Connection
	command *exec.Cmd
	once    sync.Once
	err     error
}

func (c *processConnection) Close() error {
	c.once.Do(func() {
		c.err = c.Connection.Close()
		wait := make(chan error, 1)
		go func() { wait <- c.command.Wait() }()
		select {
		case <-wait:
			return
		case <-time.After(time.Second):
			_ = c.command.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-wait:
		case <-time.After(time.Second):
			_ = c.command.Process.Kill()
			<-wait
		}
	})
	return c.err
}

type frameReader struct {
	reader io.Reader
	size   int
}

func (r *frameReader) Read(buffer []byte) (int, error) {
	if len(buffer) > MaxWireBytes+1-r.size {
		buffer = buffer[:MaxWireBytes+1-r.size]
	}
	n, err := r.reader.Read(buffer)
	for _, value := range buffer[:n] {
		if value == '\n' {
			r.size = 0
		} else {
			r.size++
			if r.size > MaxWireBytes {
				return 0, errors.New("MCP wire message exceeds 2 MiB")
			}
		}
	}
	return n, err
}

func (r *frameReader) Close() error {
	if closer, ok := r.reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func newHTTPTransport(endpoint string, headers []acp.HTTPHeader) sdk.Transport {
	values := make(http.Header, len(headers))
	for _, header := range headers {
		values.Set(header.Name, header.Value)
	}
	base := http.DefaultTransport
	origin, _ := url.Parse(endpoint)
	client := &http.Client{Transport: &headerTransport{
		base: base, headers: values, origin: origin,
		requests: &httpRequestTracker{active: make(map[string]trackedHTTPRequest)},
	}}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := validateRedirectTarget(request.URL); err != nil {
			return err
		}
		if len(via) > 0 && !sameOrigin(request.URL, via[0].URL) {
			for name := range values {
				request.Header.Del(name)
			}
		}
		return nil
	}
	return &sdk.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true,
	}
}

func validateRedirectTarget(target *url.URL) error {
	if target == nil || target.Host == "" || target.User != nil {
		return errors.New("MCP HTTP redirect target must be an absolute URL without credentials")
	}
	if target.Scheme == "https" {
		return nil
	}
	if target.Scheme != "http" || !isLocalRedirectHost(target.Hostname()) {
		return errors.New("MCP HTTP redirect target must use HTTPS unless it is local")
	}
	return nil
}

func isLocalRedirectHost(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type headerTransport struct {
	base     http.RoundTripper
	headers  http.Header
	origin   *url.URL
	requests *httpRequestTracker
}

func (t *headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	envelope := decodeHTTPRequestEnvelope(request)
	if t.requests != nil && envelope.Method == "notifications/cancelled" {
		t.requests.cancel(string(envelope.Params.RequestID))
	}
	requestContext := request.Context()
	requestID := string(envelope.ID)
	if t.requests != nil && len(envelope.ID) != 0 {
		var cancel context.CancelFunc
		requestContext, cancel = context.WithCancel(requestContext)
		t.requests.begin(requestID, cancel)
	}
	clone := request.Clone(requestContext)
	clone.Header = request.Header.Clone()
	if sameOrigin(clone.URL, t.origin) {
		for name, values := range t.headers {
			clone.Header.Del(name)
			for _, value := range values {
				clone.Header.Add(name, value)
			}
		}
	}
	response, err := t.base.RoundTrip(clone)
	if err != nil {
		if t.requests != nil && requestID != "" {
			t.requests.remove(requestID, nil)
		}
		return nil, err
	}
	body := newHTTPBodyReader(
		response.Body,
		strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream"),
		request.Context(),
	)
	response.Body = body
	if t.requests != nil && requestID != "" {
		body.onClose = func() { t.requests.remove(requestID, body) }
		t.requests.setBody(requestID, body)
	}
	return response, nil
}

type httpRequestEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		RequestID json.RawMessage `json:"requestId"`
	} `json:"params"`
}

func decodeHTTPRequestEnvelope(request *http.Request) httpRequestEnvelope {
	if request.GetBody == nil {
		return httpRequestEnvelope{}
	}
	body, err := request.GetBody()
	if err != nil {
		return httpRequestEnvelope{}
	}
	defer body.Close()
	var envelope httpRequestEnvelope
	_ = json.NewDecoder(body).Decode(&envelope)
	return envelope
}

type httpRequestTracker struct {
	mu     sync.Mutex
	active map[string]trackedHTTPRequest
}

type trackedHTTPRequest struct {
	cancel context.CancelFunc
	body   io.Closer
}

func (t *httpRequestTracker) begin(id string, cancel context.CancelFunc) {
	t.mu.Lock()
	t.active[id] = trackedHTTPRequest{cancel: cancel}
	t.mu.Unlock()
}

func (t *httpRequestTracker) setBody(id string, body io.Closer) {
	t.mu.Lock()
	request, exists := t.active[id]
	if exists {
		request.body = body
		t.active[id] = request
	}
	t.mu.Unlock()
	if !exists {
		_ = body.Close()
	}
}

func (t *httpRequestTracker) cancel(id string) {
	if id == "" || id == "null" {
		return
	}
	t.mu.Lock()
	request, exists := t.active[id]
	t.mu.Unlock()
	if exists {
		request.cancel()
		if request.body != nil {
			_ = request.body.Close()
		}
	}
}

func (t *httpRequestTracker) remove(id string, body io.Closer) {
	t.mu.Lock()
	request, exists := t.active[id]
	removed := exists && (body == nil || request.body == body)
	if removed {
		delete(t.active, id)
	}
	t.mu.Unlock()
	if removed {
		request.cancel()
	}
}

type httpBodyReader struct {
	reader  io.ReadCloser
	sse     bool
	size    int
	lastNL  bool
	once    sync.Once
	closed  chan struct{}
	onClose func()
}

func newHTTPBodyReader(reader io.ReadCloser, sse bool, ctx context.Context) *httpBodyReader {
	result := &httpBodyReader{reader: reader, sse: sse, closed: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = result.Close()
		case <-result.closed:
		}
	}()
	return result
}

func (r *httpBodyReader) Read(buffer []byte) (int, error) {
	if len(buffer) > MaxWireBytes+1-r.size {
		buffer = buffer[:MaxWireBytes+1-r.size]
	}
	n, err := r.reader.Read(buffer)
	for _, value := range buffer[:n] {
		r.size++
		if r.sse && value == '\n' {
			if r.lastNL {
				r.size = 0
			}
			r.lastNL = true
		} else if value != '\r' {
			r.lastNL = false
		}
		if r.size > MaxWireBytes {
			return 0, errors.New("MCP HTTP response frame exceeds 2 MiB")
		}
	}
	return n, err
}

func (r *httpBodyReader) Close() error {
	var err error
	r.once.Do(func() {
		err = r.reader.Close()
		if r.onClose != nil {
			r.onClose()
		}
		if r.closed != nil {
			close(r.closed)
		}
	})
	return err
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

var _ sdk.Connection = (*processConnection)(nil)
