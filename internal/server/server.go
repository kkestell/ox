package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"slices"
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
	harnesses map[string]agent.Harness
	parkAfter time.Duration
	mcp       MCP
	// ask is the titling harness's Ask.
	ask func(ctx context.Context, model, effort, instructions, text string) (string, error)

	// mu guards which workspaces and sessions exist, which sessions are
	// archived, and closed. A change to them is broadcast, and an answer
	// listing them is queued, while it is held, so every connection sees both
	// in one order. Nothing posts to a session, launches a harness, or hands
	// an agent input while holding it.
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
	sessions  []*session
}

// MCP locates Ox's MCP server for harnesses: the forwarder they run and the
// socket it reaches oxd on.
type MCP struct{ Forwarder, Socket string }

func New(st *store.Store, cfg *config.Config, parkAfter time.Duration, harnesses map[string]agent.Harness, mcp MCP) (*Server, error) {
	titler := harnesses[cfg.Titling.Harness].Ask
	if titler == nil {
		return nil, fmt.Errorf("titling harness %q cannot title sessions", cfg.Titling.Harness)
	}
	stored, err := st.Workspaces()
	if err != nil {
		return nil, err
	}
	s := &Server{
		store: st, config: cfg, harnesses: harnesses, parkAfter: parkAfter, mcp: mcp, ask: titler,
		sessions: make(map[string]*session), conns: make(map[*conn]struct{}),
	}
	for _, w := range stored {
		ws := &workspace{id: w.ID, path: w.Path, createdAt: w.CreatedAt}
		for _, record := range w.Sessions {
			var first string
			if record.Child == nil {
				for text, err := range st.UserPrompts(record.ID) {
					if err != nil {
						return nil, err
					}
					if first = firstLine(text); first != "" {
						break
					}
				}
			}
			sess := s.newSession(record, w.Path, first)
			// A session starts parked, so any earlier process has ended.
			sess.history.recordEnd()
			go sess.run()
			ws.sessions = append(ws.sessions, sess)
			s.sessions[record.ID] = sess
		}
		s.workspaces = append(s.workspaces, ws)
	}
	return s, nil
}

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
	var mcpSession string // set by an mcp/attach first line
	for first := true; ; first = false {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("client: %v", err)
			}
			return
		}
		request, invalid := jsonrpc.Parse(line)
		if invalid != nil {
			reply(invalid)
			continue
		}
		// Operations require a request ID so the client receives the outcome.
		if len(request.ID) == 0 {
			continue
		}
		if first {
			if id, handled := s.attach(c, reply, request); handled {
				mcpSession = id
				continue
			}
		}
		if mcpSession != "" {
			go s.handleMCP(reply, mcpSession, request)
		} else {
			go s.handle(reply, request)
		}
	}
}

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

// workspaceOf returns the workspace sess belongs to. The caller holds s.mu.
func (s *Server) workspaceOf(sess *session) *workspace {
	for _, w := range s.workspaces {
		if slices.Contains(w.sessions, sess) {
			return w
		}
	}
	return nil
}

// children returns parent's child sessions. The caller holds s.mu.
func (s *Server) children(parent *session) []*session {
	var children []*session
	for _, sess := range s.sessions {
		if sess.child != nil && sess.child.ParentID == parent.id {
			children = append(children, sess)
		}
	}
	return children
}

// depthLocked is the number of sess's ancestors. Every ancestor is a member
// while its descendant is, since a session with children cannot be deleted,
// so sess must be a member, as it is on its own goroutine. The caller holds
// s.mu.
func (s *Server) depthLocked(sess *session) int {
	depth := 0
	for sess.child != nil {
		sess = s.sessions[sess.child.ParentID]
		depth++
	}
	return depth
}

func (s *Server) broadcast(method string, params any) {
	notification := jsonrpc.Notify(method, params)
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	for c := range s.conns {
		c.queue(notification)
	}
}
