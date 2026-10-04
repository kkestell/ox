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

// Prompt sends the prompt. It must not be called while a turn runs.
func (s *Session) Prompt(text string) {
	if s.Busy {
		panic("a prompt was sent while a turn was running")
	}
	s.Busy = true
	conn, id := s.conn, s.ID
	go func() { conn.push(Finished{SessionID: id, Err: conn.prompt(id, text)}) }()
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

// Finished ends the turn.
func (s *Session) Finished() error {
	s.Busy = false
	s.cancelling = false
	// A completed turn cannot leave an unanswered permission behind.
	if permission := s.Permission; permission != nil {
		s.Permission = nil
		return permission.Cancel()
	}
	return nil
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
