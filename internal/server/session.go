package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	child     *store.Child // nil for a top-level session
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

	// Owned by run.
	settings    agent.Settings
	resumeToken string
	history     history
	agent       agent.Agent // nil while parked
	parkTimer   *time.Timer // armed while the agent is parkable
	ended       bool
	toParent    *toParent // nil for a top-level session
}

// toParent is what a child keeps for its parent: the reports its parent has
// not taken, oldest first, and its current turn.
type toParent struct {
	reports []agent.Report
	// turn is the turn in progress, or the next one while the child is free.
	// A turn's result or exit replaces it.
	turn *turn
}

// turn is one turn of a child: whether its current step told the parent
// itself.
type turn struct {
	told told
}

// told tracks a child's send_message to its parent through the step that made
// it. The call's own tool_use may arrive before or after the call sets
// toldSent; its tool_result always follows, and the next step's tool_use
// follows that, which clears it.
type told int

const (
	notTold      told = iota
	toldSent          // the parent accepted the message; its tool_result has not arrived
	toldAnswered      // a tool_result arrived since; the next tool_use is a later step
)

// reportRunes is how much of a child's text a report carries.
const reportRunes = 400

// firstLine is the line of a prompt that labels its session, shortened.
func firstLine(text string) string {
	return shorten(leadingLine(text))
}

// leadingLine is text's first line once surrounding space is trimmed.
func leadingLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// shorten cuts line to the length of a child's title, ending it with an
// ellipsis when it is cut.
func shorten(line string) string {
	runes := []rune(line)
	if len(runes) <= titleRunes {
		return line
	}
	return strings.TrimRightFunc(string(runes[:titleRunes-1]), unicode.IsSpace) + "…"
}

// cleanTitle is the title in a titling model's reply: its first line, without
// enclosing quotes, shortened, or empty when it has none.
func cleanTitle(reply string) string {
	title := leadingLine(reply)
	if len(title) >= 2 && strings.ContainsRune("\"'`", rune(title[0])) && title[len(title)-1] == title[0] {
		title = title[1 : len(title)-1]
	}
	return shorten(strings.TrimSpace(title))
}

// labelLocked is the session's label: a child's title, or else its generated
// title, or else the first nonblank first line of the prompts the user sent
// it, or the start of its ID before it has one. The caller holds Server.mu.
func (sess *session) labelLocked() string {
	switch {
	case sess.child != nil:
		return sess.child.Title
	case sess.title != "":
		return sess.title
	case sess.firstLine != "":
		return sess.firstLine
	}
	return sess.id[:8]
}

// broadcastLabelLocked tells every client the session's label. The caller
// holds Server.mu.
func (sess *session) broadcastLabelLocked() {
	sess.server.broadcast("session/labeled", struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label"`
	}{sess.id, sess.labelLocked()})
}

// newSession builds a stored session, whose goroutine its caller starts.
// firstLine is the first nonblank first line of the prompts the user sent it,
// or "".
func (s *Server) newSession(record *store.Session, workspace, firstLine string) *session {
	sess := &session{
		server: s, id: record.ID, harness: record.Harness, workspace: workspace, child: record.Child, createdAt: record.CreatedAt,
		ops: make(chan func()), done: make(chan struct{}), archived: record.Archived, firstLine: firstLine, title: record.Title,
		state:       agent.StateParked,
		settings:    agent.Settings{Model: record.Model, Effort: record.Effort, PermissionMode: record.PermissionMode},
		resumeToken: record.ResumeToken,
		history:     history{store: s.store, sessionID: record.ID},
	}
	sess.ctx, sess.cancel = context.WithCancel(context.Background())
	if record.Child != nil {
		sess.toParent = &toParent{turn: &turn{}}
	}
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
			images[i].ID = rand.Text()
			stored[i] = store.Image{ID: images[i].ID, MimeType: images[i].MimeType, Data: images[i].Data}
		}
		if err := sess.server.store.AddImages(sess.id, stored); err != nil {
			return "", err
		}
	}
	if messageID == "" {
		messageID = rand.Text()
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

// free reports whether the session is idle, in the background, or parked.
func (sess *session) free() bool {
	state := sess.snapshot().State
	return state == agent.StateIdle || state == agent.StateBackground || state == agent.StateParked
}

// held is a session whose goroutine waits in hold until it is released.
type held struct {
	// free is whether the session was free when held.
	free    bool
	release chan<- bool
	// done is closed once the session has handled its release, including any
	// stop.
	done <-chan struct{}
}

// hold holds the session's goroutine until it is released through the result.
// Released with true, the session stops its agent before it goes on. hold
// reports false once the session has ended.
func (sess *session) hold() (held, bool) {
	free := make(chan bool)
	release := make(chan bool, 1)
	done := make(chan struct{})
	if !sess.post(func() {
		defer close(done)
		free <- sess.free()
		if <-release && sess.agent != nil {
			sess.stop()
		}
	}) {
		return held{}, false
	}
	return held{<-free, release, done}, true
}

// archive archives sess and every session beneath it, once each is free, and
// stops their agents, returning once every one has stopped. It runs on sess's
// goroutine. It walks the family breadth-first, holding each descendant's
// goroutine before it reads that descendant's children, and holds them all
// while it checks them and sets their flags, so none starts in between. A
// child is created only on its parent's goroutine, so the children read while
// their parent is running this or held are all of them. An archive posts only
// from an ancestor to its descendants, so two in one family cannot wait on
// each other.
func (sess *session) archive() error {
	s := sess.server
	members, holds := []*session{sess}, []held{}
	for i := 0; i < len(members); i++ {
		s.mu.Lock()
		children := s.children(members[i])
		s.mu.Unlock()
		for _, c := range children {
			if h, ok := c.hold(); ok {
				members, holds = append(members, c), append(holds, h)
			}
		}
	}
	err := func() error {
		if !sess.free() || slices.ContainsFunc(holds, func(h held) bool { return !h.free }) {
			return agent.ErrBusy
		}
		return s.setArchived(members, true)
	}()
	if err == nil && sess.agent != nil {
		sess.stop()
	}
	for _, h := range holds {
		h.release <- err == nil
	}
	for _, h := range holds {
		<-h.done
	}
	return err
}

// unarchiveLocked unarchives the session with sessionID and every session
// beneath it. A child of an archived session stays archived, so an unarchived
// session's ancestors are all unarchived. The caller holds s.mu.
func (s *Server) unarchiveLocked(sessionID string) error {
	sess := s.sessions[sessionID]
	if sess == nil {
		return errSessionNotFound
	}
	if sess.child != nil {
		if parent := s.sessions[sess.child.ParentID]; parent != nil && parent.archived {
			return errors.New("parent session is archived")
		}
	}
	members := []*session{sess}
	for i := 0; i < len(members); i++ {
		members = append(members, s.children(members[i])...)
	}
	return s.setArchivedLocked(members, false)
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

// update records an update and tells every client. A state or parked update
// is broadcast under Server.mu as it sets the published state, so it and
// workspace/list reach every connection in one order.
func (sess *session) update(update agent.Update) {
	sess.history.record(update)
	notification := struct {
		SessionID string `json:"session_id"`
		agent.Update
	}{sess.id, update}
	s := sess.server
	switch update.Type {
	case agent.UpdateState, agent.UpdateParked:
		state := update.State
		if update.Type == agent.UpdateParked {
			state = agent.StateParked
		}
		s.mu.Lock()
		sess.state = state
		s.broadcast("session/update", notification)
		s.mu.Unlock()
	default:
		s.broadcast("session/update", notification)
	}
	switch {
	case update.Type == agent.UpdatePromptCancelled:
		sess.deleteImages(update.Images)
	case update.Type == agent.UpdatePrompt && update.From == "" && update.Reports == nil && sess.child == nil:
		sess.labelFrom(update.Text)
	}
	if sess.toParent != nil {
		sess.report(update)
	}
	if update.Type == agent.UpdateParked || update.Type == agent.UpdateState && update.State != agent.StateRunning {
		sess.pingFamily()
	}
}

// report keeps a child's report of a turn's result, unless the child told its
// parent itself in the turn's last step, or of an exit, and moves told. A
// turn's result or exit starts the next turn.
func (sess *session) report(update agent.Update) {
	t := sess.toParent
	var text string
	made := false
	switch update.Type {
	case agent.UpdateToolResult:
		if t.turn.told == toldSent {
			t.turn.told = toldAnswered
		}
	case agent.UpdateToolUse:
		if t.turn.told == toldAnswered {
			t.turn.told = notTold
		}
	case agent.UpdateResult:
		if made = t.turn.told == notTold; made {
			text = update.Result.Text
			if update.Result.IsError {
				text = "Turn failed: " + text
			}
		}
		t.turn = &turn{}
	case agent.UpdateExited:
		text, made = "Exited: "+update.Error, true
		t.turn = &turn{}
	}
	if !made {
		return
	}
	if n := utf8.RuneCountInString(text); n > reportRunes {
		text = fmt.Sprintf("%s…\n(first %d of %d characters; read the rest with read_session on %s.)",
			string([]rune(text)[:reportRunes]), reportRunes, n, sess.id)
	}
	t.reports = append(t.reports, agent.Report{From: sess.id, FromLabel: sess.child.Title, Text: text})
}

// pingFamily has the session's parent, and the session itself when it has
// children, try to deliver reports, now that it has stopped working.
func (sess *session) pingFamily() {
	s := sess.server
	s.mu.Lock()
	var targets []*session
	if sess.child != nil {
		if parent := s.sessions[sess.child.ParentID]; parent != nil {
			targets = append(targets, parent)
		}
	}
	if len(s.children(sess)) > 0 {
		targets = append(targets, sess)
	}
	s.mu.Unlock()
	for _, target := range targets {
		// A later operation, so the session never waits on another here.
		go target.post(target.deliverReports)
	}
}

// deliverReports sends the session every untaken report of its children as
// one input, once it is free and none of its children is running. It runs on
// the session's goroutine. A free session has no input untaken, so messages
// already waiting go first. It waits on each child's goroutine; a session
// goroutine waits on another only from an ancestor to a descendant, so no
// chain of waits returns to it.
func (sess *session) deliverReports() {
	if !sess.free() {
		return
	}
	s := sess.server
	s.mu.Lock()
	closed, archived, children := s.closed, sess.archived, s.children(sess)
	running := slices.ContainsFunc(children, func(c *session) bool { return c.state == agent.StateRunning })
	s.mu.Unlock()
	if closed || archived || running {
		return
	}
	slices.SortFunc(children, func(a, b *session) int { return a.createdAt.Compare(b.createdAt) })
	var reports []agent.Report
	for _, c := range children {
		taken := ask(c, func() []agent.Report {
			taken := c.toParent.reports
			c.toParent.reports = nil
			return taken
		})
		reports = append(reports, taken...)
	}
	if len(reports) == 0 {
		return
	}
	if err := sess.acceptMessage(reportsInput(reports)); err != nil {
		log.Printf("session %s: deliver reports: %v", sess.id, err)
	}
}

// ask runs f on the session's goroutine and returns its result, or the zero
// value once the session has ended.
func ask[T any](sess *session, f func() T) T {
	result := make(chan T, 1)
	if !sess.post(func() { result <- f() }) {
		var zero T
		return zero
	}
	return <-result
}

// labelFrom labels a top-level session with the first prompt the user sent it
// whose first line is not blank, tells every client, and starts titling it.
// Later prompts leave the label as it is.
func (sess *session) labelFrom(prompt string) {
	line := firstLine(prompt)
	s := sess.server
	s.mu.Lock()
	if sess.firstLine != "" || line == "" {
		s.mu.Unlock()
		return
	}
	sess.firstLine = line
	sess.broadcastLabelLocked()
	s.mu.Unlock()
	go sess.generateTitle(prompt)
}

const (
	// titleInstructions is the system prompt the titling model writes a
	// title with.
	titleInstructions = "Write a title of at most 48 characters for a coding session that begins with the user's message. " +
		"Reply with the title alone, in plain text, without quotes or a trailing period."
	// titleInputRunes is how much of the first prompt the titling model reads.
	titleInputRunes = 4000
	// titleLimit is how long titling may take.
	titleLimit = time.Minute
)

// generateTitle asks the titling model for a title from prompt and hands it
// to the session. It runs on its own goroutine and touches no session state.
// It gives up when the session ends.
func (sess *session) generateTitle(prompt string) {
	if runes := []rune(prompt); len(runes) > titleInputRunes {
		prompt = string(runes[:titleInputRunes])
	}
	ctx, cancel := context.WithTimeout(sess.ctx, titleLimit)
	defer cancel()
	titling := sess.server.config.Titling
	reply, err := sess.server.ask(ctx, titling.Model, titling.Effort, titleInstructions, prompt)
	title := cleanTitle(reply)
	switch {
	case err != nil:
	case title == "":
		err = errors.New("empty reply")
	case !sess.post(func() { sess.setTitle(title) }):
		err = errors.New("session ended")
	}
	if err != nil {
		log.Printf("session %s: title: %v", sess.id, err)
	}
}

// setTitle labels the session with its generated title, kept even when
// storing it fails, and tells every client.
func (sess *session) setTitle(title string) {
	if err := sess.server.store.SetTitle(sess.id, title); err != nil {
		log.Printf("session %s: store title: %v", sess.id, err)
	}
	s := sess.server
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.title = title
	sess.broadcastLabelLocked()
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
