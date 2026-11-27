package server

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	Harnesses map[string]config.Harness `json:"harnesses"`
	Defaults  config.Defaults           `json:"defaults"`
}

func (s *Server) catalog() catalog {
	return catalog{Harnesses: s.config.Harnesses, Defaults: s.config.Defaults}
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

func (s *Server) listWorkspaces(reply replyFunc, id json.RawMessage) {
	type sessionInfo struct {
		SessionID string       `json:"session_id"`
		Label     string       `json:"label"`
		Harness   string       `json:"harness"`
		CreatedAt time.Time    `json:"created_at"`
		Directory string       `json:"directory"`
		Worktree  string       `json:"worktree,omitempty"`
		Child     *store.Child `json:"child,omitempty"`
		Archived  bool         `json:"archived,omitempty"`
		State     string       `json:"state"`
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
			workspaces[i].Sessions[j] = sessionInfo{sess.id, sess.labelLocked(), sess.harness, sess.createdAt, sess.directory, sess.worktree, sess.child, sess.archived, sess.state}
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

// fileParams are the params of the file methods: a path relative to the
// directory of exactly one session or workspace.
type fileParams struct {
	SessionID   string `json:"session_id"`
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
}

// fileRoot returns the directory a file request reads beneath, its
// session's directory or its workspace's path, and the workspace's path. On
// failure it returns the error response instead.
func (s *Server) fileRoot(id json.RawMessage, input fileParams) (dir, workspace string, failed *jsonrpc.Response) {
	if (input.SessionID == "") == (input.WorkspaceID == "") {
		return "", "", invalidParams(id, "exactly one of session_id and workspace_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.SessionID != "" {
		sess := s.sessions[input.SessionID]
		if sess == nil {
			return "", "", sessionNotFound(id)
		}
		return sess.directory, sess.workspace, nil
	}
	w := s.workspace(input.WorkspaceID)
	if w == nil {
		return "", "", jsonrpc.Failure(id, notFound, "workspace not found")
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
	dir, _, failed := s.fileRoot(id, input)
	if failed != nil {
		reply(failed)
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
	dir, _, failed := s.fileRoot(id, input)
	if failed != nil {
		reply(failed)
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
	dir, workspace, failed := s.fileRoot(id, input)
	if failed != nil {
		reply(failed)
		return
	}
	result, err := listChanges(workspace, dir)
	if err != nil {
		reply(failure(id, err))
		return
	}
	reply(jsonrpc.Success(id, result))
}

// created is session/create's result.
type created struct {
	SessionID string `json:"session_id"`
	Existing  bool   `json:"existing,omitempty"`
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
	if err := json.Unmarshal(params, &input); err != nil || input.WorkspaceID == "" || input.Harness == "" {
		reply(invalidParams(id, "workspace_id, harness, model, and permission_mode are required"))
		return
	}
	if input.SessionID != "" && !isID(input.SessionID) {
		reply(invalidParams(id, "session_id must be a base58 ID"))
		return
	}
	record := &store.Session{
		ID: cmp.Or(input.SessionID, newID()), Harness: input.Harness,
		Model: input.Model, Effort: input.Effort, PermissionMode: input.PermissionMode, CreatedAt: time.Now(),
	}
	result, err := s.createTopLevel(input.WorkspaceID, record, input.Worktree)
	if err != nil {
		reply(failure(id, err))
		return
	}
	reply(jsonrpc.Success(id, result))
}

// createTopLevel creates the top-level session record describes in the
// workspace, in a single s.mu section, or, with a worktree, in three steps: it
// admits the creation under s.mu, makes the worktree without it, and publishes
// the session under s.mu, deciding then whether the daemon, the workspace, and
// another session with the ID allow it. A creation that does not publish
// discards its worktree before it returns, and Close waits for every admitted
// creation to return. It fills in record's directory and worktree.
func (s *Server) createTopLevel(workspaceID string, record *store.Session, worktree bool) (created, error) {
	s.mu.Lock()
	w, existing, err := s.admitSessionLocked(workspaceID, record)
	switch {
	case err != nil:
		s.mu.Unlock()
		return created{}, err
	case existing:
		s.mu.Unlock()
		return created{SessionID: record.ID, Existing: true}, nil
	case !worktree:
		err := s.addSession(w, record)
		s.mu.Unlock()
		return created{SessionID: record.ID}, err
	}
	s.creations.Add(1)
	s.mu.Unlock()
	defer s.creations.Done()

	log.Printf("session %s: creating worktree in %s", record.ID, s.worktrees)
	root, directory, err := addWorktree(w.path, s.worktrees)
	if err != nil {
		return created{}, err
	}
	record.Directory, record.Worktree = directory, root

	var result created
	s.mu.Lock()
	switch {
	case s.closed:
		err = errShuttingDown
	case !slices.Contains(s.workspaces, w):
		err = errWorkspaceNotFound
	default:
		if existing, err = s.existingLocked(w, record.ID); existing {
			result = created{SessionID: record.ID, Existing: true}
		} else if err == nil {
			if err = s.addSession(w, record); err == nil {
				s.mu.Unlock()
				return created{SessionID: record.ID}, nil
			}
		}
	}
	s.mu.Unlock()
	if discardErr := discardWorktree(w.path, root); discardErr != nil {
		if err == nil {
			log.Printf("session %s: %v", record.ID, discardErr)
		} else {
			err = errors.Join(err, discardErr)
		}
	}
	return result, err
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
	if err := cmp.Or(harness.CheckModel(record.Model, record.Effort), harness.CheckPermissionMode(record.PermissionMode)); err != nil {
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

// addSession stores a session in w, publishes it, and starts its goroutine.
// The caller holds s.mu.
func (s *Server) addSession(w *workspace, record *store.Session) error {
	if err := s.store.AddSession(w.id, record); err != nil {
		return err
	}
	sess := s.newSession(record, w.path, "")
	s.publishLocked(w, sess)
	go sess.run()
	return nil
}

// publishLocked adds sess to w and to the sessions requests reach, and tells
// every client. The caller holds s.mu and still owns sess, whose goroutine
// has not started.
func (s *Server) publishLocked(w *workspace, sess *session) {
	sess.state = sess.snapshot().State
	w.sessions = append(w.sessions, sess)
	s.sessions[sess.id] = sess
	s.broadcast("session/created", struct {
		SessionID   string       `json:"session_id"`
		WorkspaceID string       `json:"workspace_id"`
		Label       string       `json:"label"`
		Harness     string       `json:"harness"`
		CreatedAt   time.Time    `json:"created_at"`
		Directory   string       `json:"directory"`
		Worktree    string       `json:"worktree,omitempty"`
		Child       *store.Child `json:"child,omitempty"`
		State       string       `json:"state"`
	}{sess.id, w.id, sess.labelLocked(), sess.harness, sess.createdAt, sess.directory, sess.worktree, sess.child, sess.state})
}

// getSession returns a session's settings, history, queue, approvals, and
// current state. Every connection already receives the session's updates;
// those queued before the response are reflected in it, and those after it
// are not.
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
		if sess.agent == nil {
			reply(jsonrpc.Success(id, struct{}{}))
			return
		}
		sess.agent.Interrupt(func(err error) {
			if err != nil {
				reply(failure(id, err))
				return
			}
			reply(jsonrpc.Success(id, struct{}{}))
		})
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
		harness := s.config.Harnesses[sess.harness]
		// A model alone takes the session's current effort when the model
		// offers it, so a page need not know the effort.
		if input.Model != nil && input.Effort == nil {
			if effort := harness.Effort(*input.Model, sess.settings.Effort); effort != sess.settings.Effort {
				input.Effort = &effort
			}
		}
		settings := input.Apply(sess.settings)
		// Only the settings the request changes are checked, so a stored
		// setting the configuration no longer offers can stay.
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
			if sess.isArchived() {
				reply(failure(id, errArchived))
				return
			}
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
		sess.destroy(func(err error) {
			if err != nil {
				reply(failure(id, err))
				return
			}
			reply(jsonrpc.Success(id, struct{}{}))
		})
	})
}

// archiveSession archives a session on its goroutine, which replies once
// every member has stopped.
func (s *Server) archiveSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" {
		reply(invalidParams(id, "session_id is required"))
		return
	}
	s.onSession(reply, id, input.SessionID, func(sess *session) {
		if err := sess.archive(); err != nil {
			reply(failure(id, err))
			return
		}
		reply(jsonrpc.Success(id, struct{}{}))
	})
}

// unarchiveSession unarchives a session and replies under s.mu, so the reply
// follows the broadcasts it reflects and precedes any later one.
func (s *Server) unarchiveSession(reply replyFunc, id, params json.RawMessage) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.SessionID == "" {
		reply(invalidParams(id, "session_id is required"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.unarchiveLocked(input.SessionID); err != nil {
		reply(failure(id, err))
		return
	}
	reply(jsonrpc.Success(id, struct{}{}))
}
