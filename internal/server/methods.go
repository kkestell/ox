package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/claude"
	"github.com/kkestell/ox/internal/jsonrpc"
	"github.com/kkestell/ox/internal/store"
)

// Ox error codes.
const (
	operationFailed    = -32000
	notFound           = -32001
	sessionBusy        = -32002
	approvalNotPending = -32003
)

// handle runs one request line and returns the response to queue, or nil when
// there is none to send.
func (s *Server) handle(c *conn, line []byte) *jsonrpc.Response {
	request, invalid := jsonrpc.Parse(line)
	if invalid != nil {
		return invalid
	}
	// Operations require a request ID so the client receives the outcome.
	if len(request.ID) == 0 {
		return nil
	}
	id, params := request.ID, request.Params
	switch request.Method {
	case "workspace/create":
		return s.createWorkspace(id, params)
	case "workspace/list":
		return s.listWorkspaces(id)
	case "session/create":
		return s.createSession(id, params)
	case "session/get":
		return s.getSession(c, id, params)
	case "session/send":
		return s.sendSession(id, params)
	case "session/approve":
		return s.approveSession(id, params)
	case "session/configure":
		return s.configureSession(id, params)
	case "session/destroy":
		return s.destroySession(id, params)
	default:
		return jsonrpc.Failure(id, jsonrpc.MethodNotFound, "Method not found")
	}
}

// failure maps an operation error to its response.
func failure(id json.RawMessage, err error) *jsonrpc.Response {
	switch {
	case errors.Is(err, agent.ErrBusy):
		return jsonrpc.Failure(id, sessionBusy, "Session is busy")
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
	return jsonrpc.Failure(id, notFound, "Session not found")
}

// lookup finds and locks the session named by sessionID, or returns the
// response to send. The caller unlocks sess.mu.
func (s *Server) lookup(id json.RawMessage, sessionID string) (*session, *jsonrpc.Response) {
	sess := s.session(sessionID)
	if sess == nil {
		return nil, sessionNotFound(id)
	}
	sess.mu.Lock()
	if sess.destroyed {
		sess.mu.Unlock()
		return nil, sessionNotFound(id)
	}
	return sess, nil
}

func (s *Server) createWorkspace(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !filepath.IsAbs(input.Path) {
		return invalidParams(id, "path must be an absolute directory path")
	}
	path := filepath.Clean(input.Path)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return invalidParams(id, "path must be an existing directory")
	}
	type result struct {
		WorkspaceID string `json:"workspace_id"`
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.state.Workspaces {
		if w.Path == path {
			return jsonrpc.Success(id, result{w.ID})
		}
	}
	w := &store.Workspace{ID: rand.Text(), Path: path, Sessions: []*store.Session{}}
	s.state.Workspaces = append(s.state.Workspaces, w)
	if err := s.save(); err != nil {
		s.state.Workspaces = s.state.Workspaces[:len(s.state.Workspaces)-1]
		return failure(id, err)
	}
	s.broadcast("workspace/created", struct {
		WorkspaceID string `json:"workspace_id"`
		Path        string `json:"path"`
	}{w.ID, w.Path})
	return jsonrpc.Success(id, result{w.ID})
}

func (s *Server) listWorkspaces(id json.RawMessage) *jsonrpc.Response {
	type sessionInfo struct {
		SessionID      string `json:"session_id"`
		Model          string `json:"model"`
		Effort         string `json:"effort"`
		PermissionMode string `json:"permission_mode"`
		Parked         bool   `json:"parked"`
	}
	type workspaceInfo struct {
		WorkspaceID string        `json:"workspace_id"`
		Path        string        `json:"path"`
		Sessions    []sessionInfo `json:"sessions"`
	}

	type entry struct {
		info *sessionInfo
		sess *session
	}
	var entries []entry
	s.mu.Lock()
	workspaces := make([]workspaceInfo, len(s.state.Workspaces))
	for i, w := range s.state.Workspaces {
		workspaces[i] = workspaceInfo{WorkspaceID: w.ID, Path: w.Path, Sessions: make([]sessionInfo, len(w.Sessions))}
		for j, r := range w.Sessions {
			workspaces[i].Sessions[j] = sessionInfo{r.ID, r.Model, r.Effort, r.PermissionMode, true}
			entries = append(entries, entry{&workspaces[i].Sessions[j], s.sessions[r.ID]})
		}
	}
	s.mu.Unlock()

	for _, e := range entries {
		e.sess.mu.Lock()
		e.info.Parked = e.sess.live() == nil
		e.sess.mu.Unlock()
	}
	return jsonrpc.Success(id, struct {
		Workspaces []workspaceInfo `json:"workspaces"`
	}{workspaces})
}

func (s *Server) createSession(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		WorkspaceID    string `json:"workspace_id"`
		Model          string `json:"model"`
		Effort         string `json:"effort"`
		PermissionMode string `json:"permission_mode"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.WorkspaceID == "" {
		return invalidParams(id, "workspace_id is required")
	}
	// Claude silently falls back to its default for unrecognized effort values.
	if !validEffort(input.Effort) {
		return invalidParams(id, "Unknown effort level")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.workspace(input.WorkspaceID)
	if w == nil {
		return jsonrpc.Failure(id, notFound, "Workspace not found")
	}
	record := &store.Session{
		ID: claude.NewUUID(), Model: input.Model, Effort: input.Effort, PermissionMode: input.PermissionMode,
	}
	w.Sessions = append(w.Sessions, record)
	if err := s.save(); err != nil {
		w.Sessions = w.Sessions[:len(w.Sessions)-1]
		return failure(id, err)
	}
	s.sessions[record.ID] = s.newSession(record, w.Path)
	s.broadcast("session/created", struct {
		SessionID      string `json:"session_id"`
		WorkspaceID    string `json:"workspace_id"`
		Model          string `json:"model"`
		Effort         string `json:"effort"`
		PermissionMode string `json:"permission_mode"`
	}{record.ID, w.ID, record.Model, record.Effort, record.PermissionMode})
	return jsonrpc.Success(id, struct {
		SessionID string `json:"session_id"`
	}{record.ID})
}

// getSession returns a session's history and current state. Every connection
// already receives the session's updates; those queued before the response
// are in the history, and those after it are not.
func (s *Server) getSession(c *conn, id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" {
		return invalidParams(id, "session_id is required")
	}
	sess, missing := s.lookup(id, input.SessionID)
	if missing != nil {
		return missing
	}
	defer sess.mu.Unlock()
	result := func(snapshot agent.Snapshot) *jsonrpc.Response {
		history, err := sess.history.entries()
		if err != nil {
			return failure(id, err)
		}
		return jsonrpc.Success(id, struct {
			agent.Snapshot
			History []agent.Update `json:"history"`
		}{snapshot, history})
	}
	// A parked session has no process, so no update is in flight.
	parked := agent.Snapshot{State: "parked"}
	a := sess.live()
	if a == nil {
		return result(parked)
	}
	// The agent publishes updates on the goroutine that runs this callback, so
	// the response takes its place among them in the connection's queue.
	err := a.Inspect(func(snapshot agent.Snapshot) {
		c.queue(result(snapshot))
	})
	if errors.Is(err, agent.ErrExited) {
		return result(parked)
	}
	return nil
}

func (s *Server) sendSession(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" || strings.TrimSpace(input.Text) == "" {
		return invalidParams(id, "session_id and text are required")
	}
	sess, missing := s.lookup(id, input.SessionID)
	if missing != nil {
		return missing
	}
	defer sess.mu.Unlock()
	a, err := s.running(sess)
	if err != nil {
		return failure(id, err)
	}
	messageID, err := a.Send(input.Text)
	if errors.Is(err, agent.ErrExited) {
		// The process is parking or has exited; start the next one.
		<-a.Done()
		if a, err = s.running(sess); err != nil {
			return failure(id, err)
		}
		messageID, err = a.Send(input.Text)
	}
	if err != nil {
		return failure(id, err)
	}
	return jsonrpc.Success(id, struct {
		MessageID string `json:"message_id"`
	}{messageID})
}

func (s *Server) approveSession(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		SessionID string          `json:"session_id"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" || input.RequestID == "" || !validApprovalResponse(input.Response) {
		return invalidParams(id, "session_id, request_id and an allow or deny response are required")
	}
	sess, missing := s.lookup(id, input.SessionID)
	if missing != nil {
		return missing
	}
	a := sess.live()
	sess.mu.Unlock()
	if a == nil {
		return failure(id, agent.ErrApprovalNotFound)
	}
	err := a.Approve(input.RequestID, input.Response)
	if errors.Is(err, agent.ErrExited) {
		err = agent.ErrApprovalNotFound
	}
	if err != nil {
		return failure(id, err)
	}
	return jsonrpc.Success(id, struct{}{})
}

// validApprovalResponse checks the response shape while keeping Claude's
// fields as raw JSON.
func validApprovalResponse(raw json.RawMessage) bool {
	var response struct {
		Behavior           string          `json:"behavior"`
		UpdatedInput       json.RawMessage `json:"updatedInput"`
		UpdatedPermissions json.RawMessage `json:"updatedPermissions"`
		Message            string          `json:"message"`
		Interrupt          bool            `json:"interrupt"`
		ToolUseID          string          `json:"toolUseID"`
	}
	if json.Unmarshal(raw, &response) != nil || (response.Behavior != "allow" && response.Behavior != "deny") {
		return false
	}
	return (len(response.UpdatedInput) == 0 || response.UpdatedInput[0] == '{') &&
		(len(response.UpdatedPermissions) == 0 || response.UpdatedPermissions[0] == '[')
}

func (s *Server) configureSession(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		SessionID      string  `json:"session_id"`
		Model          *string `json:"model"`
		Effort         *string `json:"effort"`
		PermissionMode *string `json:"permission_mode"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" {
		return invalidParams(id, "session_id and configuration fields are required")
	}
	if input.Model == nil && input.Effort == nil && input.PermissionMode == nil {
		return invalidParams(id, "At least one of model, effort, or permission_mode is required")
	}
	if input.Model != nil && strings.TrimSpace(*input.Model) == "" {
		return invalidParams(id, "model must be nonblank")
	}
	if input.Effort != nil && !validEffort(*input.Effort) {
		return invalidParams(id, "Unknown effort level")
	}
	if input.PermissionMode != nil && strings.TrimSpace(*input.PermissionMode) == "" {
		return invalidParams(id, "permission_mode must be nonblank")
	}
	sess, missing := s.lookup(id, input.SessionID)
	if missing != nil {
		return missing
	}
	a := sess.live()
	sess.mu.Unlock()
	// A parked session, or one whose process exits first, takes the settings
	// when its next process starts.
	if a != nil {
		err := a.Configure(agent.Configuration{Model: input.Model, Effort: input.Effort, PermissionMode: input.PermissionMode})
		if err != nil && !errors.Is(err, agent.ErrExited) {
			return failure(id, err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	record := sess.record
	if input.Model != nil {
		record.Model = *input.Model
	}
	if input.Effort != nil {
		record.Effort = *input.Effort
	}
	if input.PermissionMode != nil {
		record.PermissionMode = *input.PermissionMode
	}
	if err := s.save(); err != nil {
		return failure(id, err)
	}
	return jsonrpc.Success(id, struct{}{})
}

func validEffort(effort string) bool {
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max", "ultracode":
		return true
	default:
		return false
	}
}

// destroySession forgets the session and stops its process. Claude's
// transcript stays on disk.
func (s *Server) destroySession(id, params json.RawMessage) *jsonrpc.Response {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" {
		return invalidParams(id, "session_id is required")
	}
	sess, missing := s.lookup(id, input.SessionID)
	if missing != nil {
		return missing
	}
	defer sess.mu.Unlock()

	s.mu.Lock()
	for _, w := range s.state.Workspaces {
		for i, r := range w.Sessions {
			if r == sess.record {
				w.Sessions = append(w.Sessions[:i], w.Sessions[i+1:]...)
			}
		}
	}
	delete(s.sessions, input.SessionID)
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		return failure(id, err)
	}

	sess.destroyed = true
	if a := sess.live(); a != nil {
		if err := a.Stop(); err != nil {
			return failure(id, err)
		}
	}
	if err := os.Remove(sess.history.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return failure(id, err)
	}
	s.broadcast("session/destroyed", struct {
		SessionID string `json:"session_id"`
	}{input.SessionID})
	return jsonrpc.Success(id, struct{}{})
}
