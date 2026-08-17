package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/creachadair/jrpc2"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/workspace"
)

// session is one conversation. Its history and the state of the turn running in
// it are reachable only through the methods below, which keep the locking
// honest.
type session struct {
	id            string
	workspace     workspace.Workspace
	configuration config.Resolved

	mu      sync.Mutex
	history []openrouter.Message
	active  *turn
}

// turn is the prompt currently running in a session. cancelledByClient tells a
// client cancellation apart from any other reason the turn's context ended.
type turn struct {
	cancel            context.CancelFunc
	cancelledByClient bool
}

func (a *Agent) NewSession(
	_ context.Context,
	request acp.NewSessionRequest,
) (acp.NewSessionResponse, error) {
	if err := request.Validate(); err != nil {
		return acp.NewSessionResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	root, err := workspace.Canonical(request.CWD)
	if err != nil {
		return acp.NewSessionResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	sessionWorkspace := workspace.New(root)
	configuration, err := config.Resolve(a.environment, sessionWorkspace.Root())
	if err != nil {
		return acp.NewSessionResponse{}, jrpc2.Errorf(jrpc2.InternalError, "%v", err)
	}
	if problem := a.credentialProblem(); problem != "" {
		return acp.NewSessionResponse{}, authRequiredError(problem)
	}

	value := &session{
		id:            randomID(),
		workspace:     sessionWorkspace,
		configuration: configuration,
	}
	a.sessionsMu.Lock()
	a.sessions[value.id] = value
	a.sessionsMu.Unlock()

	a.logger.Info(
		"session created",
		"session_id", value.id,
		"cwd", value.workspace.Root(),
		"model", value.configuration.Model,
		"model_source", value.configuration.ModelSource,
	)
	return acp.NewSessionResponse{SessionID: value.id}, nil
}

// Cancel must never wait for the turn it cancels: jrpc2 does not dispatch the
// next input batch until every previously issued notification handler returns.
func (a *Agent) Cancel(_ context.Context, notification acp.CancelNotification) error {
	if err := notification.Validate(); err != nil {
		return jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}

	// jrpc2 discards the error a notification handler returns, so a session the
	// client does not know it has lost is logged rather than reported.
	value := a.findSession(notification.SessionID)
	if value == nil {
		a.logger.Warn("cancelling unknown session", "session_id", notification.SessionID)
		return nil
	}
	a.logger.Info(
		"cancelling session",
		"session_id", value.id,
		"turn_running", value.cancel(),
	)
	return nil
}

func (a *Agent) findSession(id string) *session {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	return a.sessions[id]
}

// claim makes this the turn running in the session, refusing a second one. The
// returned context ends when the turn does; the returned release ends the turn
// and reports whether the client cancelled it.
func (s *session) claim(parent context.Context) (context.Context, func() bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return nil, nil, errors.New("session is already running a prompt")
	}
	ctx, cancel := context.WithCancel(parent)
	active := &turn{cancel: cancel}
	s.active = active

	var once sync.Once
	var cancelledByClient bool
	release := func() bool {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			defer s.mu.Unlock()
			cancelledByClient = active.cancelledByClient
			if s.active == active {
				s.active = nil
			}
		})
		return cancelledByClient
	}
	return ctx, release, nil
}

// cancel ends the running turn and reports whether there was one.
func (s *session) cancel() bool {
	s.mu.Lock()
	active := s.active
	if active != nil {
		active.cancelledByClient = true
	}
	s.mu.Unlock()
	if active == nil {
		return false
	}
	active.cancel()
	return true
}

// appendMessage adds a message and returns the history length before it was
// added, so a refused turn can discard itself without disturbing earlier
// conversation.
func (s *session) appendMessage(message openrouter.Message) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	previousLength := len(s.history)
	s.history = append(s.history, message)
	return previousLength
}

func (s *session) truncateHistory(length int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if length < 0 || length > len(s.history) {
		panic("invalid session history length")
	}
	s.history = s.history[:length]
}

func (s *session) messages() []openrouter.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]openrouter.Message(nil), s.history...)
}

func randomID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data[:])
}
