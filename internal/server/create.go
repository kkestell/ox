package server

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/jsonrpc"
	"github.com/kkestell/ox/internal/store"
)

// created is session/create's result.
type created struct {
	SessionID string `json:"session_id"`
	Existing  bool   `json:"existing,omitempty"`
}

// createTopLevel creates the top-level session record describes in the
// workspace. It admits the creation under s.mu, then, without it, makes the
// worktree, and, given first, launches the session and hands it first's
// message. It publishes the session under s.mu, deciding then whether the
// daemon, the workspace, and another session with the ID allow it. A
// session without first is stored there. A creation that does not publish
// discards its session and worktree before it returns, and Close waits for
// every admitted creation to return. It fills in record's directory and
// worktree.
func (s *Server) createTopLevel(workspaceID string, record *store.Session, worktree bool, first func(*session) agent.Input) (created, error) {
	s.mu.Lock()
	w, existing, err := s.admitSessionLocked(workspaceID, record)
	switch {
	case err != nil:
		s.mu.Unlock()
		return created{}, err
	case existing:
		s.mu.Unlock()
		return created{SessionID: record.ID, Existing: true}, nil
	}
	s.creations.Add(1)
	s.mu.Unlock()
	defer s.creations.Done()

	root := ""
	if worktree {
		log.Printf("session %s: creating worktree in %s", record.ID, s.worktrees)
		var directory string
		if root, directory, err = addWorktree(w.path, s.worktrees); err != nil {
			return created{}, err
		}
		record.Directory, record.Worktree = directory, root
	}

	var sess *session
	if first != nil {
		if sess, err = s.launch(w, record, first); err != nil {
			return created{}, s.abandonWorktree(w, record.ID, root, err)
		}
	}

	s.mu.Lock()
	switch {
	case s.closed:
		err = errShuttingDown
	case !slices.Contains(s.workspaces, w):
		err = errWorkspaceNotFound
	default:
		existing, err = s.existingLocked(w, record.ID)
	}
	if err == nil && !existing && sess == nil {
		sess, err = s.storeSession(w, record)
	}
	if err == nil && !existing {
		s.publishLocked(w, sess)
		s.mu.Unlock()
		go sess.run()
		return created{SessionID: record.ID}, nil
	}
	s.mu.Unlock()
	if sess != nil {
		s.discardUnpublished(sess)
	}
	var result created
	if existing {
		result = created{SessionID: record.ID, Existing: true}
	}
	return result, s.abandonWorktree(w, record.ID, root, err)
}

// admitSessionLocked checks a top-level session's creation and returns its
// workspace, setting record's directory to the workspace's path, or reports
// that record's ID names a session already in that workspace. The caller holds
// s.mu.
func (s *Server) admitSessionLocked(workspaceID string, record *store.Session) (w *workspace, existing bool, err error) {
	// Close ends the goroutine of every session that exists when it starts.
	if s.closed {
		return nil, false, errShuttingDown
	}
	w = s.workspace(workspaceID)
	if existing, err := s.existingLocked(w, record.ID); existing || err != nil {
		return nil, existing, err
	}
	harness, offered := s.config.Harnesses[record.Harness]
	if !offered {
		return nil, false, &codedError{jsonrpc.InvalidParams, fmt.Sprintf("harness %q is not configured", record.Harness)}
	}
	if err := harness.CheckModel(record.Model, record.Effort); err != nil {
		return nil, false, &codedError{jsonrpc.InvalidParams, err.Error()}
	}
	if w == nil {
		return nil, false, errWorkspaceNotFound
	}
	record.Directory = w.path
	return w, false, nil
}

// existingLocked reports whether sessionID names a session in w, and refuses
// one that names a session elsewhere. The caller holds s.mu.
func (s *Server) existingLocked(w *workspace, sessionID string) (bool, error) {
	existing := s.sessions[sessionID]
	switch {
	case existing == nil:
		return false, nil
	case w == nil || !slices.Contains(w.sessions, existing):
		return false, &codedError{jsonrpc.InvalidParams, "session_id belongs to another workspace"}
	}
	return true, nil
}

// storeSession stores the session record describes in w and builds it,
// unpublished. Its creation time is set here, just before it is published or
// launched, so the list and the store order sessions alike.
func (s *Server) storeSession(w *workspace, record *store.Session) (*session, error) {
	record.CreatedAt = time.Now()
	if err := s.store.AddSession(w.id, record); err != nil {
		log.Printf("session %s: store session: %v", record.ID, err)
		return nil, err
	}
	return s.newSession(record, w.path, ""), nil
}

// abandonWorktree removes the worktree at root, if any, of a creation that
// did not publish, and returns err joined with any failure to remove it.
func (s *Server) abandonWorktree(w *workspace, sessionID, root string, err error) error {
	if root == "" {
		return err
	}
	if discardErr := discardWorktree(w.path, root); discardErr != nil {
		if err == nil {
			log.Printf("session %s: %v", sessionID, discardErr)
		} else {
			err = errors.Join(err, discardErr)
		}
	}
	return err
}

// publishLocked adds sess to w and to the sessions requests reach, and tells
// every client. The caller holds s.mu and still owns sess, whose goroutine
// has not started.
func (s *Server) publishLocked(w *workspace, sess *session) {
	sess.state = sess.snapshot().State
	w.sessions = append(w.sessions, sess)
	s.sessions[sess.id] = sess
	s.broadcast("session/created", struct {
		WorkspaceID string `json:"workspace_id"`
		sessionInfo
	}{w.id, sess.infoLocked()})
}

// addChild checks that parent may have the child record names now, in the
// directory whose physical form is physical, or "" for parent's own, then adds
// it with text as its first message, on parent's goroutine. The checks hold
// until publication: only parent's goroutine creates its children or archives
// it, and its depth is fixed. Only shutdown can intervene, so it is decided at
// publication. The child is launched off s.mu and published only once its
// agent holds the first message. A launch or first Send that fails leaves no
// child.
func (s *Server) addChild(parent *session, record *store.Session, physical, text string) error {
	s.mu.Lock()
	err := s.checkChildLocked(parent, record.Child.Name, physical)
	// The parent is a member while its goroutine runs.
	w := s.workspaceOf(parent)
	label := parent.labelLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}

	sess, err := s.launch(w, record, func(sess *session) agent.Input { return messageInput(parent, label, sess, text) })
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.discardUnpublished(sess)
		return errShuttingDown
	}
	s.publishLocked(w, sess)
	s.mu.Unlock()
	go sess.run()
	return nil
}

// launch stores the session record describes in w, starts its agent, and
// hands it first's message, without s.mu. The session is not published, so
// the caller owns it until it publishes it or discards it with
// discardUnpublished. A failure leaves no agent or stored row.
func (s *Server) launch(w *workspace, record *store.Session, first func(*session) agent.Input) (*session, error) {
	sess, err := s.storeSession(w, record)
	if err != nil {
		return nil, err
	}
	err = sess.start()
	if err == nil {
		err = sess.acceptMessage(first(sess))
	}
	if err != nil {
		s.discardUnpublished(sess)
		return nil, err
	}
	return sess, nil
}

// discardUnpublished stops the agent of a session that was never published
// and waits for its processes to exit, so its directory can be removed, then
// deletes its stored row. sess.stop would publish parked for a session no
// client knows.
func (s *Server) discardUnpublished(sess *session) {
	if sess.agent != nil {
		sess.agent.Stop()
		<-sess.agent.Reaped()
	}
	if err := s.store.DeleteSession(sess.id); err != nil {
		log.Printf("session %s: delete unpublished session: %v", sess.id, err)
	}
}

// checkChildLocked reports why parent may not have a child named name now, in
// the directory whose physical form is physical, or "" for parent's own. Only
// the family of a worktree's owner works in it. The caller holds s.mu.
func (s *Server) checkChildLocked(parent *session, name, physical string) error {
	if slices.ContainsFunc(s.children(parent), func(other *session) bool { return other.child.Name == name }) {
		return fmt.Errorf("a child named %q already exists", name)
	}
	if err := s.checkStarterLocked(parent); err != nil {
		return err
	}
	if s.depthLocked(parent) >= s.config.MaxChildDepth {
		return fmt.Errorf("child sessions nest at most %d deep", s.config.MaxChildDepth)
	}
	if physical != "" {
		if owner := s.worktreeOwnerLocked(physical); owner != nil && owner != s.topLocked(parent) {
			return errors.New("dir is in another session's worktree")
		}
	}
	return nil
}

// checkStarterLocked reports why caller may not start a session now. The
// caller holds s.mu.
func (s *Server) checkStarterLocked(caller *session) error {
	if caller.archived {
		return errArchived
	}
	if caller.deleting != nil {
		return errDeleting
	}
	return nil
}

// checkTitle returns the title a session is started with, trimmed, or why it
// is refused.
func checkTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > titleRunes || strings.ContainsAny(title, "\r\n") {
		return "", fmt.Errorf("title must be one line of 1 to %d characters", titleRunes)
	}
	return title, nil
}

// startedRecord resolves the harness and settings of a session the caller
// starts, on the caller's goroutine, and checks them against the
// configuration. The session takes the caller's harness and permission mode,
// the caller's model on the caller's harness, and the caller's effort when
// its model offers it.
func (s *Server) startedRecord(caller *session, harnessName, model, effort string) (*store.Session, error) {
	if harnessName == "" {
		harnessName = caller.harness
	}
	harness, offered := s.config.Harnesses[harnessName]
	if !offered {
		return nil, fmt.Errorf("harness %q is not configured", harnessName)
	}
	if model == "" {
		if harnessName != caller.harness {
			return nil, errors.New("model is required with another harness")
		}
		model = caller.settings.Model
	}
	if effort == "" {
		effort = harness.Effort(model, caller.settings.Effort)
	}
	if err := harness.CheckModel(model, effort); err != nil {
		return nil, err
	}
	return &store.Session{
		ID: newID(), Harness: harnessName, Model: model, Effort: effort, PermissionMode: caller.settings.PermissionMode,
	}, nil
}
