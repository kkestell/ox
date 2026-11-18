// Package server is the Ox daemon. It accepts client connections, keeps the
// stored workspaces and sessions, and turns each JSON-RPC request into a
// session operation. Each session runs on its own goroutine, which owns its
// settings, history, and harness process: a parked session has no process,
// and the next prompt resumes its conversation.
// Clients have no identity: any connection can use any session, and every
// connection receives every session's updates.
package server

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/jsonrpc"
	"github.com/kkestell/ox/internal/store"
)

type Server struct {
	store     *store.Store
	config    *config.Config
	harnesses map[string]agent.Start
	parkAfter time.Duration

	// mu guards which workspaces and sessions exist, and closed. A change to
	// them is broadcast, and an answer listing them is queued, while it is
	// held, so every connection sees both in one order. Nothing posts to a
	// session while holding it.
	mu         sync.Mutex
	workspaces []*workspace
	sessions   map[string]*session
	closed     bool

	connsMu sync.Mutex // guards conns; taken after mu
	conns   map[*conn]struct{}
}

type workspace struct {
	id        string
	path      string
	createdAt time.Time
	sessions  []*session // oldest first
}

// New loads the stored workspaces and sessions. Every session starts parked;
// harnesses start a session's process by its harness name, and a process
// parks once it has been unused for parkAfter. The configuration's harnesses
// are offered to clients.
func New(st *store.Store, cfg *config.Config, parkAfter time.Duration, harnesses map[string]agent.Start) (*Server, error) {
	stored, err := st.Workspaces()
	if err != nil {
		return nil, err
	}
	s := &Server{
		store: st, config: cfg, harnesses: harnesses, parkAfter: parkAfter,
		sessions: make(map[string]*session), conns: make(map[*conn]struct{}),
	}
	for _, w := range stored {
		ws := &workspace{id: w.ID, path: w.Path, createdAt: w.CreatedAt}
		for _, record := range w.Sessions {
			sess := s.newSession(record, w.Path)
			ws.sessions = append(ws.sessions, sess)
			s.sessions[record.ID] = sess
		}
		s.workspaces = append(s.workspaces, ws)
	}
	return s, nil
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(listener net.Listener) error {
	for {
		c, err := listener.Accept()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		go s.serveConn(c)
	}
}

// serveConn handles each request on its own goroutine, so a slow session
// holds up only the requests waiting for it.
func (s *Server) serveConn(netConn net.Conn) {
	c := newConn(netConn)
	s.connsMu.Lock()
	s.conns[c] = struct{}{}
	s.connsMu.Unlock()
	defer func() {
		s.connsMu.Lock()
		delete(s.conns, c)
		s.connsMu.Unlock()
		c.close()
	}()
	reply := func(response *jsonrpc.Response) { c.queue(response) }
	reader := bufio.NewReader(netConn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("client: %v", err)
			}
			return
		}
		go s.handle(reply, line)
	}
}

// Close stops every running process and ends every session's goroutine. The
// sessions stay stored.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	sessions := make([]*session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()

	for _, sess := range sessions {
		sess.post(sess.end)
	}
	for _, sess := range sessions {
		<-sess.done
	}
}

// workspace returns the workspace with id. The caller holds s.mu.
func (s *Server) workspace(id string) *workspace {
	for _, w := range s.workspaces {
		if w.id == id {
			return w
		}
	}
	return nil
}

// broadcast sends a notification to every connection.
func (s *Server) broadcast(method string, params any) {
	notification := jsonrpc.Notify(method, params)
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	for c := range s.conns {
		c.queue(notification)
	}
}
