// Package acp adapts the JSON-RPC transport to Ox's ordered request handling.
package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"

	protocol "github.com/coder/acp-go-sdk"
	"github.com/sourcegraph/jsonrpc2"
)

type Error = protocol.RequestError

var (
	InvalidRequest = protocol.NewInvalidRequest
	InvalidParams  = protocol.NewInvalidParams
	InternalError  = protocol.NewInternalError
)

func ResourceNotFound(uri string) *Error {
	return &Error{Code: -32002, Message: "Resource not found", Data: map[string]string{"uri": uri}}
}

// Handler reserves operations in receive order and includes response writes
// in their lifetimes. Shutdown cancels and drains them before transport close.
type Handler interface {
	HandleRequest(*Request)
	HandleNotification(string, json.RawMessage)
	Shutdown()
}

type Conn struct {
	out   io.Writer
	rpc   *jsonrpc2.Conn
	ready chan struct{}
}

func NewConn(out io.Writer) *Conn { return &Conn{out: out, ready: make(chan struct{})} }

func (c *Conn) Serve(in io.Reader, handler Handler) error {
	stream := &drainingStream{ObjectStream: jsonrpc2.NewPlainObjectStream(stdio{in, c.out}), shutdown: handler.Shutdown}
	c.rpc = jsonrpc2.NewConn(context.Background(), stream, rpcHandler{c, handler}, jsonrpc2.SetLogger(log.New(io.Discard, "", 0)))
	close(c.ready)
	<-c.rpc.DisconnectNotify()
	if errors.Is(stream.err, io.EOF) {
		return nil
	}
	return stream.err
}

type rpcHandler struct {
	conn    *Conn
	handler Handler
}

func (h rpcHandler) Handle(_ context.Context, conn *jsonrpc2.Conn, r *jsonrpc2.Request) {
	<-h.conn.ready
	params := json.RawMessage("null")
	if r.Params != nil {
		params = *r.Params
	}
	if r.Notif {
		h.handler.HandleNotification(r.Method, params)
	} else {
		h.handler.HandleRequest(&Request{conn: conn, id: r.ID, Method: r.Method, params: params})
	}
}

type stdio struct {
	io.Reader
	io.Writer
}

func (stdio) Close() error { return nil }

type drainingStream struct {
	jsonrpc2.ObjectStream
	shutdown func()
	err      error
}

func (s *drainingStream) ReadObject(value any) error {
	if err := s.ObjectStream.ReadObject(value); err != nil {
		s.err = err
		s.shutdown()
		return err
	}
	return nil
}

func (c *Conn) Notify(method string, params any) error {
	<-c.ready
	return c.rpc.Notify(context.Background(), method, params)
}

func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	wait, err := c.Dispatch(ctx, method, params)
	if err != nil {
		return err
	}
	return wait(ctx, result)
}

// Dispatch sends a request and returns a function that waits for its result,
// so requests dispatched from one goroutine are sent in order.
func (c *Conn) Dispatch(ctx context.Context, method string, params any) (func(ctx context.Context, result any) error, error) {
	<-c.ready
	waiter, err := c.rpc.DispatchCall(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, result any) error { return responseError(waiter.Wait(ctx, result)) }, nil
}

func responseError(err error) error {
	var rpcErr *jsonrpc2.Error
	if errors.As(err, &rpcErr) {
		response := &Error{Code: int(rpcErr.Code), Message: rpcErr.Message}
		if rpcErr.Data != nil {
			json.Unmarshal(*rpcErr.Data, &response.Data)
		}
		return response
	}
	return err
}

type Request struct {
	conn   *jsonrpc2.Conn
	id     jsonrpc2.ID
	Method string
	params json.RawMessage
}

func (r *Request) Params(value any) *Error {
	if err := json.Unmarshal(r.params, value); err != nil {
		return InvalidParams(err.Error())
	}
	return nil
}
func (r *Request) Respond(result any) error { return r.conn.Reply(context.Background(), r.id, result) }
func (r *Request) Fail(err *Error) error {
	response := &jsonrpc2.Error{Code: int64(err.Code), Message: err.Message}
	if err.Data != nil {
		response.SetError(err.Data)
	}
	return r.conn.ReplyWithError(context.Background(), r.id, response)
}
