package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/store"
)

// session is a stored session. Its goroutine, run, owns its state and makes
// every call to its agent; other goroutines reach it only through post. Each
// operation queues its own response there, at once or from the agent event
// that finishes it, so a response lands in a connection's queue after every
// update it reflects and before every update it does not. Until run starts,
// the session belongs to whoever builds it.
type session struct {
	// Fixed at creation.
	server    *Server
	id        string
	harness   string
	workspace string       // the workspace's path
	directory string       // where the harness runs
	worktree  string       // the root of the session's worktree, or ""
	child     *store.Child // nil for a top-level session
	// creator is the ID of the session whose Claude started this top-level
	// session, or "".
	creator   string
	createdAt time.Time
	ops       chan func()
	done      chan struct{} // closed once run returns
	// ctx bounds the work the session starts off its goroutine. run cancels
	// it before it closes done, so nothing it bounds starts after the
	// session has ended.
	ctx    context.Context
	cancel context.CancelFunc

	// Guarded by Server.mu. An archived session has no agent, and the child
	// of an archived session is archived.
	archived bool
	// The first nonblank first line of the prompts the user sent a top-level
	// session, shortened, empty before it has one. Written only by run.
	firstLine string
	// The title the titling model wrote for a top-level session, empty
	// before it has one. Written only by run.
	title string
	// The state last published to clients. Written by whoever owns the
	// session: its builder until run starts, then run.
	state string
	// deleting is the delete in progress, nil when none is. Written only by
	// run, under the lock, so run reads it without the lock.
	deleting *deleting

	// reporting is what a session that reports to another keeps for its
	// recipient, nil for a session that reports to none. Fixed at creation.
	reporting *reporting

	// Owned by run.
	settings    agent.Settings
	resumeToken string
	history     history
	agent       agent.Agent // nil while parked
	parkTimer   *time.Timer // armed while the agent is parkable
	ended       bool
	// exiting holds the Reaped channels of dropped agents whose processes
	// may still be running, so a delete waits for them.
	exiting []<-chan struct{}
}

// deleting is a delete waiting off the session's goroutine for the session's
// processes to exit and its worktree to be removed, with the reply that
// answers it.
type deleting struct {
	reply func(error)
}

// newSession builds a stored session, whose goroutine its caller starts.
// firstLine is the first nonblank first line of the prompts the user sent it,
// or "".
func (s *Server) newSession(record *store.Session, workspace, firstLine string) *session {
	sess := &session{
		server: s, id: record.ID, harness: record.Harness, workspace: workspace, directory: record.Directory, worktree: record.Worktree, child: record.Child, creator: record.Creator, createdAt: record.CreatedAt,
		ops: make(chan func()), done: make(chan struct{}), archived: record.Archived, firstLine: firstLine, title: record.Title,
		state:       agent.StateParked,
		settings:    agent.Settings{Model: record.Model, Effort: record.Effort, PermissionMode: record.PermissionMode},
		resumeToken: record.ResumeToken,
		history:     history{store: s.store, sessionID: record.ID},
	}
	sess.ctx, sess.cancel = context.WithCancel(context.Background())
	if sess.reportsTo() != "" {
		sess.reporting = newReporting()
	}
	return sess
}

func (sess *session) run() {
	defer close(sess.done)
	defer sess.cancel()
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
// inputs while the session is parked.
func (sess *session) snapshot() agent.Snapshot {
	if sess.agent == nil {
		return agent.Snapshot{State: agent.StateParked, Approvals: []agent.Approval{}, Queued: []agent.Input{}}
	}
	return sess.agent.Snapshot()
}

// sendPrompt refuses a user prompt while any input waits for the agent to
// take it; a message waits its turn instead. It stores the prompt's images
// before sending, so a page can load them as soon as it sees the prompt, and
// deletes them when the send fails.
// An empty messageID gets a generated one.
func (sess *session) sendPrompt(messageID, text string, images []agent.Image) (string, error) {
	if len(sess.snapshot().Queued) != 0 {
		return "", agent.ErrBusy
	}
	if err := sess.startForInput(); err != nil {
		return "", err
	}
	if len(images) > 0 {
		stored := make([]store.Image, len(images))
		for i := range images {
			images[i].ID = newID()
			stored[i] = store.Image{ID: images[i].ID, MimeType: images[i].MimeType, Data: images[i].Data}
		}
		if err := sess.server.store.AddImages(sess.id, stored); err != nil {
			log.Printf("session %s: store images: %v", sess.id, err)
			return "", err
		}
	}
	if messageID == "" {
		messageID = newID()
	}
	input := agent.Input{MessageID: messageID, Text: text, Images: images, Content: text}
	if err := sess.agent.Send(input); err != nil {
		sess.deleteImages(input.ImageIDs())
		return "", err
	}
	return input.MessageID, nil
}

// deleteImages deletes the images of a prompt that was dropped or never sent.
func (sess *session) deleteImages(ids []string) {
	if len(ids) == 0 {
		return
	}
	if err := sess.server.store.DeleteImages(ids); err != nil {
		log.Printf("session %s: delete images: %v", sess.id, err)
	}
}

// acceptMessage hands a message to the agent, which queues it behind every
// input it has not taken.
func (sess *session) acceptMessage(message agent.Input) error {
	if err := sess.startForInput(); err != nil {
		return err
	}
	message.MessageID = newID()
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
	errDeleting        = errors.New("session is being deleted")
	errShuttingDown    = errors.New("daemon is shutting down")
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

func (sess *session) start() error {
	start := sess.server.harnesses[sess.harness].Start
	if start == nil {
		return fmt.Errorf("unknown harness %q", sess.harness)
	}
	if sess.deleting != nil {
		return errDeleting
	}
	if info, err := os.Stat(sess.directory); err != nil || !info.IsDir() {
		return fmt.Errorf("session directory %s no longer exists", sess.directory)
	}
	toolNames, err := sess.server.store.ToolNames(sess.id)
	if err != nil {
		log.Printf("session %s: read history: %v", sess.id, err)
	}
	a, err := start(agent.Options{
		SessionID: sess.id,
		Workspace: sess.directory,
		Resume:    sess.resumeToken,
		ToolNames: toolNames,
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

// parked drops the agent that has ended, keeping what tells when its
// processes have exited, and tells every client.
func (sess *session) parked() {
	sess.exiting = slices.DeleteFunc(sess.exiting, func(reaped <-chan struct{}) bool {
		select {
		case <-reaped:
			return true
		default:
			return false
		}
	})
	sess.exiting = append(sess.exiting, sess.agent.Reaped())
	sess.agent = nil
	sess.update(agent.Update{Type: agent.UpdateParked, Time: time.Now()})
}

// destroy puts the session into deleting and stops the agent, then waits off
// the goroutine for every process the session started to exit and removes
// its worktree, so the goroutine keeps answering meanwhile. finishDelete
// takes the outcome back on the goroutine and replies. destroy refuses while
// the session has children; a child is created only on its parent's
// goroutine, which refuses while deleting, so none appears before the delete
// finishes. Nothing starts the agent while it waits, since only the goroutine
// starts it and it refuses while deleting. While deleting, the session makes
// no report, its untaken reports wait and its state holds back none of the
// other reports to its recipient, and reports to it wait.
func (sess *session) destroy(reply func(error)) {
	if sess.deleting != nil {
		reply(errDeleting)
		return
	}
	s := sess.server
	s.mu.Lock()
	if len(s.children(sess)) > 0 {
		s.mu.Unlock()
		reply(errors.New("session has child sessions"))
		return
	}
	// The session is deleting before it stops, so its stop makes no report
	// and delivers none to it.
	sess.deleting = &deleting{reply: reply}
	s.familyChangedLocked(s.reportsToLocked(sess))
	s.mu.Unlock()
	// The process stops first, so it records nothing after the events are gone.
	if sess.agent != nil {
		sess.stop()
	}
	exiting := sess.exiting
	sess.exiting = nil
	id, workspace, worktree := sess.id, sess.workspace, sess.worktree
	go func() {
		for _, reaped := range exiting {
			<-reaped
		}
		var err error
		if worktree != "" {
			err = removeWorktree(id, workspace, worktree)
		}
		if !sess.post(func() { sess.finishDelete(err) }) {
			reply(errShuttingDown)
		}
	}()
}

// finishDelete ends the delete in progress: it deletes the session and
// removes it from every client, dropping its untaken reports, or, when that
// fails, leaves the session parked and listed, with its reports and the
// reports to it delivered as if no delete had been tried. Either way it
// replies.
func (sess *session) finishDelete(err error) {
	reply := sess.deleting.reply
	if err == nil {
		if err = sess.server.store.DeleteSession(sess.id); err != nil {
			log.Printf("session %s: delete: %v", sess.id, err)
		}
	}
	s := sess.server
	if err != nil {
		s.mu.Lock()
		sess.deleting = nil
		s.familyChangedLocked(s.reportsToLocked(sess))
		s.familyChangedLocked(sess)
		s.mu.Unlock()
		reply(err)
		return
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
	reply(nil)
}

func (sess *session) end() {
	if sess.agent != nil {
		sess.stop()
	}
	sess.ended = true
}

// update records an update and, for a member session, tells every client.
// Every update is broadcast under Server.mu, where membership is decided, and
// a state or parked update as it sets the published state, so it and
// workspace/list reach every connection in one order, and the session's
// family learns of it in the same critical section.
func (sess *session) update(update agent.Update) {
	sess.history.record(update)
	notification := struct {
		SessionID string `json:"session_id"`
		agent.Update
	}{sess.id, update}
	s := sess.server
	s.mu.Lock()
	switch update.Type {
	case agent.UpdateState:
		sess.state = update.State
	case agent.UpdateParked:
		sess.state = agent.StateParked
	}
	// A session another session starts, child or top-level, is held outside
	// membership until its agent has accepted the first input; publication
	// then reports its state.
	if s.sessions[sess.id] == sess {
		s.broadcast("session/update", notification)
		if update.Type == agent.UpdateState || update.Type == agent.UpdateParked {
			s.familyChangedLocked(s.reportsToLocked(sess))
			s.familyChangedLocked(sess)
		}
	}
	s.mu.Unlock()
	switch {
	case update.Type == agent.UpdatePromptCancelled:
		sess.deleteImages(update.Images)
	case update.Type == agent.UpdatePrompt && update.From == "" && update.Reports == nil && sess.child == nil:
		sess.labelFrom(update.Text)
	}
	if sess.reporting != nil {
		sess.report(update)
	}
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
