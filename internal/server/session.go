package server

import (
	"crypto/rand"
	"errors"
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
	agent       agent.Agent   // nil while parked
	parkTimer   *time.Timer   // armed while the agent is parkable
	inbox       []agent.Input // messages waiting to be written, oldest first
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
	// A session starts parked, so any earlier process has ended.
	sess.history.recordEnd()
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
				sess.parked()
			}
		case <-park:
			sess.parkTimer = nil
			if err := sess.stop(); err != nil {
				log.Printf("park session %s: %v", sess.id, err)
			}
		}
		sess.deliver()
		sess.armPark()
	}
}

// deliver sends the oldest waiting message to the agent, starting a parked
// session for it. It runs after every operation and event, so each message
// goes in as soon as the agent can take it, in order.
func (sess *session) deliver() {
	if sess.ended || len(sess.inbox) == 0 {
		return
	}
	if sess.startForInput() != nil {
		return
	}
	message := sess.inbox[0]
	err := sess.agent.Send(message)
	if errors.Is(err, agent.ErrBusy) {
		return // retried after the next operation or event
	}
	sess.inbox[0] = agent.Input{}
	sess.inbox = sess.inbox[1:]
	if err != nil {
		sess.inboxUpdate(message, agent.UpdatePromptCancelled)
		log.Printf("session %s: deliver message: %v", sess.id, err)
	}
}

// snapshot is the agent's snapshot, or a parked one with no approvals while no
// process runs.
func (sess *session) snapshot() agent.Snapshot {
	if sess.agent == nil {
		return agent.Snapshot{State: agent.StateParked, Approvals: []agent.Approval{}}
	}
	return sess.agent.Snapshot()
}

// queued lists the inputs the agent has not taken: the one written to the
// harness, then the inbox, oldest first.
func (sess *session) queued() []agent.Input {
	queued := make([]agent.Input, 0, len(sess.inbox)+1)
	if written := sess.snapshot().Queued; written != nil {
		queued = append(queued, *written)
	}
	return append(queued, sess.inbox...)
}

// messagesWaiting reports that messages wait in the inbox, which go ahead of
// a user prompt and hold off a configuration.
func (sess *session) messagesWaiting() bool {
	return len(sess.inbox) != 0
}

func (sess *session) sendPrompt(text string) (string, error) {
	if sess.messagesWaiting() {
		return "", agent.ErrBusy
	}
	if err := sess.startForInput(); err != nil {
		return "", err
	}
	input := agent.Input{MessageID: rand.Text(), Text: text, Content: text}
	return input.MessageID, sess.agent.Send(input)
}

func (sess *session) acceptMessage(message agent.Input) {
	message.MessageID = rand.Text()
	sess.inbox = append(sess.inbox, message)
	sess.inboxUpdate(message, agent.UpdatePromptQueued)
}

func (sess *session) cancelInbox() {
	for _, message := range sess.inbox {
		sess.inboxUpdate(message, agent.UpdatePromptCancelled)
	}
	sess.inbox = nil
}

// inboxUpdate publishes a lifecycle update for a message the agent has not
// received.
func (sess *session) inboxUpdate(message agent.Input, kind string) {
	update := message.Update(kind)
	update.Time = time.Now()
	sess.update(update)
}

// agentUpdate publishes the agent's updates, except the announcement of a
// message the inbox already made on acceptance.
func (sess *session) agentUpdate(u agent.Update) {
	if u.Type == agent.UpdatePromptQueued && u.From != "" {
		return
	}
	sess.update(u)
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

// startForInput starts a parked session for either source of input and owns
// the effects of a failed start. A user send also receives the start error.
func (sess *session) startForInput() error {
	if sess.agent != nil {
		return nil
	}
	err := sess.start()
	if err == nil {
		return nil
	}
	hadMessages := len(sess.inbox) > 0
	sess.cancelInbox()
	switch {
	case errors.Is(err, agent.ErrNoConversation):
		sess.forgetConversation(err)
		err = errors.New(lostConversation)
		sess.update(agent.Update{Type: agent.UpdateExited, Error: err.Error(), Time: time.Now()})
	case hadMessages:
		sess.update(agent.Update{Type: agent.UpdateExited, Error: "messages not delivered: " + err.Error(), Time: time.Now()})
	}
	return err
}

func (sess *session) start() error {
	start := sess.server.harnesses[sess.harness].Start
	if start == nil {
		return fmt.Errorf("unknown harness %q", sess.harness)
	}
	a, err := start(agent.Options{
		SessionID: sess.id,
		Workspace: sess.workspace,
		Resume:    sess.resumeToken,
		Settings:  sess.settings,
		MCP: agent.MCPServer{
			Command:  sess.server.mcp.Forwarder,
			Env:      map[string]string{"OX_SOCKET": sess.server.mcp.Socket, "OX_SESSION_ID": sess.id},
			Approved: approvedTools,
		},
		OnUpdate:     sess.agentUpdate,
		OnResumable:  sess.markResumable,
		OnConfigured: sess.adopt,
		OnError:      func(err error) { log.Printf("session %s: %v", sess.id, err) },
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
	sess.parked()
	return nil
}

// parked drops the agent whose process has exited, with the messages waiting
// for it, and tells every client.
func (sess *session) parked() {
	sess.agent = nil
	sess.cancelInbox()
	sess.update(agent.Update{Type: agent.UpdateParked, Time: time.Now()})
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

// adopt keeps settings in use even when storing them fails.
func (sess *session) adopt(settings agent.Settings) {
	if settings == sess.settings {
		return
	}
	sess.settings = settings
	if err := sess.save(settings, sess.resumeToken); err != nil {
		log.Printf("session %s: store settings: %v", sess.id, err)
	}
	sess.server.broadcast("session/configured", struct {
		SessionID string `json:"session_id"`
		agent.Settings
	}{sess.id, settings})
}

// markResumable keeps the token even when storing it fails, as adopt keeps
// settings: Claude has already started the conversation.
func (sess *session) markResumable(token string) {
	sess.resumeToken = token
	if err := sess.save(sess.settings, token); err != nil {
		log.Printf("mark session %s resumable: %v", sess.id, err)
	}
}

// lostConversation tells the user that the next prompt starts a new
// conversation. The send that found the conversation gone fails with it, so
// the user decides whether to send the prompt without the earlier context.
const lostConversation = "Claude could not resume the earlier conversation; the next prompt starts a new one without its context"

// forgetConversation clears a resume token the harness can no longer resume,
// such as one whose transcript Claude has deleted. The session forgets the
// token even when storing that fails, since resuming it can only fail again.
func (sess *session) forgetConversation(cause error) {
	log.Printf("session %s: %v", sess.id, cause)
	sess.resumeToken = ""
	if err := sess.save(sess.settings, ""); err != nil {
		log.Printf("session %s: forget conversation: %v", sess.id, err)
	}
}
