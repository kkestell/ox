package mcp

import (
	"context"
	"errors"
	"io"
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
	client := &http.Client{Transport: headerTransport{base: base, headers: values, origin: origin}}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
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

type headerTransport struct {
	base    http.RoundTripper
	headers http.Header
	origin  *url.URL
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
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
		return nil, err
	}
	response.Body = &httpBodyReader{reader: response.Body, sse: strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream")}
	return response, nil
}

type httpBodyReader struct {
	reader io.ReadCloser
	sse    bool
	size   int
	lastNL bool
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

func (r *httpBodyReader) Close() error { return r.reader.Close() }

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

var _ sdk.Connection = (*processConnection)(nil)
