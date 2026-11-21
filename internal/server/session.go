package server

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
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
	workspace string // the workspace's path
	child     *child // nil for a top-level session
	createdAt time.Time
	ops       chan func()
	done      chan struct{} // closed once run returns

	// Guarded by Server.mu. An archived session has no agent, and the child
	// of an archived session is archived.
	archived bool
	// The first line of the first prompt the user sent a top-level session,
	// empty before it has one. Written only by run.
	firstLine string

	// Owned by run.
	settings    agent.Settings
	resumeToken string
	history     history
	agent       agent.Agent // nil while parked
	parkTimer   *time.Timer // armed while the agent is parkable
	ended       bool
}

// child is what makes a session a child of another, fixed at creation.
type child struct {
	ParentID  string `json:"parent_id"`
	Name      string `json:"name"`
	Title     string `json:"title"`
	Directory string `json:"directory"`
}

// firstLine is the line of a prompt that labels its session.
func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// labelLocked is the session's label: a child's title, or else the first line
// of the first prompt the user sent it, or the start of its ID before it has
// one. The caller holds Server.mu.
func (sess *session) labelLocked() string {
	switch {
	case sess.child != nil:
		return sess.child.Title
	case sess.firstLine != "":
		return sess.firstLine
	}
	return sess.id[:8]
}

// newSession starts a stored session's goroutine. firstLine is the first line
// of the first prompt the user sent it, or "".
func (s *Server) newSession(record *store.Session, workspace, firstLine string) *session {
	var c *child
	if r := record.Child; r != nil {
		c = &child{ParentID: r.ParentID, Name: r.Name, Title: r.Title, Directory: r.Directory}
	}
	sess := &session{
		server: s, id: record.ID, harness: record.Harness, workspace: workspace, child: c, createdAt: record.CreatedAt,
		ops: make(chan func()), done: make(chan struct{}), archived: record.Archived, firstLine: firstLine,
		settings:    agent.Settings{Model: record.Model, Effort: record.Effort, PermissionMode: record.PermissionMode},
		resumeToken: record.ResumeToken,
		history:     history{store: s.store, sessionID: record.ID},
	}
	// A session starts parked, so any earlier process has ended.
	sess.history.recordEnd()
	go sess.run()
	return sess
}

// directory is where the harness runs: a child's own directory, or the
// workspace's.
func (sess *session) directory() string {
	if sess.child != nil {
		return sess.child.Directory
	}
	return sess.workspace
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
			sess.stop()
		}
		sess.armPark()
	}
}

// snapshot is the agent's snapshot, or a parked one with no approvals or
// inputs while no process runs.
func (sess *session) snapshot() agent.Snapshot {
	if sess.agent == nil {
		return agent.Snapshot{State: agent.StateParked, Approvals: []agent.Approval{}, Queued: []agent.Input{}}
	}
	return sess.agent.Snapshot()
}

// sendPrompt refuses a user prompt while any input waits for the agent to
// take it; a message waits its turn instead.
func (sess *session) sendPrompt(text string) (string, error) {
	if len(sess.snapshot().Queued) != 0 {
		return "", agent.ErrBusy
	}
	if err := sess.startForInput(); err != nil {
		return "", err
	}
	input := agent.Input{MessageID: rand.Text(), Text: text, Content: text}
	return input.MessageID, sess.agent.Send(input)
}

// acceptMessage hands a message to the agent, which queues it behind every
// input it has not taken.
func (sess *session) acceptMessage(message agent.Input) error {
	if err := sess.startForInput(); err != nil {
		return err
	}
	message.MessageID = rand.Text()
	return sess.agent.Send(message)
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

var (
	errArchived        = errors.New("session is archived")
	errSessionNotFound = errors.New("session not found")
)

// startForInput starts a parked session for an input, unless it is archived.
// The sender receives the error of a harness that cannot be launched; a start
// that fails later ends the agent, which drops its inputs and reports why.
func (sess *session) startForInput() error {
	if sess.agent != nil {
		return nil
	}
	if sess.isArchived() {
		return errArchived
	}
	return sess.start()
}

func (sess *session) isArchived() bool {
	sess.server.mu.Lock()
	defer sess.server.mu.Unlock()
	return sess.archived
}

// held is a session whose goroutine waits in hold until it is released.
type held struct {
	// free is whether the session was idle, in the background, or parked when
	// held.
	free    bool
	release chan<- bool
}

// hold holds the session's goroutine until it is released through the result.
// Released with true, the session stops its agent before it goes on. hold
// reports false once the session has ended.
func (sess *session) hold() (held, bool) {
	free := make(chan bool)
	release := make(chan bool, 1)
	if !sess.post(func() {
		state := sess.snapshot().State
		free <- state == agent.StateIdle || state == agent.StateBackground || state == agent.StateParked
		if <-release && sess.agent != nil {
			sess.stop()
		}
	}) {
		return held{}, false
	}
	return held{<-free, release}, true
}

// archive archives sess and, for a top-level session, its children, once
// each is idle or parked, and stops their agents. It holds every member's
// goroutine, the parent's first, while it checks them and sets their flags,
// so none starts in between; a child is created only on its parent's
// goroutine, so the children read while the parent is held are all of them.
func (s *Server) archive(sess *session) error {
	first, ok := sess.hold()
	if !ok {
		return errSessionNotFound
	}
	members, holds := []*session{sess}, []held{first}
	if sess.child == nil {
		s.mu.Lock()
		children := s.children(sess)
		s.mu.Unlock()
		for _, c := range children {
			if h, ok := c.hold(); ok {
				members, holds = append(members, c), append(holds, h)
			}
		}
	}
	err := func() error {
		if slices.ContainsFunc(holds, func(h held) bool { return !h.free }) {
			return agent.ErrBusy
		}
		return s.setArchived(members, true)
	}()
	for _, h := range holds {
		h.release <- err == nil
	}
	return err
}

// unarchive unarchives sess and, for a top-level session, its children. A
// child of an archived session stays archived.
func (s *Server) unarchive(sess *session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess.child != nil {
		if parent := s.sessions[sess.child.ParentID]; parent != nil && parent.archived {
			return errors.New("parent session is archived")
		}
	}
	return s.setArchivedLocked(append([]*session{sess}, s.children(sess)...), false)
}

func (s *Server) setArchived(members []*session, archived bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setArchivedLocked(members, archived)
}

// setArchivedLocked stores and sets the members' archived flags and tells
// every client. The caller holds s.mu.
func (s *Server) setArchivedLocked(members []*session, archived bool) error {
	if s.closed {
		return errors.New("daemon is shutting down")
	}
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.id
	}
	if err := s.store.SetArchived(ids, archived); err != nil {
		return err
	}
	method := "session/unarchived"
	if archived {
		method = "session/archived"
	}
	for _, m := range members {
		m.archived = archived
		s.broadcast(method, struct {
			SessionID string `json:"session_id"`
		}{m.id})
	}
	return nil
}

func (sess *session) start() error {
	start := sess.server.harnesses[sess.harness].Start
	if start == nil {
		return fmt.Errorf("unknown harness %q", sess.harness)
	}
	a, err := start(agent.Options{
		SessionID: sess.id,
		Workspace: sess.directory(),
		Resume:    sess.resumeToken,
		Settings:  sess.settings,
		MCP: agent.MCPServer{
			Command:  sess.server.mcp.Forwarder,
			Env:      map[string]string{"OX_SOCKET": sess.server.mcp.Socket, "OX_SESSION_ID": sess.id},
			Approved: approvedTools(),
		},
		OnUpdate:     sess.update,
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

// stop ends the agent at once; its process exits in the background.
func (sess *session) stop() {
	sess.agent.Stop()
	sess.parked()
}

// parked drops the agent that has ended and tells every client.
func (sess *session) parked() {
	sess.agent = nil
	sess.update(agent.Update{Type: agent.UpdateParked, Time: time.Now()})
}

// destroy stops the agent, deletes the session, and removes it from every
// client. It refuses while the session has children; a child is created only
// on its parent's goroutine, so none appears while destroy runs.
func (sess *session) destroy() error {
	s := sess.server
	s.mu.Lock()
	children := s.children(sess)
	s.mu.Unlock()
	if len(children) > 0 {
		return errors.New("session has child sessions")
	}
	// The process stops first, so it records nothing after the events are gone.
	if sess.agent != nil {
		sess.stop()
	}
	if err := s.store.DeleteSession(sess.id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.sessions, sess.id)
	for _, w := range s.workspaces {
		w.sessions = slices.DeleteFunc(w.sessions, func(other *session) bool { return other == sess })
	}
	s.broadcast("session/destroyed", struct {
		SessionID string `json:"session_id"`
	}{sess.id})
	s.mu.Unlock()
	sess.ended = true
	return nil
}

func (sess *session) end() {
	if sess.agent != nil {
		sess.stop()
	}
	sess.ended = true
}

func (sess *session) update(update agent.Update) {
	sess.history.record(update)
	sess.server.broadcast("session/update", struct {
		SessionID string `json:"session_id"`
		agent.Update
	}{sess.id, update})
	if update.Type == agent.UpdatePrompt && update.From == "" && sess.child == nil {
		sess.labelFrom(update.Text)
	}
}

// labelFrom labels a top-level session with the first prompt the user sent it
// and tells every client. Later prompts leave the label as it is.
func (sess *session) labelFrom(prompt string) {
	s := sess.server
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess.firstLine != "" {
		return
	}
	sess.firstLine = firstLine(prompt)
	s.broadcast("session/labeled", struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label"`
	}{sess.id, sess.labelLocked()})
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
// settings: Claude has already started the conversation. An empty token,
// for a conversation the harness no longer has, is kept the same way, since
// resuming it can only fail again.
func (sess *session) markResumable(token string) {
	sess.resumeToken = token
	if err := sess.save(sess.settings, token); err != nil {
		log.Printf("mark session %s resumable: %v", sess.id, err)
	}
}
