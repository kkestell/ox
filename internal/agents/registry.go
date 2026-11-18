package agents

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("agent not found")
	ErrBusy     = errors.New("agent is busy")
)

type Registry struct {
	mu        sync.Mutex
	processes map[string]*process
	closed    bool
}

func NewRegistry() *Registry {
	return &Registry{processes: make(map[string]*process)}
}

func (r *Registry) Create(options Options) (string, error) {
	p, id, err := r.start(options)
	if err != nil {
		return "", err
	}
	select {
	case err = <-p.ready:
		if err == nil {
			return id, nil
		}
	case <-time.After(time.Minute):
		err = errors.New("no startup response within one minute")
	}
	return "", fmt.Errorf("initialize Claude: %w", errors.Join(err, p.stop()))
}

func (r *Registry) start(options Options) (*process, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, "", errors.New("daemon is shutting down")
	}
	p, err := startClaude(options)
	if err != nil {
		return nil, "", fmt.Errorf("start Claude: %w", err)
	}
	id := rand.Text()
	r.processes[id] = p
	go func() {
		p.run()
		r.mu.Lock()
		delete(r.processes, id)
		close(p.done)
		r.mu.Unlock()
	}()
	return p, id, nil
}

func (r *Registry) Destroy(id string) error {
	p := r.lookup(id)
	if p == nil {
		return ErrNotFound
	}
	return p.stop()
}

func (r *Registry) Send(id, clientID, text string, notify func(Update)) (string, error) {
	p := r.lookup(id)
	if p == nil {
		return "", ErrNotFound
	}
	return p.send(clientID, text, notify)
}

func (r *Registry) Approve(id, clientID, requestID string, response json.RawMessage) error {
	p := r.lookup(id)
	if p == nil {
		return ErrNotFound
	}
	return p.approve(clientID, requestID, response)
}

func (r *Registry) Configure(id string, config Configuration) error {
	p := r.lookup(id)
	if p == nil {
		return ErrNotFound
	}
	return p.configure(config)
}

func (r *Registry) Disconnect(clientID string) {
	r.mu.Lock()
	processes := make([]*process, 0, len(r.processes))
	for _, p := range r.processes {
		processes = append(processes, p)
	}
	r.mu.Unlock()
	for _, p := range processes {
		p.disconnect(clientID)
	}
}

func (r *Registry) lookup(id string) *process {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processes[id]
}

func (r *Registry) Close() {
	r.mu.Lock()
	r.closed = true
	processes := make([]*process, 0, len(r.processes))
	for _, p := range r.processes {
		processes = append(processes, p)
	}
	r.mu.Unlock()

	for _, p := range processes {
		if err := p.stop(); err != nil {
			log.Print(err)
		}
	}
}
