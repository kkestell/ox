package server

import (
	"context"
	"sync"
)

// operations allows at most one prompt, load, close, or delete per session at
// a time. An operation starts when it is acquired and ends when it is
// released. Once shutdown begins, no operation starts.
type operations struct {
	mu      sync.Mutex
	active  map[string]*operation
	closing map[string]bool
	// running counts acquired operations, so shutdown can wait for them.
	running      sync.WaitGroup
	shuttingDown bool
}

type operation struct {
	// cancel cancels a prompt; it is nil for other operations.
	cancel context.CancelFunc
	done   chan struct{}
}

func newOperations() *operations {
	return &operations{active: map[string]*operation{}, closing: map[string]bool{}}
}

// tryPrompt acquires a prompt, whose context is cancelled by cancel, close, or
// shutdown.
func (o *operations) tryPrompt(id string) (context.Context, func(), bool) {
	ctx, cancel := context.WithCancel(context.Background())
	release, ok := o.acquire(id, cancel)
	if !ok {
		cancel()
	}
	return ctx, release, ok
}

// try acquires a load or delete.
func (o *operations) try(id string) (func(), bool) {
	return o.acquire(id, nil)
}

func (o *operations) acquire(id string, cancel context.CancelFunc) (func(), bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.shuttingDown || o.closing[id] || o.active[id] != nil {
		return nil, false
	}
	op := &operation{cancel: cancel, done: make(chan struct{})}
	o.active[id] = op
	o.running.Add(1)
	return func() {
		o.mu.Lock()
		delete(o.active, id)
		o.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		close(op.done)
		o.running.Done()
	}, true
}

// beginClose reserves a close and cancels the session's prompt. The returned
// wait must finish before cleanup starts; release ends the close after its
// response is sent.
func (o *operations) beginClose(id string) (wait, release func(), ok bool) {
	o.mu.Lock()
	if o.shuttingDown || o.closing[id] {
		o.mu.Unlock()
		return nil, nil, false
	}
	o.closing[id] = true
	o.running.Add(1)
	op := o.active[id]
	o.mu.Unlock()
	if op != nil && op.cancel != nil {
		op.cancel()
	}
	wait = func() {
		if op != nil {
			<-op.done
		}
	}
	return wait, func() {
		o.mu.Lock()
		delete(o.closing, id)
		o.mu.Unlock()
		o.running.Done()
	}, true
}

// cancel cancels the session's prompt. It does nothing when the session is
// idle or is being loaded or deleted.
func (o *operations) cancel(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if op := o.active[id]; op != nil && op.cancel != nil {
		op.cancel()
	}
}

// beginShutdown rejects every later operation and cancels the active prompts.
func (o *operations) beginShutdown() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.shuttingDown = true
	for _, op := range o.active {
		if op.cancel != nil {
			op.cancel()
		}
	}
}

func (o *operations) isShuttingDown() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.shuttingDown
}

// shutdown begins shutdown and waits until every operation is released.
func (o *operations) shutdown() {
	o.beginShutdown()
	o.running.Wait()
}
