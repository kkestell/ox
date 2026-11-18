package server

import (
	"encoding/json"
	"net"
	"sync"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/jsonrpc"
)

// conn is one client connection. Responses and notifications go through a
// queue drained by a single writer, so agents never wait on a client.
type conn struct {
	net       net.Conn
	out       chan any
	done      chan struct{}
	closeOnce sync.Once
}

func newConn(c net.Conn) *conn {
	cn := &conn{net: c, out: make(chan any, 1024), done: make(chan struct{})}
	go cn.write()
	return cn
}

// queue sends a message without blocking. A client too far behind to take it
// is disconnected. It reports false once the connection is closed.
func (c *conn) queue(message any) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.out <- message:
		return true
	default:
		c.close()
		return false
	}
}

func (c *conn) write() {
	encoder := json.NewEncoder(c.net)
	for {
		select {
		case message := <-c.out:
			if c.net.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil || encoder.Encode(message) != nil {
				c.close()
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.net.Close()
	})
}

// subscription delivers one agent's updates to a connection.
type subscription struct {
	conn    *conn
	agentID string
}

func (s subscription) Publish(update agent.Update) bool {
	return s.conn.queue(jsonrpc.Notify("agent/update", struct {
		AgentID string `json:"agent_id"`
		agent.Update
	}{s.agentID, update}))
}
