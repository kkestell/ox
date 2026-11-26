package server

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/fake"
	"github.com/kkestell/ox/internal/jsonrpc"
	"github.com/kkestell/ox/internal/store"
)

// newServer builds a server with New, with the system's temporary directory
// for worktrees. A configuration that names no titling
// harness titles sessions with a titler harness that echoes the prompt.
func newServer(st *store.Store, cfg *config.Config, parkAfter time.Duration, harnesses map[string]agent.Harness, mcp MCP) (*Server, error) {
	if cfg.Titling.Harness == "" {
		titled := *cfg
		titled.Titling.Harness = "titler"
		cfg = &titled
		harnesses = maps.Clone(harnesses)
		if harnesses == nil {
			harnesses = map[string]agent.Harness{}
		}
		harnesses["titler"] = agent.Harness{Ask: fake.Ask}
	}
	return New(st, cfg, parkAfter, harnesses, mcp, os.TempDir())
}

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *jsonrpc.Error  `json:"error"`
}

func (m message) sessionID() string {
	var params struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(m.Params, &params)
	return params.SessionID
}

type client struct {
	t        *testing.T
	conn     net.Conn
	messages chan message
	backlog  []message     // read but not yet matched, oldest first
	timeout  time.Duration // how long next waits for a match
	nextID   int
}

func connect(t *testing.T, s *Server) *client {
	t.Helper()
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

// received drains pending reads and returns this session's unmatched
// notifications, oldest first.
func (c *client) received(sessionID string) []message {
	c.drain()
	var mine []message
	for _, m := range c.backlog {
		if m.Method != "" && m.sessionID() == sessionID {
			mine = append(mine, m)
		}
	}
	return mine
}

// created drains pending reads and returns the unmatched session/created
// notifications, oldest first.
func (c *client) created() []message {
	c.drain()
	var matched []message
	for _, m := range c.backlog {
		if m.Method == "session/created" {
			matched = append(matched, m)
		}
	}
	return matched
}

// drain moves every message already read into the backlog.
func (c *client) drain() {
	c.t.Helper()
	for {
		select {
		case m, ok := <-c.messages:
			if !ok {
				c.t.Fatal("connection closed")
			}
			c.backlog = append(c.backlog, m)
		default:
			return
		}
	}
}

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

func (c *client) send(method string, params any) string {
	c.t.Helper()
	c.nextID++
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		c.t.Fatal(err)
	}
	return fmt.Sprint(c.nextID)
}

func (c *client) response(id string) message {
	c.t.Helper()
	return c.next(func(m message) bool { return string(m.ID) == id })
}

func (c *client) request(method string, params any) message {
	c.t.Helper()
	return c.response(c.send(method, params))
}

func (c *client) call(method string, params any) json.RawMessage {
	c.t.Helper()
	response := c.request(method, params)
	if response.Error != nil {
		c.t.Fatalf("%s: %+v", method, response.Error)
	}
	return response.Result
}

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

// attach connects as the MCP forwarder of a session.
func attach(t *testing.T, s *Server, sessionID string) *client {
	t.Helper()
	c := connect(t, s)
	c.call("mcp/attach", map[string]string{"session_id": sessionID})
	return c
}

// tool calls an MCP tool and returns its text and whether it is a refusal.
func (c *client) tool(name string, arguments any) (string, bool) {
	c.t.Helper()
	return c.toolUse("", name, arguments)
}

// toolUse calls an MCP tool as Claude Code does for the tool_use with
// toolUseID, and returns its text and whether it is a refusal.
func (c *client) toolUse(toolUseID, name string, arguments any) (string, bool) {
	c.t.Helper()
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	params := map[string]any{"name": name, "arguments": arguments}
	if toolUseID != "" {
		params["_meta"] = map[string]string{"claudecode/toolUseId": toolUseID}
	}
	raw := c.call("tools/call", params)
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Content) != 1 {
		c.t.Fatalf("%s returned %s", name, raw)
	}
	return result.Content[0].Text, result.IsError
}
