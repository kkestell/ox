package server

import (
	"fmt"
	"log"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/store"
)

// session is a stored session. Its goroutine, run, owns its state and makes
// every call to its agent; other goroutines reach it only through post. Each
// operation queues its own response there, at once or from the agent event
// that finishes it, so a response lands in a connection's queue after every
// update it reflects and before every update it does not.
type session struct {
	// Fixed at creation.
	server    *Server
	id        string
	harness   string
	workspace string // the directory the harness runs in
	createdAt time.Time
	ops       chan func()
	done      chan struct{} // closed once run returns

	// Owned by run.
	settings    agent.Settings
	resumeToken string
	history     history
	agent       agent.Agent // nil while parked
	parkTimer   *time.Timer // armed while the agent is parkable
	ended       bool
}

func (s *Server) newSession(record *store.Session, workspace string) *session {
	sess := &session{
		server: s, id: record.ID, harness: record.Harness, workspace: workspace, createdAt: record.CreatedAt,
		ops: make(chan func()), done: make(chan struct{}),
		settings:    agent.Settings{Model: record.Model, Effort: record.Effort, PermissionMode: record.PermissionMode},
		resumeToken: record.ResumeToken,
		history:     history{store: s.store, sessionID: record.ID},
	}
	go sess.run()
	return sess
}

func (sess *session) run() {
	defer close(sess.done)
	for !sess.ended {
		var events <-chan agent.Event
		if sess.agent != nil {
			events = sess.agent.Events()
		}
		var park <-chan time.Time
		if sess.parkTimer != nil {
			park = sess.parkTimer.C
		}
		select {
		case op := <-sess.ops:
			op()
		case event := <-events:
			sess.agent.Handle(event)
			if sess.agent.Exited() {
				sess.agent = nil
			}
		case <-park:
			sess.parkTimer = nil
			if err := sess.stop(); err != nil {
				log.Printf("park session %s: %v", sess.id, err)
			}
		}
		sess.armPark()
	}
}

// post runs op on the session's goroutine. It reports false once the session
// has ended.
func (sess *session) post(op func()) bool {
	select {
	case sess.ops <- op:
		return true
	case <-sess.done:
		return false
	}
}

// armPark starts the park timer when the agent becomes parkable and stops it
// when the agent stops being parkable, so the full period restarts each time.
func (sess *session) armPark() {
	parkable := sess.agent != nil && sess.server.parkAfter > 0 && sess.agent.Parkable()
	switch {
	case !parkable && sess.parkTimer != nil:
		sess.parkTimer.Stop()
		sess.parkTimer = nil
	case parkable && sess.parkTimer == nil:
		sess.parkTimer = time.NewTimer(sess.server.parkAfter)
	}
}

func (sess *session) start() error {
	start := sess.server.harnesses[sess.harness]
	if start == nil {
		return fmt.Errorf("unknown harness %q", sess.harness)
	}
	a, err := start(agent.Options{
		Workspace:   sess.workspace,
		Resume:      sess.resumeToken,
		Settings:    sess.settings,
		OnUpdate:    sess.update,
		OnResumable: sess.markResumable,
		OnError:     func(err error) { log.Printf("session %s: %v", sess.id, err) },
	})
	if err != nil {
		return err
	}
	sess.agent = a
	return nil
}

func (sess *session) stop() error {
	if err := sess.agent.Stop(); err != nil {
		return err
	}
	for !sess.agent.Exited() {
		sess.agent.Handle(<-sess.agent.Events())
	}
	sess.agent = nil
	return nil
}

func (sess *session) end() {
	if sess.agent != nil {
		if err := sess.stop(); err != nil {
			log.Printf("stop session %s: %v", sess.id, err)
		}
	}
	sess.ended = true
}

func (sess *session) update(update agent.Update) {
	sess.history.record(update)
	sess.server.broadcast("session/update", struct {
		SessionID string `json:"session_id"`
		agent.Update
	}{sess.id, update})
}

func (sess *session) save(settings agent.Settings, resumeToken string) error {
	return sess.server.store.SaveSession(&store.Session{
		ID: sess.id, Model: settings.Model, Effort: settings.Effort, PermissionMode: settings.PermissionMode,
		ResumeToken: resumeToken,
	})
}

// configured makes settings the session's own and tells every client.
func (sess *session) configured(settings agent.Settings) {
	sess.settings = settings
	sess.server.broadcast("session/configured", struct {
		SessionID string `json:"session_id"`
		agent.Settings
	}{sess.id, settings})
}

func (sess *session) markResumable(token string) {
	if err := sess.save(sess.settings, token); err != nil {
		log.Printf("mark session %s resumable: %v", sess.id, err)
		return
	}
	sess.resumeToken = token
}
