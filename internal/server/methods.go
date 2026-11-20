package server

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/jsonrpc"
	"github.com/kkestell/ox/internal/store"
)

const (
	operationFailed    = -32000
	notFound           = -32001
	sessionBusy        = -32002
	approvalNotPending = -32003
)

type replyFunc func(*jsonrpc.Response)

// handle runs one request line. Each method queues its own response through
// reply, from wherever it holds the state the response reports.
func (s *Server) handle(reply replyFunc, line []byte) {
	request, invalid := jsonrpc.Parse(line)
	if invalid != nil {
		reply(invalid)
		return
	}
	// Operations require a request ID so the client receives the outcome.
	if len(request.ID) == 0 {
		return
	}
	id, params := request.ID, request.Params
	switch request.Method {
	case "workspace/create":
		s.createWorkspace(reply, id, params)
	case "workspace/list":
		s.listWorkspaces(reply, id)
	case "workspace/remove":
		s.removeWorkspace(reply, id, params)
	case "harness/list":
		reply(jsonrpc.Success(id, s.config))
	case "session/create":
		s.createSession(reply, id, params)
	case "session/get":
		s.getSession(reply, id, params)
	case "session/send":
		s.sendSession(reply, id, params)
	case "session/approve":
		s.approveSession(reply, id, params)
	case "session/interrupt":
		s.interruptSession(reply, id, params)
	case "session/configure":
		s.configureSession(reply, id, params)
	case "session/destroy":
		s.destroySession(reply, id, params)
	default:
		reply(jsonrpc.Failure(id, jsonrpc.MethodNotFound, "Method not found"))
	}
}

func failure(id json.RawMessage, err error) *jsonrpc.Response {
	switch {
	case errors.Is(err, agent.ErrBusy):
		return jsonrpc.Failure(id, sessionBusy, "session is busy")
	case errors.Is(err, agent.ErrApprovalNotFound):
		return jsonrpc.Failure(id, approvalNotPending, err.Error())
	default:
		return jsonrpc.Failure(id, operationFailed, err.Error())
	}
}

func invalidParams(id json.RawMessage, message string) *jsonrpc.Response {
	return jsonrpc.Failure(id, jsonrpc.InvalidParams, message)
}

func sessionNotFound(id json.RawMessage) *jsonrpc.Response {
	return jsonrpc.Failure(id, notFound, "session not found")
}

func shuttingDown(id json.RawMessage) *jsonrpc.Response {
	return jsonrpc.Failure(id, operationFailed, "daemon is shutting down")
}

func (s *Server) onSession(reply replyFunc, id json.RawMessage, sessionID string, op func(*session)) {
	s.mu.Lock()
	closed, sess := s.closed, s.sessions[sessionID]
	s.mu.Unlock()
	switch {
	case closed:
		reply(shuttingDown(id))
	case sess == nil || !sess.post(func() { op(sess) }):
		reply(sessionNotFound(id))
	}
}

func (s *Server) createWorkspace(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !filepath.IsAbs(input.Path) {
		reply(invalidParams(id, "path must be an absolute directory path"))
		return
	}
	// Keep the path as given: the filesystem resolves a symlink before any
	// following .., unlike filepath.Clean.
	path := input.Path
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		reply(invalidParams(id, "path must be an existing directory"))
		return
	}
	type result struct {
		WorkspaceID string `json:"workspace_id"`
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		reply(shuttingDown(id))
		return
	}
	// A symlink or a path that differs only in case can name a workspace's
	// directory.
	for _, w := range s.workspaces {
		if existing, err := os.Stat(w.path); err == nil && os.SameFile(info, existing) {
			reply(jsonrpc.Success(id, result{w.id}))
			return
		}
	}
	w := &workspace{id: rand.Text(), path: path, createdAt: time.Now()}
	if err := s.store.AddWorkspace(&store.Workspace{ID: w.id, Path: w.path, CreatedAt: w.createdAt}); err != nil {
		reply(failure(id, err))
		return
	}
	s.workspaces = append(s.workspaces, w)
	s.broadcast("workspace/created", struct {
		WorkspaceID string    `json:"workspace_id"`
		Path        string    `json:"path"`
		CreatedAt   time.Time `json:"created_at"`
	}{w.id, w.path, w.createdAt})
	reply(jsonrpc.Success(id, result{w.id}))
}

func (s *Server) listWorkspaces(reply replyFunc, id json.RawMessage) {
	type sessionInfo struct {
		SessionID string    `json:"session_id"`
		Label     string    `json:"label"`
		Harness   string    `json:"harness"`
		CreatedAt time.Time `json:"created_at"`
		Child     *child    `json:"child,omitempty"`
	}
	type workspaceInfo struct {
		WorkspaceID string        `json:"workspace_id"`
		Path        string        `json:"path"`
		CreatedAt   time.Time     `json:"created_at"`
		Sessions    []sessionInfo `json:"sessions"`
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	workspaces := make([]workspaceInfo, len(s.workspaces))
	for i, w := range s.workspaces {
		workspaces[i] = workspaceInfo{WorkspaceID: w.id, Path: w.path, CreatedAt: w.createdAt, Sessions: make([]sessionInfo, len(w.sessions))}
		for j, sess := range w.sessions {
			label, err := s.label(sess)
			if err != nil {
				reply(failure(id, err))
				return
			}
			workspaces[i].Sessions[j] = sessionInfo{sess.id, label, sess.harness, sess.createdAt, sess.child}
		}
	}
	reply(jsonrpc.Success(id, struct {
		Workspaces []workspaceInfo `json:"workspaces"`
	}{workspaces}))
}

func (s *Server) removeWorkspace(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.WorkspaceID == "" {
		reply(invalidParams(id, "workspace_id is required"))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		reply(shuttingDown(id))
		return
	}
	w := s.workspace(input.WorkspaceID)
	switch {
	case w == nil:
		reply(jsonrpc.Failure(id, notFound, "workspace not found"))
		return
	case len(w.sessions) > 0:
		reply(jsonrpc.Failure(id, operationFailed, "workspace has sessions"))
		return
	}
	if err := s.store.DeleteWorkspace(w.id); err != nil {
		reply(failure(id, err))
		return
	}
	s.workspaces = slices.DeleteFunc(s.workspaces, func(other *workspace) bool { return other == w })
	s.broadcast("workspace/removed", struct {
		WorkspaceID string `json:"workspace_id"`
	}{w.id})
	reply(jsonrpc.Success(id, struct{}{}))
}

func (s *Server) createSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID   string `json:"session_id"`
		WorkspaceID string `json:"workspace_id"`
		Harness     string `json:"harness"`
		agent.Settings
	}
	if err := json.Unmarshal(params, &input); err != nil || input.WorkspaceID == "" || input.Harness == "" {
		reply(invalidParams(id, "workspace_id, harness, model, and permission_mode are required"))
		return
	}
	if input.SessionID != "" {
		if _, err := hex.DecodeString(input.SessionID); len(input.SessionID) != 32 || err != nil {
			reply(invalidParams(id, "session_id must be 32 hexadecimal digits"))
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Close ends the goroutine of every session that exists when it starts.
	if s.closed {
		reply(shuttingDown(id))
		return
	}
	w := s.workspace(input.WorkspaceID)
	type result struct {
		SessionID string `json:"session_id"`
		Existing  bool   `json:"existing,omitempty"`
	}
	sessionID := input.SessionID
	if sessionID == "" {
		sessionID = rand.Text()
	} else if existing := s.sessions[sessionID]; existing != nil {
		if w == nil || !slices.Contains(w.sessions, existing) {
			reply(invalidParams(id, "session_id belongs to another workspace"))
			return
		}
		reply(jsonrpc.Success(id, result{SessionID: sessionID, Existing: true}))
		return
	}
	harness, offered := s.config.Harnesses[input.Harness]
	if !offered {
		reply(invalidParams(id, fmt.Sprintf("harness %q is not configured", input.Harness)))
		return
	}
	if err := cmp.Or(harness.CheckModel(input.Model, input.Effort), harness.CheckPermissionMode(input.PermissionMode)); err != nil {
		reply(invalidParams(id, err.Error()))
		return
	}

	if w == nil {
		reply(jsonrpc.Failure(id, notFound, "workspace not found"))
		return
	}
	record := &store.Session{
		ID: sessionID, Harness: input.Harness, Model: input.Model, Effort: input.Effort, PermissionMode: input.PermissionMode,
		CreatedAt: time.Now(),
	}
	if err := s.store.AddSession(w.id, record); err != nil {
		reply(failure(id, err))
		return
	}
	sess := s.addSession(w, record)
	reply(jsonrpc.Success(id, result{SessionID: sess.id}))
}

// addSession starts a stored session in w and tells every client. The caller
// holds s.mu.
func (s *Server) addSession(w *workspace, record *store.Session) *session {
	sess := s.newSession(record, w.path)
	w.sessions = append(w.sessions, sess)
	s.sessions[sess.id] = sess
	s.broadcast("session/created", struct {
		SessionID   string    `json:"session_id"`
		WorkspaceID string    `json:"workspace_id"`
		Harness     string    `json:"harness"`
		CreatedAt   time.Time `json:"created_at"`
		Child       *child    `json:"child,omitempty"`
	}{sess.id, w.id, sess.harness, sess.createdAt, sess.child})
	return sess
}

// getSession returns a session's settings, history, and current state. Every
// connection already receives the session's updates; those queued before the
// response are in the history, and those after it are not.
func (s *Server) getSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" {
		reply(invalidParams(id, "session_id is required"))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		history, err := sess.history.entries()
		if err != nil {
			reply(failure(id, err))
			return
		}
		reply(jsonrpc.Success(id, struct {
			agent.Snapshot
			agent.Settings
			History []agent.Update `json:"history"`
		}{sess.snapshot(), sess.settings, history}))
	})
}

func (s *Server) sendSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" || strings.TrimSpace(input.Text) == "" {
		reply(invalidParams(id, "session_id and text are required"))
		return
	}
	if len(input.Text) > 1<<20 {
		reply(invalidParams(id, "prompt is larger than 1 MiB"))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		if check := s.harnesses[sess.harness].CheckPrompt; check != nil {
			if err := check(input.Text); err != nil {
				reply(invalidParams(id, err.Error()))
				return
			}
		}
		messageID, err := sess.sendPrompt(input.Text)
		if err != nil {
			reply(failure(id, err))
			return
		}
		reply(jsonrpc.Success(id, struct {
			MessageID string `json:"message_id"`
		}{messageID}))
	})
}

func (s *Server) approveSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string          `json:"session_id"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" || input.RequestID == "" || len(input.Response) == 0 {
		reply(invalidParams(id, "session_id, request_id, and response are required"))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		if sess.agent == nil {
			reply(failure(id, agent.ErrApprovalNotFound))
			return
		}
		if err := sess.agent.Approve(input.RequestID, input.Response); err != nil {
			reply(failure(id, err))
			return
		}
		reply(jsonrpc.Success(id, struct{}{}))
	})
}

func (s *Server) interruptSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" {
		reply(invalidParams(id, "session_id is required"))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		if sess.agent != nil {
			if err := sess.agent.Interrupt(); err != nil {
				reply(failure(id, err))
				return
			}
		}
		reply(jsonrpc.Success(id, struct{}{}))
	})
}

func (s *Server) configureSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string `json:"session_id"`
		agent.SettingsPatch
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" ||
		(input.Model == nil && input.Effort == nil && input.PermissionMode == nil) {
		reply(invalidParams(id, "session_id and at least one of model, effort, and permission_mode are required"))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		settings := input.Apply(sess.settings)
		// Only the settings the request changes are checked, so a stored
		// setting the configuration no longer offers can stay.
		harness := s.config.Harnesses[sess.harness]
		var err error
		if input.Model != nil || input.Effort != nil {
			err = harness.CheckModel(settings.Model, settings.Effort)
		}
		if input.PermissionMode != nil {
			err = cmp.Or(err, harness.CheckPermissionMode(settings.PermissionMode))
		}
		if err != nil {
			reply(invalidParams(id, err.Error()))
			return
		}
		if sess.agent == nil {
			sess.adopt(settings)
			reply(jsonrpc.Success(id, struct{}{}))
			return
		}
		sess.agent.Configure(input.SettingsPatch, func(err error) {
			if errors.Is(err, agent.ErrExited) {
				// Retain any changes the harness made in the meantime.
				sess.adopt(input.Apply(sess.settings))
				err = nil
			}
			if err != nil {
				reply(failure(id, err))
				return
			}
			reply(jsonrpc.Success(id, struct{}{}))
		})
	})
}

func (s *Server) destroySession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" {
		reply(invalidParams(id, "session_id is required"))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		if err := sess.destroy(); err != nil {
			reply(failure(id, err))
			return
		}
		reply(jsonrpc.Success(id, struct{}{}))
	})
}
