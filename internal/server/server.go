// Package server is the Ox daemon. It accepts client connections, keeps the
// stored workspaces and sessions, and turns each JSON-RPC request into a
// session operation. A session runs a harness process only while it is in
// use: a parked session has none, and the next prompt resumes its
// conversation.
// Clients have no identity: any connection can use any session, and every
// connection receives every session's updates.
package server

import (
	"bufio"
	"errors"
	"fmt"
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

	mu         sync.Mutex // guards workspaces, every stored record, sessions, and closed
	workspaces []*store.Workspace
	sessions   map[string]*session
	closed     bool

	connsMu sync.Mutex // guards conns; taken after any other lock
	conns   map[*conn]struct{}
}

// session is a stored session and the harness process running it, if any.
type session struct {
	record    *store.Session
	workspace string
	history   *history

	// mu guards the fields below and is held while a process starts, so a
	// session runs at most one process. Lock it before Server.mu, never after.
	mu        sync.Mutex
	agent     agent.Agent
	destroyed bool
}

// New loads the stored workspaces and sessions. Every session starts parked;
// harnesses start a session's process by its harness name, and a process
// parks once it has been unused for parkAfter. The configuration's harnesses
// are offered to clients.
func New(st *store.Store, cfg *config.Config, parkAfter time.Duration, harnesses map[string]agent.Start) (*Server, error) {
	workspaces, err := st.Workspaces()
	if err != nil {
		return nil, err
	}
	s := &Server{
		store: st, config: cfg, harnesses: harnesses, parkAfter: parkAfter, workspaces: workspaces,
		sessions: make(map[string]*session), conns: make(map[*conn]struct{}),
	}
	for _, w := range workspaces {
		for _, record := range w.Sessions {
			s.sessions[record.ID] = s.newSession(record, w.Path)
		}
	}
	return s, nil
}

func (s *Server) newSession(record *store.Session, workspace string) *session {
	return &session{record: record, workspace: workspace, history: &history{store: s.store, sessionID: record.ID}}
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
	reader := bufio.NewReader(netConn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("client: %v", err)
			}
			return
		}
		if response := s.handle(c, line); response != nil {
			c.queue(response)
		}
	}
}

// Close stops every running process and refuses to start new ones. The
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
		sess.mu.Lock()
		a := sess.live()
		sess.mu.Unlock()
		if a == nil {
			continue
		}
		if err := a.Stop(); err != nil {
			log.Print(err)
		}
	}
}

func (s *Server) session(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *Server) workspace(id string) *store.Workspace {
	for _, w := range s.workspaces {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// live returns the session's process, or nil when it is parked. The caller
// holds sess.mu.
func (sess *session) live() agent.Agent {
	if sess.agent != nil {
		select {
		case <-sess.agent.Done():
			sess.agent = nil
		default:
		}
	}
	return sess.agent
}

// running returns the session's process, starting one if it is parked. The
// caller holds sess.mu.
func (s *Server) running(sess *session) (agent.Agent, error) {
	if a := sess.live(); a != nil {
		return a, nil
	}
	s.mu.Lock()
	closed := s.closed
	record := *sess.record
	s.mu.Unlock()
	if closed {
		return nil, errors.New("daemon is shutting down")
	}

	start := s.harnesses[record.Harness]
	if start == nil {
		return nil, fmt.Errorf("unknown harness %q", record.Harness)
	}
	a, err := start(agent.Options{
		Workspace:   sess.workspace,
		Resume:      record.ResumeToken,
		Settings:    agent.Settings{Model: record.Model, Effort: record.Effort, PermissionMode: record.PermissionMode},
		ParkAfter:   s.parkAfter,
		Subscribers: []agent.Subscriber{sess.history, sessionUpdates{s, record.ID}},
		OnResumable: func(token string) { s.markResumable(sess, token) },
	})
	if err != nil {
		return nil, err
	}
	sess.agent = a
	return a, nil
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

// sessionUpdates broadcasts a session's updates.
type sessionUpdates struct {
	s         *Server
	sessionID string
}

func (u sessionUpdates) Publish(update agent.Update) bool {
	u.s.broadcast("session/update", struct {
		SessionID string `json:"session_id"`
		agent.Update
	}{u.sessionID, update})
	return true
}

// markResumable stores the token that resumes the session's conversation.
func (s *Server) markResumable(sess *session, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := *sess.record
	record.ResumeToken = token
	if err := s.store.SaveSession(&record); err != nil {
		log.Printf("mark session resumable: %v", err)
		return
	}
	*sess.record = record
}
