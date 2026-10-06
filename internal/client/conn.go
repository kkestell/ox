// Package client is the terminal client's side of ACP: it starts a server,
// sends session requests, and delivers the server's messages in arrival order.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/acp"
	"ox/internal/settings"
)

// Event is a message from the server or the end of a prompt.
type Event interface{ event() }

// Update is a session update notification.
type Update struct {
	SessionID protocol.SessionId
	Update    protocol.SessionUpdate
	// Name is the tool name Ox adds to tool calls and tool call updates, or
	// empty.
	Name string
}

// Permission is a permission request waiting for an answer.
type Permission struct {
	SessionID protocol.SessionId
	ToolCall  protocol.ToolCallUpdate
	// Name is the tool name Ox adds to the tool call, or empty.
	Name    string
	Options []protocol.PermissionOption
	request *acp.Request
}

// Finished reports that a prompt's turn ended, with its error.
type Finished struct {
	SessionID protocol.SessionId
	Err       error
}

// Diagnostic is one line the server wrote to standard error.
type Diagnostic string

// Closed reports that the server closed the connection.
type Closed struct{}

func (Update) event()      {}
func (*Permission) event() {}
func (Finished) event()    {}
func (Diagnostic) event()  {}
func (Closed) event()      {}

func (p *Permission) respond(outcome protocol.RequestPermissionOutcome) error {
	return p.request.Respond(protocol.RequestPermissionResponse{Outcome: outcome})
}

// Cancel answers the request as cancelled.
func (p *Permission) Cancel() error {
	return p.respond(protocol.RequestPermissionOutcome{Cancelled: &protocol.RequestPermissionOutcomeCancelled{Outcome: "cancelled"}})
}

// Conn is a connection to an ACP server.
type Conn struct {
	rpc       *acp.Conn
	directory string
	// CanResume reports whether the server lists, loads, and closes sessions.
	CanResume bool
	stop      func()

	mu     sync.Mutex
	events []Event
	closed bool
	// ready holds a signal while events may be waiting.
	ready chan struct{}
}

// startupTimeout bounds initialization and the first session.
const startupTimeout = 30 * time.Second

// Start runs the server's command, initializes the connection, and creates a
// session in directory.
func Start(server settings.Server, directory, version string) (*Conn, protocol.NewSessionResponse, error) {
	cmd := exec.Command(server.Command, server.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, protocol.NewSessionResponse{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, protocol.NewSessionResponse{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, protocol.NewSessionResponse{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, protocol.NewSessionResponse{}, fmt.Errorf("starting %s: %w", server.Command, err)
	}
	c := newConn(stdout, stdin, directory)
	exited := make(chan struct{})
	go func() {
		lines := bufio.NewReader(stderr)
		for {
			line, err := lines.ReadString('\n')
			if line != "" {
				c.push(Diagnostic(strings.TrimSuffix(line, "\n")))
			}
			if err != nil {
				break
			}
		}
		cmd.Wait()
		close(exited)
	}()
	// The server shuts down when its input ends; one that does not is killed.
	c.stop = func() {
		stdin.Close()
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
			<-exited
		}
	}
	session, err := c.initialize(version)
	if err != nil {
		c.Close()
		return nil, protocol.NewSessionResponse{}, err
	}
	return c, session, nil
}

// Connect initializes a connection to a server that reads out and writes in,
// and creates a session in directory. Closing the connection closes out.
func Connect(in io.Reader, out io.WriteCloser, directory, version string) (*Conn, protocol.NewSessionResponse, error) {
	c := newConn(in, out, directory)
	session, err := c.initialize(version)
	if err != nil {
		c.Close()
		return nil, protocol.NewSessionResponse{}, err
	}
	return c, session, nil
}

func newConn(in io.Reader, out io.WriteCloser, directory string) *Conn {
	c := &Conn{rpc: acp.NewConn(out), directory: directory, stop: func() { out.Close() }, ready: make(chan struct{}, 1)}
	go c.rpc.Serve(in, c)
	return c
}

func (c *Conn) initialize(version string) (protocol.NewSessionResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	var initialized protocol.InitializeResponse
	request := protocol.InitializeRequest{ProtocolVersion: acp.ProtocolVersion, ClientInfo: &protocol.Implementation{Name: "ox", Version: version}}
	err := c.call(ctx, acp.MethodInitialize, request, &initialized)
	var session protocol.NewSessionResponse
	if err == nil {
		if initialized.ProtocolVersion != acp.ProtocolVersion {
			return session, fmt.Errorf("the server uses ACP version %d; ox supports only version %d", initialized.ProtocolVersion, acp.ProtocolVersion)
		}
		capabilities := initialized.AgentCapabilities
		c.CanResume = capabilities.LoadSession && capabilities.SessionCapabilities.List != nil && capabilities.SessionCapabilities.Close != nil
		err = c.call(ctx, acp.MethodNewSession, protocol.NewSessionRequest{Cwd: c.directory, McpServers: []protocol.McpServer{}}, &session)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return session, errors.New("server startup timed out")
	}
	return session, err
}

// Close stops the server.
func (c *Conn) Close() {
	c.stop()
}

// Next waits for the next event. Once the connection is closed and every
// earlier event is delivered, it returns Closed.
func (c *Conn) Next() Event {
	for {
		c.mu.Lock()
		if len(c.events) > 0 {
			event := c.events[0]
			c.events = c.events[1:]
			c.mu.Unlock()
			return event
		}
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return Closed{}
		}
		<-c.ready
	}
}

// push queues an event without blocking, since the connection reads every
// response on the goroutine that delivers requests and notifications.
func (c *Conn) push(event Event) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
	c.signal()
}

func (c *Conn) signal() {
	select {
	case c.ready <- struct{}{}:
	default:
	}
}

// HandleRequest queues permission requests and refuses the rest.
func (c *Conn) HandleRequest(r *acp.Request) {
	if r.Method != acp.MethodRequestPermission {
		r.Fail(&acp.Error{Code: -32601, Message: "Method not found", Data: r.Method})
		return
	}
	var params acp.RequestPermissionRequest
	if err := r.Params(&params); err != nil {
		r.Fail(err)
		return
	}
	c.push(&Permission{
		SessionID: params.SessionId, ToolCall: params.ToolCall.ToolCallUpdate,
		Name: params.ToolCall.Name, Options: params.Options, request: r,
	})
}

// HandleNotification queues session updates and ignores the rest.
func (c *Conn) HandleNotification(method string, params json.RawMessage) {
	var notification struct {
		SessionID protocol.SessionId `json:"sessionId"`
		Update    json.RawMessage    `json:"update"`
	}
	if method != acp.MethodUpdate || json.Unmarshal(params, &notification) != nil {
		return
	}
	var update protocol.SessionUpdate
	if json.Unmarshal(notification.Update, &update) != nil {
		return
	}
	var tool struct {
		Name string `json:"name"`
	}
	json.Unmarshal(notification.Update, &tool)
	c.push(Update{SessionID: notification.SessionID, Update: update, Name: tool.Name})
}

// Shutdown marks the connection closed when the server's output ends.
func (c *Conn) Shutdown() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.signal()
}

// NewSession creates a session in the connection's directory.
func (c *Conn) NewSession() (protocol.NewSessionResponse, error) {
	var response protocol.NewSessionResponse
	err := c.call(context.Background(), acp.MethodNewSession, protocol.NewSessionRequest{Cwd: c.directory, McpServers: []protocol.McpServer{}}, &response)
	return response, err
}

// LoadSession loads a saved session; the server replays it as updates before
// it responds.
func (c *Conn) LoadSession(id protocol.SessionId) (protocol.LoadSessionResponse, error) {
	var response protocol.LoadSessionResponse
	request := protocol.LoadSessionRequest{SessionId: id, Cwd: c.directory, McpServers: []protocol.McpServer{}}
	err := c.call(context.Background(), acp.MethodLoadSession, request, &response)
	return response, err
}

// ListSessions returns every saved session in the connection's directory.
func (c *Conn) ListSessions() ([]protocol.SessionInfo, error) {
	var sessions []protocol.SessionInfo
	var cursor *string
	for {
		var response protocol.ListSessionsResponse
		request := protocol.ListSessionsRequest{Cwd: &c.directory, Cursor: cursor}
		if err := c.call(context.Background(), acp.MethodListSessions, request, &response); err != nil {
			return nil, err
		}
		sessions = append(sessions, response.Sessions...)
		cursor = response.NextCursor
		if cursor == nil {
			return sessions, nil
		}
	}
}

// CloseSession closes a session.
func (c *Conn) CloseSession(id protocol.SessionId) error {
	return c.call(context.Background(), acp.MethodCloseSession, protocol.CloseSessionRequest{SessionId: id}, &protocol.CloseSessionResponse{})
}

// SetConfigOption sends a choice of a value of a session's select option and
// returns a function that waits for the session's options. Choices sent from
// one goroutine reach the server in order.
func (c *Conn) SetConfigOption(id protocol.SessionId, option protocol.SessionConfigId, value protocol.SessionConfigValueId) (func() ([]protocol.SessionConfigOption, error), error) {
	request := protocol.SetSessionConfigOptionRequest{ValueId: &protocol.SetSessionConfigOptionValueId{SessionId: id, ConfigId: option, Value: value}}
	wait, err := c.rpc.Dispatch(context.Background(), acp.MethodSetConfigOption, request)
	if err != nil {
		return nil, err
	}
	return func() ([]protocol.SessionConfigOption, error) {
		var response protocol.SetSessionConfigOptionResponse
		err := describe(wait(context.Background(), &response))
		return response.ConfigOptions, err
	}, nil
}

func (c *Conn) prompt(id protocol.SessionId, text string) error {
	request := protocol.PromptRequest{SessionId: id, Prompt: []protocol.ContentBlock{protocol.TextBlock(text)}}
	return c.call(context.Background(), acp.MethodPrompt, request, &protocol.PromptResponse{})
}

func (c *Conn) cancel(id protocol.SessionId) error {
	return c.rpc.Notify(acp.MethodCancel, protocol.CancelNotification{SessionId: id})
}

// call sends a request and describes an error response.
func (c *Conn) call(ctx context.Context, method string, params, result any) error {
	return describe(c.rpc.Call(ctx, method, params, result))
}

// describe describes an error response as its message and data.
func describe(err error) error {
	var response *acp.Error
	if !errors.As(err, &response) {
		return err
	}
	message := response.Message
	if message == "" {
		message = fmt.Sprint(response.Code)
	}
	if response.Data != nil {
		data, _ := json.MarshalIndent(response.Data, "", "  ")
		message += ": " + string(data)
	}
	return errors.New(message)
}
