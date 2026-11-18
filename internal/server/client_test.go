package server

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/jsonrpc"
)

// message is one line from oxd: a response or a notification.
type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *jsonrpc.Error  `json:"error"`
}

// sessionID returns the session a notification is about, if any.
func (m message) sessionID() string {
	var params struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(m.Params, &params)
	return params.SessionID
}

// client is one connection to a server, read on its own goroutine.
type client struct {
	t        *testing.T
	conn     net.Conn
	messages chan message
	backlog  []message     // read but not yet matched, oldest first
	timeout  time.Duration // how long next waits for a match
	nextID   int
}

func connect(t *testing.T, s *Server) *client {
	serverSide, clientSide := net.Pipe()
	go s.serveConn(serverSide)
	c := &client{t: t, conn: clientSide, messages: make(chan message, 1024), timeout: 5 * time.Second}
	go func() {
		decoder := json.NewDecoder(clientSide)
		for {
			var m message
			if decoder.Decode(&m) != nil {
				close(c.messages)
				return
			}
			c.messages <- m
		}
	}()
	t.Cleanup(func() { clientSide.Close() })
	return c
}

// next returns the oldest message that matches, keeping the others for later
// calls.
func (c *client) next(matches func(message) bool) message {
	c.t.Helper()
	for i, m := range c.backlog {
		if matches(m) {
			c.backlog = append(c.backlog[:i], c.backlog[i+1:]...)
			return m
		}
	}
	timeout := time.After(c.timeout)
	for {
		select {
		case m, ok := <-c.messages:
			if !ok {
				c.t.Fatal("connection closed")
			}
			if matches(m) {
				return m
			}
			c.backlog = append(c.backlog, m)
		case <-timeout:
			c.t.Fatalf("no matching message within %v", c.timeout)
		}
	}
}

// received returns every notification about the session read so far,
// oldest first, including those already matched by nothing.
func (c *client) received(sessionID string) []message {
	for {
		select {
		case m, ok := <-c.messages:
			if !ok {
				c.t.Fatal("connection closed")
			}
			c.backlog = append(c.backlog, m)
			continue
		default:
		}
		break
	}
	var mine []message
	for _, m := range c.backlog {
		if m.Method != "" && m.sessionID() == sessionID {
			mine = append(mine, m)
		}
	}
	return mine
}

// forget discards the unmatched notifications about the session.
func (c *client) forget(sessionID string) {
	c.received(sessionID)
	kept := c.backlog[:0]
	for _, m := range c.backlog {
		if m.Method == "" || m.sessionID() != sessionID {
			kept = append(kept, m)
		}
	}
	c.backlog = kept
}

// send writes a request and returns its ID without waiting for the response.
func (c *client) send(method string, params any) string {
	c.t.Helper()
	c.nextID++
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		c.t.Fatal(err)
	}
	return fmt.Sprint(c.nextID)
}

// response waits for the response to the request with id.
func (c *client) response(id string) message {
	c.t.Helper()
	return c.next(func(m message) bool { return string(m.ID) == id })
}

func (c *client) request(method string, params any) message {
	c.t.Helper()
	return c.response(c.send(method, params))
}

// call sends a request and returns its result, failing on an error.
func (c *client) call(method string, params any) json.RawMessage {
	c.t.Helper()
	response := c.request(method, params)
	if response.Error != nil {
		c.t.Fatalf("%s: %+v", method, response.Error)
	}
	return response.Result
}

// update returns the session's next session/update of the given type.
func (c *client) update(sessionID, kind string) agent.Update {
	c.t.Helper()
	var u agent.Update
	c.next(func(m message) bool {
		if m.Method != "session/update" || m.sessionID() != sessionID {
			return false
		}
		u = agent.Update{}
		json.Unmarshal(m.Params, &u)
		return u.Type == kind
	})
	return u
}
