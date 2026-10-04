// Package acp is the Agent Client Protocol transport: JSON-RPC 2.0 messages,
// one per line, and the protocol types the Ox server sends and receives.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Error is a JSON-RPC error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if data, ok := e.Data.(string); ok {
		return e.Message + ": " + data
	}
	if e.Data != nil {
		data, _ := json.Marshal(e.Data)
		return fmt.Sprintf("%s: %s", e.Message, data)
	}
	return e.Message
}

func InvalidRequest(data string) *Error { return &Error{-32600, "Invalid request", data} }
func InvalidParams(data string) *Error  { return &Error{-32602, "Invalid params", data} }
func InternalError(data string) *Error  { return &Error{-32603, "Internal error", data} }

// ResourceNotFound names the missing resource.
func ResourceNotFound(uri string) *Error {
	return &Error{-32002, "Resource not found", map[string]string{"uri": uri}}
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Handler handles incoming messages in the order they arrive. A request
// handler must respond exactly once, and may do so later from another
// goroutine.
type Handler interface {
	HandleRequest(*Request)
	HandleNotification(method string, params json.RawMessage)
}

// Conn is one JSON-RPC connection.
type Conn struct {
	writeMu sync.Mutex
	out     io.Writer

	mu      sync.Mutex
	pending map[string]chan message
	closed  bool
}

// NewConn returns a connection that writes to out.
func NewConn(out io.Writer) *Conn {
	return &Conn{out: out, pending: map[string]chan message{}}
}

// Serve reads messages from in until EOF and dispatches them. Afterward,
// every outgoing request still waiting for a response fails.
func (c *Conn) Serve(in io.Reader, handler Handler) error {
	defer c.close()
	reader := bufio.NewReader(in)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			c.dispatch(line, handler)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (c *Conn) dispatch(line []byte, handler Handler) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		c.write(message{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &Error{-32700, "Parse error", err.Error()}})
		return
	}
	switch {
	case m.Method != "" && m.ID != nil:
		handler.HandleRequest(&Request{conn: c, id: m.ID, Method: m.Method, params: m.Params})
	case m.Method != "":
		handler.HandleNotification(m.Method, m.Params)
	case m.ID != nil:
		c.mu.Lock()
		response, ok := c.pending[string(m.ID)]
		delete(c.pending, string(m.ID))
		c.mu.Unlock()
		if ok {
			response <- m
		}
	}
}

func (c *Conn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, response := range c.pending {
		close(response)
		delete(c.pending, id)
	}
}

func (c *Conn) write(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.out.Write(append(data, '\n'))
	return err
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(message{JSONRPC: "2.0", Method: method, Params: data})
}

// Call sends a request and decodes its result. It fails when ctx is done
// first, when the peer returns an error, or when the connection closes.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	id, _ := json.Marshal(rand.Text())
	response := make(chan message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("the ACP connection closed")
	}
	c.pending[string(id)] = response
	c.mu.Unlock()
	if err := c.write(message{JSONRPC: "2.0", ID: id, Method: method, Params: data}); err != nil {
		c.forget(id)
		return err
	}
	select {
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case m, ok := <-response:
		if !ok {
			return errors.New("the ACP connection closed")
		}
		if m.Error != nil {
			return m.Error
		}
		return json.Unmarshal(m.Result, result)
	}
}

func (c *Conn) forget(id json.RawMessage) {
	c.mu.Lock()
	delete(c.pending, string(id))
	c.mu.Unlock()
}

// Request is one incoming request.
type Request struct {
	conn   *Conn
	id     json.RawMessage
	Method string
	params json.RawMessage
}

// Params decodes the request parameters.
func (r *Request) Params(value any) *Error {
	if err := json.Unmarshal(r.params, value); err != nil {
		return InvalidParams(err.Error())
	}
	return nil
}

// Respond sends the result.
func (r *Request) Respond(result any) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return r.conn.write(message{JSONRPC: "2.0", ID: r.id, Result: data})
}

// Fail sends the error.
func (r *Request) Fail(err *Error) error {
	return r.conn.write(message{JSONRPC: "2.0", ID: r.id, Error: err})
}
