package server

import (
	"errors"
	"log"
	"slices"
	"time"

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
// each other. An auto archive also requires the family to be idle, checked
// with the flags set, so a pin, prompt, or unarchive since the sweep wins.
func (sess *session) archive(auto bool) error {
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
		s.mu.Lock()
		defer s.mu.Unlock()
		if auto && !s.familyIdleLocked(members) {
			return errNotIdle
		}
		return s.setArchivedLocked(members, true)
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

var errNotIdle = errors.New("session family is not idle")

// familyIdleLocked reports whether the family, a top-level session followed
// by every session beneath it, is due to be archived automatically: the
// top-level session is neither archived nor pinned, and every member is
// parked, not deleting, and unused for archiveAfter. The caller holds s.mu.
func (s *Server) familyIdleLocked(members []*session) bool {
	top := members[0]
	if top.archived || top.pinned {
		return false
	}
	var latest time.Time
	for _, m := range members {
		if m.state != agent.StateParked || m.deleting != nil {
			return false
		}
		if m.activeAt.After(latest) {
			latest = m.activeAt
		}
	}
	return time.Since(latest) >= s.archiveAfter
}

// familyLocked is sess followed by every session beneath it, breadth-first.
// The caller holds s.mu.
func (s *Server) familyLocked(sess *session) []*session {
	members := []*session{sess}
	for i := 0; i < len(members); i++ {
		members = append(members, s.children(members[i])...)
	}
	return members
}

// autoArchive archives each idle family, checking every tick until quit is
// closed.
func (s *Server) autoArchive() {
	defer close(s.swept)
	ticker := time.NewTicker(min(s.archiveAfter/10, time.Minute))
	defer ticker.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		var idle []*session
		for _, w := range s.workspaces {
			for _, sess := range w.sessions {
				if sess.child == nil && s.familyIdleLocked(s.familyLocked(sess)) {
					idle = append(idle, sess)
				}
			}
		}
		s.mu.Unlock()
		for _, sess := range idle {
			select {
			case <-s.quit:
				return
			default:
			}
			done := make(chan error, 1)
			if !sess.post(func() { done <- sess.archive(true) }) {
				continue
			}
			// A busy or no longer idle family is decided again next tick.
			err := <-done
			if err != nil && !errors.Is(err, agent.ErrBusy) && !errors.Is(err, errNotIdle) && !errors.Is(err, errShuttingDown) {
				log.Printf("session %s: auto-archive: %v", sess.id, err)
			}
		}
	}
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
	members := s.familyLocked(sess)
	if err := s.setArchivedLocked(members, false); err != nil {
		return err
	}
	now := time.Now()
	for _, m := range members {
		m.activeAt = now
	}
	return nil
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
