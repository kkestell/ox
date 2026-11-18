// Package protocol handles one JSON-RPC message per line.
package protocol

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/kkestell/ox/internal/agents"
)

const version = "2.0"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  *string         `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type client struct {
	id      string
	conn    net.Conn
	encoder *json.Encoder
	writeMu sync.Mutex
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Serve reads requests from a client and writes responses on the same stream.
func Serve(conn net.Conn, registry *agents.Registry) error {
	reader := bufio.NewReader(conn)
	c := &client{id: rand.Text(), conn: conn, encoder: json.NewEncoder(conn)}
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if reply := c.handle(line, registry); reply != nil {
			if err := c.write(reply); err != nil {
				return err
			}
		}
	}
}

func (c *client) write(message any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// A client that stops reading must release the agent's output reader.
	if err := c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := c.encoder.Encode(message); err != nil {
		c.conn.Close()
		return err
	}
	return nil
}

func (c *client) handle(line []byte, registry *agents.Registry) *response {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return failure(nil, -32700, "Parse error")
		}
		return failure(nil, -32600, "Invalid Request")
	}
	if req.JSONRPC != version || req.Method == nil || !validID(req.ID) ||
		(len(req.Params) != 0 && req.Params[0] != '{' && req.Params[0] != '[') {
		return failure(nil, -32600, "Invalid Request")
	}

	// Agent operations require a request ID so the client receives the outcome.
	if len(req.ID) == 0 {
		return nil
	}
	result, rpcErr := c.dispatch(registry, *req.Method, req.Params)
	return &response{JSONRPC: version, ID: req.ID, Result: result, Error: rpcErr}
}

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	// Unmarshal has already checked the JSON syntax, so only the type matters.
	switch id[0] {
	case '{', '[', 't', 'f':
		return false
	default:
		return true
	}
}

func failure(id json.RawMessage, code int, message string) *response {
	return &response{JSONRPC: version, ID: id, Error: &rpcError{Code: code, Message: message}}
}
