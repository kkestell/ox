package client

import (
	protocol "github.com/coder/acp-go-sdk"
)

// Session is the client's state of the one session it shows. It is not safe
// for concurrent use; Conn's requests are.
type Session struct {
	conn   *Conn
	ID     protocol.SessionId
	active bool
	// Busy is true while a prompt's turn runs.
	Busy       bool
	cancelling bool
	// Permission is the request waiting for an answer. The server runs tool
	// calls in order, so at most one is pending.
	Permission    *Permission
	ConfigOptions []protocol.SessionConfigOption
	// Commands are the names in the latest available commands update.
	Commands []string
	Usage    *protocol.SessionUsageUpdate
	// Queued is the prompt sent during a turn, held until the cancelled turn
	// finishes; empty when none is.
	Queued string
}

// NewSession returns the state of a session the server just created.
func NewSession(conn *Conn, created protocol.NewSessionResponse) *Session {
	s := &Session{conn: conn}
	s.Opened(created.SessionId, created.ConfigOptions)
	return s
}

// Active reports whether a session is open.
func (s *Session) Active() bool { return s.active }

// Accepts reports whether a message for id belongs to the open session.
func (s *Session) Accepts(id protocol.SessionId) bool {
	return s.active && s.ID == id
}

// Opened records a session the server created or loaded.
func (s *Session) Opened(id protocol.SessionId, options []protocol.SessionConfigOption) {
	s.ID = id
	s.active = true
	s.ConfigOptions = options
}

// Closed records that the server closed the session.
func (s *Session) Closed() error {
	permission := s.Permission
	*s = Session{conn: s.conn, ID: s.ID}
	if permission != nil {
		return permission.Cancel()
	}
	return nil
}

// Prompt sends the prompt, or cancels the running turn and sends the prompt
// when that turn finishes. It returns false while another prompt is already
// queued.
func (s *Session) Prompt(text string) (bool, error) {
	if s.Busy {
		if s.Queued != "" {
			return false, nil
		}
		s.Queued = text
		return true, s.Cancel()
	}
	s.Busy = true
	conn, id := s.conn, s.ID
	go func() { conn.push(Finished{SessionID: id, Err: conn.prompt(id, text)}) }()
	return true, nil
}

// Ask holds a permission request for an answer, or cancels it while the turn
// is being cancelled.
func (s *Session) Ask(permission *Permission) error {
	if s.cancelling {
		return permission.Cancel()
	}
	if s.Permission != nil {
		panic("a permission request arrived while another was pending")
	}
	s.Permission = permission
	return nil
}

// Answer selects the pending request's option with the 1-based number and
// reports whether there was one.
func (s *Session) Answer(number int) (bool, error) {
	if s.Permission == nil || number < 1 || number > len(s.Permission.Options) {
		return false, nil
	}
	permission := s.Permission
	s.Permission = nil
	selected := &protocol.RequestPermissionOutcomeSelected{Outcome: "selected", OptionId: permission.Options[number-1].OptionId}
	return true, permission.respond(protocol.RequestPermissionOutcome{Selected: selected})
}

// Cancel cancels the pending permission request and the running turn.
func (s *Session) Cancel() error {
	s.cancelling = s.Busy
	if permission := s.Permission; permission != nil {
		s.Permission = nil
		if err := permission.Cancel(); err != nil {
			return err
		}
	}
	if s.Busy {
		return s.conn.cancel(s.ID)
	}
	return nil
}

// Finished ends the turn and sends the queued prompt, returning its text.
func (s *Session) Finished() (string, error) {
	// A completed turn cannot leave an unanswered permission behind.
	if permission := s.Permission; permission != nil {
		s.Permission = nil
		if err := permission.Cancel(); err != nil {
			return "", err
		}
	}
	s.Busy = false
	s.cancelling = false
	queued := s.Queued
	s.Queued = ""
	if queued != "" {
		if _, err := s.Prompt(queued); err != nil {
			return "", err
		}
	}
	return queued, nil
}

// Update records the session settings, available commands, and usage an
// update carries.
func (s *Session) Update(update protocol.SessionUpdate) {
	switch {
	case update.ConfigOptionUpdate != nil:
		s.ConfigOptions = update.ConfigOptionUpdate.ConfigOptions
	case update.AvailableCommandsUpdate != nil:
		s.Commands = nil
		for _, command := range update.AvailableCommandsUpdate.AvailableCommands {
			s.Commands = append(s.Commands, command.Name)
		}
	case update.UsageUpdate != nil:
		s.Usage = update.UsageUpdate
	}
}
