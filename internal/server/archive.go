package server

import (
	"errors"
	"log"
	"slices"

	"github.com/kkestell/ox/internal/agent"
)

// free reports whether the session is parked or its agent is idle.
func (sess *session) free() bool {
	return sess.agent == nil || sess.agent.Idle()
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

func (sess *session) isArchived() bool {
	sess.server.mu.Lock()
	defer sess.server.mu.Unlock()
	return sess.archived
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
		return errShuttingDown
	}
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.id
	}
	if err := s.store.SetArchived(ids, archived); err != nil {
		log.Printf("session %s: store archived: %v", members[0].id, err)
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
	for _, m := range members {
		s.familyChangedLocked(m)
	}
	return nil
}
