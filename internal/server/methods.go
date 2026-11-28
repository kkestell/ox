package server

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
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

// catalog is the choices `harness/list` and the `list_models` tool serve.
type catalog struct {
	Harnesses       map[string]config.Harness `json:"harnesses"`
	PermissionModes []config.Option           `json:"permission_modes"`
	Defaults        config.Defaults           `json:"defaults"`
}

// permissionModes are the permission modes Ox offers for every harness.
var permissionModes = []config.Option{{ID: string(agent.Manual), Name: "Manual"}, {ID: string(agent.Auto), Name: "Auto"}}

func (s *Server) catalog() catalog {
	return catalog{Harnesses: s.config.Harnesses, PermissionModes: permissionModes, Defaults: s.config.Defaults}
}

// handle runs one request. Each method queues its own response through
// reply, from wherever it holds the state the response reports.
func (s *Server) handle(reply replyFunc, request jsonrpc.Request) {
	id, params := request.ID, request.Params
	switch request.Method {
	case "workspace/create":
		s.createWorkspace(reply, id, params)
	case "workspace/list":
		s.listWorkspaces(reply, id)
	case "workspace/remove":
		s.removeWorkspace(reply, id, params)
	case "workspace/list_folder":
		s.listFolder(reply, id, params)
	case "workspace/read_file":
		s.readFile(reply, id, params)
	case "workspace/list_changes":
		s.listChanges(reply, id, params)
	case "harness/list":
		reply(jsonrpc.Success(id, s.catalog()))
	case "session/create":
		s.createSession(reply, id, params)
	case "session/get":
		s.getSession(reply, id, params)
	case "session/send":
		s.sendSession(reply, id, params)
	case "session/image":
		s.imageSession(reply, id, params)
	case "session/approve":
		s.approveSession(reply, id, params)
	case "session/interrupt":
		s.interruptSession(reply, id, params)
	case "session/configure":
		s.configureSession(reply, id, params)
	case "session/destroy":
		s.destroySession(reply, id, params)
	case "session/archive":
		s.archiveSession(reply, id, params)
	case "session/unarchive":
		s.unarchiveSession(reply, id, params)
	case "session/pin":
		s.pinSession(reply, id, params, true)
	case "session/unpin":
		s.pinSession(reply, id, params, false)
	default:
		reply(jsonrpc.Failure(id, jsonrpc.MethodNotFound, "Method not found"))
	}
}

// codedError is an error whose reply carries code.
type codedError struct {
	code    int
	message string
}

func (e *codedError) Error() string { return e.message }

var errWorkspaceNotFound = &codedError{notFound, "workspace not found"}

func failure(id json.RawMessage, err error) *jsonrpc.Response {
	var coded *codedError
	switch {
	case errors.As(err, &coded):
		return jsonrpc.Failure(id, coded.code, err.Error())
	case errors.Is(err, agent.ErrBusy):
		return jsonrpc.Failure(id, sessionBusy, "session is busy")
	case errors.Is(err, agent.ErrApprovalNotFound):
		return jsonrpc.Failure(id, approvalNotPending, err.Error())
	case errors.Is(err, errSessionNotFound):
		return sessionNotFound(id)
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

// sessionIDParam reads params that hold only a session_id, and reports false
// when it is missing.
func sessionIDParam(params json.RawMessage) (string, bool) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" {
		return "", false
	}
	return input.SessionID, true
}

// onSessionID runs op on the goroutine of the session params name.
func (s *Server) onSessionID(reply replyFunc, id, params json.RawMessage, op func(*session)) {
	sessionID, ok := sessionIDParam(params)
	if !ok {
		reply(invalidParams(id, "session_id is required"))
		return
	}
	s.onSession(reply, id, sessionID, op)
}

// replyEmpty returns a function that answers a request whose success has no
// result.
func replyEmpty(reply replyFunc, id json.RawMessage) func(error) {
	return func(err error) {
		if err != nil {
			reply(failure(id, err))
			return
		}
		reply(jsonrpc.Success(id, struct{}{}))
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
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		reply(failure(id, err))
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
	if s.worktreeOwnerLocked(physical) != nil {
		reply(invalidParams(id, "path is inside a session's worktree"))
		return
	}
	if w := s.workspaceAtLocked(info); w != nil {
		reply(jsonrpc.Success(id, result{w.id}))
		return
	}
	w := &workspace{id: newID(), path: path, createdAt: time.Now()}
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

// workspaceAtLocked returns the workspace whose directory info describes, or
// nil. A symlink or a path that differs only in case can name a workspace's
// directory. The caller holds s.mu.
func (s *Server) workspaceAtLocked(info os.FileInfo) *workspace {
	for _, w := range s.workspaces {
		if existing, err := os.Stat(w.path); err == nil && os.SameFile(info, existing) {
			return w
		}
	}
	return nil
}

// sessionInfo is a session's wire form in workspace/list and
// session/created.
type sessionInfo struct {
	SessionID string       `json:"session_id"`
	Label     string       `json:"label"`
	Harness   string       `json:"harness"`
	CreatedAt time.Time    `json:"created_at"`
	Directory string       `json:"directory"`
	Worktree  string       `json:"worktree,omitempty"`
	Child     *store.Child `json:"child,omitempty"`
	Archived  bool         `json:"archived,omitempty"`
	Pinned    bool         `json:"pinned,omitempty"`
	State     string       `json:"state"`
}

// infoLocked returns the session's wire form. The caller holds s.mu.
func (sess *session) infoLocked() sessionInfo {
	return sessionInfo{sess.id, sess.labelLocked(), sess.harness, sess.createdAt, sess.directory, sess.worktree, sess.child, sess.archived, sess.pinned, sess.state}
}

func (s *Server) listWorkspaces(reply replyFunc, id json.RawMessage) {
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
			workspaces[i].Sessions[j] = sess.infoLocked()
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
	replyEmpty(reply, id)(s.removeWorkspaceLocked(input.WorkspaceID))
}

// removeWorkspaceLocked removes a workspace that has no sessions and tells
// every client. The caller holds s.mu, and replies under it, so the reply
// follows the broadcast.
func (s *Server) removeWorkspaceLocked(workspaceID string) error {
	if s.closed {
		return errShuttingDown
	}
	w := s.workspace(workspaceID)
	switch {
	case w == nil:
		return errWorkspaceNotFound
	case len(w.sessions) > 0:
		return &codedError{operationFailed, "workspace has sessions"}
	}
	if err := s.store.DeleteWorkspace(w.id); err != nil {
		return err
	}
	s.workspaces = slices.DeleteFunc(s.workspaces, func(other *workspace) bool { return other == w })
	s.broadcast("workspace/removed", struct {
		WorkspaceID string `json:"workspace_id"`
	}{w.id})
	return nil
}

// fileParams are the params of the file methods: a path relative to the
// directory of exactly one session or workspace.
type fileParams struct {
	SessionID   string `json:"session_id"`
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
}

// fileRoot returns the directory a file request reads beneath, its
// session's directory or its workspace's path, and the workspace's path.
func (s *Server) fileRoot(input fileParams) (dir, workspace string, err error) {
	if (input.SessionID == "") == (input.WorkspaceID == "") {
		return "", "", &codedError{jsonrpc.InvalidParams, "exactly one of session_id and workspace_id is required"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.SessionID != "" {
		sess := s.sessions[input.SessionID]
		if sess == nil {
			return "", "", errSessionNotFound
		}
		return sess.directory, sess.workspace, nil
	}
	w := s.workspace(input.WorkspaceID)
	if w == nil {
		return "", "", errWorkspaceNotFound
	}
	return w.path, w.path, nil
}

// listFolder lists a folder in a session's or workspace's directory. The
// directory is read outside s.mu, so a slow disk holds up no other request.
func (s *Server) listFolder(reply replyFunc, id, params json.RawMessage) {
	var input fileParams
	if err := json.Unmarshal(params, &input); err != nil {
		reply(invalidParams(id, "exactly one of session_id and workspace_id is required"))
		return
	}
	if input.Path != "" && !filepath.IsLocal(input.Path) {
		reply(invalidParams(id, "path must be relative to the directory"))
		return
	}
	dir, _, err := s.fileRoot(input)
	if err != nil {
		reply(failure(id, err))
		return
	}
	entries, err := listFolder(dir, input.Path)
	if err != nil {
		reply(failure(id, err))
		return
	}
	reply(jsonrpc.Success(id, struct {
		Entries []folderEntry `json:"entries"`
	}{entries}))
}

// readFile returns the text of a file in a session's or workspace's
// directory. The file is read outside s.mu, so a slow disk holds up no other
// request.
func (s *Server) readFile(reply replyFunc, id, params json.RawMessage) {
	var input fileParams
	if err := json.Unmarshal(params, &input); err != nil {
		reply(invalidParams(id, "exactly one of session_id and workspace_id is required"))
		return
	}
	if !filepath.IsLocal(input.Path) {
		reply(invalidParams(id, "path must be relative to the directory"))
		return
	}
	dir, _, err := s.fileRoot(input)
	if err != nil {
		reply(failure(id, err))
		return
	}
	text, err := readFile(dir, input.Path)
	if err != nil {
		reply(failure(id, err))
		return
	}
	reply(jsonrpc.Success(id, struct {
		Text string `json:"text"`
	}{text}))
}

// listChanges lists the changed files in a session's or workspace's
// directory. Git runs outside s.mu, so a large repository holds up no other
// request.
func (s *Server) listChanges(reply replyFunc, id, params json.RawMessage) {
	var input fileParams
	if err := json.Unmarshal(params, &input); err != nil {
		reply(invalidParams(id, "exactly one of session_id and workspace_id is required"))
		return
	}
	dir, workspace, err := s.fileRoot(input)
	if err != nil {
		reply(failure(id, err))
		return
	}
	result, err := listChanges(workspace, dir)
	if err != nil {
		reply(failure(id, err))
		return
	}
	reply(jsonrpc.Success(id, result))
}

// createSession creates a top-level session from a client's request.
func (s *Server) createSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID   string `json:"session_id"`
		WorkspaceID string `json:"workspace_id"`
		Harness     string `json:"harness"`
		Worktree    bool   `json:"worktree"`
		agent.Settings
	}
	if err := json.Unmarshal(params, &input); err != nil {
		reply(invalidParams(id, err.Error()))
		return
	}
	if input.WorkspaceID == "" || input.Harness == "" || input.PermissionMode == "" {
		reply(invalidParams(id, "workspace_id, harness, model, and permission_mode are required"))
		return
	}
	if input.SessionID != "" && !isID(input.SessionID) {
		reply(invalidParams(id, "session_id must be a base58 ID"))
		return
	}
	record := &store.Session{
		ID: cmp.Or(input.SessionID, newID()), Harness: input.Harness,
		Model: input.Model, Effort: input.Effort, PermissionMode: input.PermissionMode,
	}
	result, err := s.createTopLevel(input.WorkspaceID, record, input.Worktree, nil)
	if err != nil {
		reply(failure(id, err))
		return
	}
	reply(jsonrpc.Success(id, result))
}

// getSession returns a session's settings, history, queue, approvals, and
// current state. Every connection already receives the session's updates;
// those queued before the response are reflected in it, and those after it
// are not.
func (s *Server) getSession(reply replyFunc, id, params json.RawMessage) {
	s.onSessionID(reply, id, params, func(sess *session) {
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

// imageTypes are the image types a prompt takes.
var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// maxImageBytes is the most image data, decoded, one prompt takes.
const maxImageBytes = 10 << 20

// decodeImages decodes a prompt's base64 images, taking each one's type from
// its bytes.
func decodeImages(encoded []string) ([]agent.Image, error) {
	var images []agent.Image
	total := 0
	for _, e := range encoded {
		data, err := base64.StdEncoding.DecodeString(e)
		if err != nil {
			return nil, errors.New("images must be base64")
		}
		mimeType := http.DetectContentType(data)
		if !slices.Contains(imageTypes, mimeType) {
			return nil, errors.New("images must be PNG, JPEG, GIF, or WebP")
		}
		if total += len(data); total > maxImageBytes {
			return nil, errors.New("images are larger than 10 MiB")
		}
		images = append(images, agent.Image{MimeType: mimeType, Data: data})
	}
	return images, nil
}

func (s *Server) sendSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string   `json:"session_id"`
		MessageID string   `json:"message_id"`
		Text      string   `json:"text"`
		Images    []string `json:"images"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" ||
		(strings.TrimSpace(input.Text) == "" && len(input.Images) == 0) {
		reply(invalidParams(id, "session_id and text or images are required"))
		return
	}
	if input.MessageID != "" && !isID(input.MessageID) {
		reply(invalidParams(id, "message_id must be a base58 ID"))
		return
	}
	if len(input.Text) > 1<<20 {
		reply(invalidParams(id, "prompt is larger than 1 MiB"))
		return
	}
	images, err := decodeImages(input.Images)
	if err != nil {
		reply(invalidParams(id, err.Error()))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		if check := s.harnesses[sess.harness].CheckPrompt; check != nil {
			if err := check(input.Text); err != nil {
				reply(invalidParams(id, err.Error()))
				return
			}
		}
		messageID, err := sess.sendPrompt(input.MessageID, input.Text, images)
		if err != nil {
			reply(failure(id, err))
			return
		}
		reply(jsonrpc.Success(id, struct {
			MessageID string `json:"message_id"`
		}{messageID}))
	})
}

// imageSession returns an image attached to one of a session's prompts.
func (s *Server) imageSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string `json:"session_id"`
		ImageID   string `json:"image_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" || input.ImageID == "" {
		reply(invalidParams(id, "session_id and image_id are required"))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		image, err := s.store.Image(sess.id, input.ImageID)
		switch {
		case err != nil:
			reply(failure(id, err))
		case image == nil:
			reply(jsonrpc.Failure(id, notFound, "image not found"))
		default:
			reply(jsonrpc.Success(id, struct {
				MimeType string `json:"mime_type"`
				Data     []byte `json:"data"`
			}{image.MimeType, image.Data}))
		}
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
		replyEmpty(reply, id)(sess.approve(input.RequestID, input.Response))
	})
}

func (s *Server) interruptSession(reply replyFunc, id, params json.RawMessage) {
	s.onSessionID(reply, id, params, func(sess *session) {
		sess.interrupt(replyEmpty(reply, id))
	})
}

func (s *Server) configureSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string `json:"session_id"`
		agent.SettingsPatch
	}
	if err := json.Unmarshal(params, &input); err != nil {
		reply(invalidParams(id, err.Error()))
		return
	}
	if input.SessionID == "" || (input.Model == nil && input.Effort == nil && input.PermissionMode == nil) {
		reply(invalidParams(id, "session_id and at least one of model, effort, and permission_mode are required"))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		sess.configure(input.SettingsPatch, replyEmpty(reply, id))
	})
}

func (s *Server) destroySession(reply replyFunc, id, params json.RawMessage) {
	s.onSessionID(reply, id, params, func(sess *session) {
		sess.destroy(replyEmpty(reply, id))
	})
}

// archiveSession archives a session on its goroutine, which replies once
// every member has stopped.
func (s *Server) archiveSession(reply replyFunc, id, params json.RawMessage) {
	s.onSessionID(reply, id, params, func(sess *session) {
		replyEmpty(reply, id)(sess.archive(false))
	})
}

// unarchiveSession unarchives a session and replies under s.mu, so the reply
// follows the broadcasts it reflects and precedes any later one.
func (s *Server) unarchiveSession(reply replyFunc, id, params json.RawMessage) {
	sessionID, ok := sessionIDParam(params)
	if !ok {
		reply(invalidParams(id, "session_id is required"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replyEmpty(reply, id)(s.unarchiveLocked(sessionID))
}

// pinSession pins or unpins a top-level session and replies under s.mu, so the
// reply follows the broadcast it reflects and precedes any later one.
func (s *Server) pinSession(reply replyFunc, id, params json.RawMessage, pinned bool) {
	sessionID, ok := sessionIDParam(params)
	if !ok {
		reply(invalidParams(id, "session_id is required"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replyEmpty(reply, id)(s.setPinnedLocked(sessionID, pinned))
}

// setPinnedLocked stores and sets the session's pinned flag and tells every
// client. Unpinning gives the session the full auto-archive period. The
// caller holds s.mu.
func (s *Server) setPinnedLocked(sessionID string, pinned bool) error {
	sess := s.sessions[sessionID]
	switch {
	case sess == nil:
		return errSessionNotFound
	case sess.child != nil:
		return errors.New("only a top-level session can be pinned")
	case s.closed:
		return errShuttingDown
	}
	if err := s.store.SetPinned(sess.id, pinned); err != nil {
		log.Printf("session %s: store pinned: %v", sess.id, err)
		return err
	}
	sess.pinned = pinned
	method := "session/pinned"
	if !pinned {
		method = "session/unpinned"
		sess.activeAt = time.Now()
	}
	s.broadcast(method, struct {
		SessionID string `json:"session_id"`
	}{sess.id})
	return nil
}
