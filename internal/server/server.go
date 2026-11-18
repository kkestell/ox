// Package server is the Ox daemon. It accepts client connections, owns the
// table of agents, and turns each JSON-RPC request into an agent operation.
// Clients have no identity: any connection can use any agent, and a
// connection attaches to an agent to receive its updates.
package server

import (
	"bufio"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"net"
	"sync"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/claude"
)

type Server struct {
	mu     sync.Mutex
	agents map[string]*agent.Agent
	closed bool
}

func New() *Server {
	return &Server{agents: make(map[string]*agent.Agent)}
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

// Close stops every agent and refuses new ones.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	agents := make([]*agent.Agent, 0, len(s.agents))
	for _, a := range s.agents {
		agents = append(agents, a)
	}
	s.mu.Unlock()

	for _, a := range agents {
		if err := a.Stop(); err != nil {
			log.Print(err)
		}
	}
}

func (s *Server) create(options claude.Options) (string, error) {
	a, err := agent.Start(options)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		a.Stop()
		return "", errors.New("daemon is shutting down")
	}
	id := rand.Text()
	s.agents[id] = a
	go func() {
		<-a.Done()
		s.mu.Lock()
		delete(s.agents, id)
		s.mu.Unlock()
	}()
	return id, nil
}

func (s *Server) agent(id string) *agent.Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agents[id]
}
