// Package server is the Ox daemon. It accepts client connections, keeps the
// stored workspaces and sessions, and turns each JSON-RPC request into a
// session operation. A session runs a Claude process only while it is in use:
// a parked session has none, and the next prompt resumes its conversation.
// Clients have no identity: any connection can use any session, and a
// connection attaches to a session to receive its updates.
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
	"github.com/kkestell/ox/internal/claude"
	"github.com/kkestell/ox/internal/store"
)

type Server struct {
	statePath string
	parkAfter time.Duration

	mu       sync.Mutex // guards state, every stored record, sessions, and closed
	state    *store.State
	sessions map[string]*session
	closed   bool
}

// session is a stored session and the Claude process running it, if any.
type session struct {
	record    *store.Session
	workspace string

	// mu guards the fields below and is held while a process starts, so a
	// session runs at most one process. Lock it before Server.mu, never after.
	mu          sync.Mutex
	agent       *agent.Agent
	subscribers map[subscription]struct{} // carried to each new process
	destroyed   bool
}

// New loads the stored state. Every session starts parked, and a running
// session parks once its process has been unused for parkAfter.
func New(statePath string, parkAfter time.Duration) (*Server, error) {
	state, err := store.Load(statePath)
	if err != nil {
		return nil, err
	}
	s := &Server{statePath: statePath, parkAfter: parkAfter, state: state, sessions: make(map[string]*session)}
	for _, w := range state.Workspaces {
		for _, record := range w.Sessions {
			s.sessions[record.ID] = newSession(record, w.Path)
		}
	}
	return s, nil
}

func newSession(record *store.Session, workspace string) *session {
	return &session{record: record, workspace: workspace, subscribers: make(map[subscription]struct{})}
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
	defer c.close()
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

// save writes the stored state. The caller holds s.mu.
func (s *Server) save() error {
	return s.state.Save(s.statePath)
}

func (s *Server) session(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *Server) workspace(id string) *store.Workspace {
	for _, w := range s.state.Workspaces {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// live returns the session's process, or nil when it is parked. The caller
// holds sess.mu.
func (sess *session) live() *agent.Agent {
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
func (s *Server) running(sess *session) (*agent.Agent, error) {
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

	a, err := agent.Start(claude.Options{
		Workspace: sess.workspace, SessionID: record.ID, Resume: record.Resumable,
		Model: record.Model, Effort: record.Effort, PermissionMode: record.PermissionMode,
	}, s.parkAfter)
	if err != nil {
		return nil, err
	}
	sess.agent = a
	// No prompt is running yet, so these subscribers miss no update.
	for sub := range sess.subscribers {
		if sub.conn.isClosed() {
			delete(sess.subscribers, sub)
			continue
		}
		a.Attach(sub, func(agent.Snapshot) {})
	}
	if !record.Resumable {
		a.Attach(resumeMarker{s, sess}, func(agent.Snapshot) {})
	}
	return a, nil
}

// resumeMarker marks its session resumable at the first result, once Claude
// has written the transcript.
type resumeMarker struct {
	s    *Server
	sess *session
}

func (m resumeMarker) Publish(update agent.Update) bool {
	if update.Type != "result" {
		return true
	}
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	m.sess.record.Resumable = true
	if err := m.s.save(); err != nil {
		log.Printf("save state: %v", err)
	}
	return false
}
