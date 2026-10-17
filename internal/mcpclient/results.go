package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type resultKey struct{}

// The SDK decodes schemas and structured content through float64 and rejects
// unfamiliar content blocks. Keep these payloads raw while the SDK owns request
// matching, protocol errors, and cancellation.
type resultTransport struct{ mcp.Transport }

func (t resultTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &resultConnection{Connection: conn, pending: map[jsonrpc.ID]chan json.RawMessage{}}, nil
}

type resultConnection struct {
	mcp.Connection
	mu      sync.Mutex
	pending map[jsonrpc.ID]chan json.RawMessage
}

func (c *resultConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if result, ok := ctx.Value(resultKey{}).(chan json.RawMessage); ok {
		if request, ok := message.(*jsonrpc.Request); ok && request.IsCall() {
			c.mu.Lock()
			c.pending[request.ID] = result
			c.mu.Unlock()
			context.AfterFunc(ctx, func() {
				c.mu.Lock()
				delete(c.pending, request.ID)
				c.mu.Unlock()
			})
		}
	}
	return c.Connection.Write(ctx, message)
}

func (c *resultConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := c.Connection.Read(ctx)
	if response, ok := message.(*jsonrpc.Response); ok {
		c.mu.Lock()
		result := c.pending[response.ID]
		delete(c.pending, response.ID)
		c.mu.Unlock()
		if result != nil && response.Error == nil {
			result <- response.Result
			response.Result = json.RawMessage(`{}`)
		}
	}
	return message, err
}

func receiveResult(ctx context.Context, call func(context.Context) error, result any) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	received := make(chan json.RawMessage, 1)
	if err := call(context.WithValue(ctx, resultKey{}, received)); err != nil {
		return err
	}
	select {
	case raw := <-received:
		if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
			return errors.New("MCP result must be a JSON object")
		}
		return json.Unmarshal(raw, result)
	default:
		return errors.New("MCP response payload is missing")
	}
}
