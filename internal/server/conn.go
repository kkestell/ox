package server

import (
	"encoding/json"
	"net"
	"sync"
	"time"
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
// is disconnected.
func (c *conn) queue(message any) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.out <- message:
	default:
		c.close()
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
