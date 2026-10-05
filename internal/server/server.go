// Package server is the Ox ACP agent: it creates, loads, lists, configures,
// prompts, closes, and deletes sessions for one ACP connection.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/acp"
	"ox/internal/agent"
	"ox/internal/catalog"
	"ox/internal/settings"
	"ox/internal/shellproc"
	"ox/internal/skills"
	"ox/internal/store"
	"ox/internal/sysprompt"
	"ox/internal/tools"
	"ox/internal/transcript"
)

// Server serves one ACP connection.
type Server struct {
	conn    *acp.Conn
	agent   *agent.Agent
	catalog catalog.Catalog
	// home holds the global settings file and the user skills directories.
	home string
	ops  *operations
	// prompts includes writing the response after the session becomes idle.
	prompts sync.WaitGroup

	mu sync.Mutex
	// defaults are the global settings. Each session applies its workspace
	// settings file to them when it becomes active.
	defaults settings.Settings
	// active holds the sessions created or loaded by this process. Only an
	// active session can be configured or prompted.
	active map[string]*activeSession
}

// activeSession is the process state of one session.
type activeSession struct {
	// selections are the latest ACP selections. A prompt copies the model and
	// effort when it starts, so changing them during the prompt applies to the
	// next turn; the turn reads the mode before each batch of tool calls.
	selections   settings.Settings
	systemPrompt string
	skills       []skills.Skill
	// processes are the background commands the session started. They
	// outlive prompts and repeated loads, and stop when the session is closed
	// or deleted or the connection shuts down.
	processes *shellproc.Processes
}

// New returns a server that writes to conn.
func New(conn *acp.Conn, a *agent.Agent, defaults settings.Settings, home string) *Server {
	return &Server{
		conn: conn, agent: a, catalog: a.Catalog, home: home, ops: newOperations(),
		defaults: defaults, active: map[string]*activeSession{},
	}
}

// HandleRequest answers one request. Requests that wait for other work
// respond from their own goroutine.
func (s *Server) HandleRequest(r *acp.Request) {
	var err *acp.Error
	switch r.Method {
	case acp.MethodInitialize:
		r.Respond(initializeResponse)
	case acp.MethodNewSession:
		err = s.newSession(r)
	case acp.MethodLoadSession:
		err = s.loadSession(r)
	case acp.MethodListSessions:
		err = s.listSessions(r)
	case acp.MethodCloseSession:
		err = s.closeSession(r)
	case acp.MethodDeleteSession:
		err = s.deleteSession(r)
	case acp.MethodSetConfigOption:
		err = s.setConfigOption(r)
	case acp.MethodPrompt:
		err = s.prompt(r)
	default:
		err = &acp.Error{Code: -32601, Message: "Method not found", Data: r.Method}
	}
	if err != nil {
		r.Fail(err)
	}
}

// HandleNotification handles cancellation, the one notification Ox reads.
func (s *Server) HandleNotification(method string, params json.RawMessage) {
	var cancel protocol.CancelNotification
	if method == acp.MethodCancel && json.Unmarshal(params, &cancel) == nil {
		s.ops.cancel(string(cancel.SessionId))
	}
}

// initializeResponse names the one protocol version Ox speaks; a client that
// needs another disconnects.
var initializeResponse = protocol.InitializeResponse{
	ProtocolVersion: acp.ProtocolVersion,
	AgentCapabilities: protocol.AgentCapabilities{
		LoadSession:        true,
		PromptCapabilities: protocol.PromptCapabilities{Image: true},
		SessionCapabilities: protocol.SessionCapabilities{
			List: &protocol.SessionListCapabilities{}, Close: &protocol.SessionCloseCapabilities{}, Delete: &protocol.SessionDeleteCapabilities{},
		},
	},
}

func internal(err error) *acp.Error {
	var acpErr *acp.Error
	if errors.As(err, &acpErr) {
		return acpErr
	}
	return acp.InternalError(err.Error())
}

func inactive(id string) *acp.Error {
	return acp.InvalidRequest(fmt.Sprintf("session %s is not active; create or load it first", id))
}

// storedSession reads a saved session, mapping a missing one to a not-found
// error.
func (s *Server) storedSession(id string) (*store.Session, *acp.Error) {
	session, err := s.agent.Store.Read(id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, acp.ResourceNotFound(id)
	case err != nil:
		return nil, internal(err)
	}
	return session, nil
}

// newActiveSession builds the process state a session gets when it becomes
// active. Its selections are set by the caller.
func (s *Server) newActiveSession(workspace string) (*activeSession, *acp.Error) {
	systemPrompt, err := sysprompt.ForWorkspace(workspace, tools.ShellProgram)
	if err != nil {
		return nil, internal(err)
	}
	return &activeSession{systemPrompt: systemPrompt, skills: s.loadSkills(workspace), processes: &shellproc.Processes{}}, nil
}

// unavailable says why a session operation could not start.
func (s *Server) unavailable() *acp.Error {
	if s.ops.isShuttingDown() {
		return acp.InvalidRequest("Ox is shutting down")
	}
	return acp.InvalidRequest("session has an operation in progress")
}

func (s *Server) notify(id string, update any) error {
	return s.conn.Notify(acp.MethodUpdate, acp.SessionNotification{SessionID: id, Update: update})
}

func (s *Server) session(id string) *activeSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[id]
}

// defaultsFor returns the settings of a new session in workspace.
func (s *Server) defaultsFor(workspace string) (settings.Settings, error) {
	s.mu.Lock()
	defaults := s.defaults
	s.mu.Unlock()
	return defaults.ForWorkspace(workspace, s.catalog)
}

// loadSkills loads the skill catalog for a session becoming active, writing
// each skipped definition to stderr.
func (s *Server) loadSkills(workspace string) []skills.Skill {
	loaded, skipped := skills.Load(s.home, workspace)
	for _, message := range skipped {
		fmt.Fprintln(os.Stderr, message)
	}
	return loaded
}

func (s *Server) newSession(r *acp.Request) *acp.Error {
	var params protocol.NewSessionRequest
	if err := r.Params(&params); err != nil {
		return err
	}
	session, acpErr := s.newActiveSession(params.Cwd)
	if acpErr != nil {
		return acpErr
	}
	selections, err := s.defaultsFor(params.Cwd)
	if err != nil {
		return internal(err)
	}
	session.selections = selections
	summary, err := s.agent.Store.Create(params.Cwd)
	if errors.Is(err, store.ErrRelativeWorkspace) {
		return acp.InvalidParams(err.Error())
	} else if err != nil {
		return internal(err)
	}
	s.mu.Lock()
	s.active[summary.ID] = session
	s.mu.Unlock()
	r.Respond(protocol.NewSessionResponse{SessionId: protocol.SessionId(summary.ID), ConfigOptions: configOptions(s.catalog, selections)})
	s.notify(summary.ID, availableCommands(session.skills))
	return nil
}

// loadSession replays the saved transcript as updates before responding. A
// repeated load keeps the system prompt, skill catalog, and background
// processes of the first load.
func (s *Server) loadSession(r *acp.Request) *acp.Error {
	var params protocol.LoadSessionRequest
	if err := r.Params(&params); err != nil {
		return err
	}
	id := string(params.SessionId)
	release, ok := s.ops.try(id)
	if !ok {
		return s.unavailable()
	}
	defer release()
	saved, acpErr := s.storedSession(id)
	if acpErr != nil {
		return acpErr
	}
	if saved.Summary.Workspace != params.Cwd {
		return acp.InvalidParams(fmt.Sprintf("session %s belongs to workspace %s, not %s", id, saved.Summary.Workspace, params.Cwd))
	}
	selections, err := s.defaultsFor(saved.Summary.Workspace)
	if err != nil {
		return internal(err)
	}
	if start := transcript.LatestTurnStart(saved.Transcript); start != nil {
		selections = settings.Settings{Model: start.Model, Effort: start.Effort, Mode: start.Mode}
	}
	session := s.session(id)
	if session == nil {
		session, acpErr = s.newActiveSession(saved.Summary.Workspace)
		if acpErr != nil {
			return acpErr
		}
	}
	model, err := agent.ModelSelection(s.catalog, selections.Model, selections.Effort)
	if err != nil {
		return internal(err)
	}
	s.mu.Lock()
	session.selections = selections
	s.active[id] = session
	s.mu.Unlock()
	send := func(update any) error { return s.notify(id, update) }
	if err := replay(saved.Transcript, model.ContextLimit, send); err != nil {
		return internal(err)
	}
	r.Respond(protocol.LoadSessionResponse{ConfigOptions: configOptions(s.catalog, selections)})
	s.notify(id, availableCommands(session.skills))
	return nil
}

// listSessions returns every matching session in one page.
func (s *Server) listSessions(r *acp.Request) *acp.Error {
	var params protocol.ListSessionsRequest
	if err := r.Params(&params); err != nil {
		return err
	}
	cwd := ""
	if params.Cwd != nil {
		cwd = *params.Cwd
	}
	summaries, err := s.agent.Store.List(cwd)
	if err != nil {
		return internal(err)
	}
	response := protocol.ListSessionsResponse{Sessions: []protocol.SessionInfo{}}
	for _, summary := range summaries {
		response.Sessions = append(response.Sessions, protocol.SessionInfo{
			SessionId: protocol.SessionId(summary.ID), Cwd: summary.Workspace, Title: optionalText(summary.Title), UpdatedAt: optionalText(summary.UpdatedAt),
		})
	}
	r.Respond(response)
	return nil
}

// closeSession cancels the session's prompt, waits for it, and stops its
// background processes, leaving the saved session loadable.
func (s *Server) closeSession(r *acp.Request) *acp.Error {
	var params protocol.CloseSessionRequest
	if err := r.Params(&params); err != nil {
		return err
	}
	id := string(params.SessionId)
	wait, release, ok := s.ops.beginClose(id)
	if !ok {
		return s.unavailable()
	}
	go func() {
		defer release()
		wait()
		session := s.session(id)
		if session == nil {
			r.Fail(inactive(id))
			return
		}
		session.processes.Shutdown()
		s.deactivate(id)
		r.Respond(struct{}{})
	}()
	return nil
}

// deleteSession stops the session's background processes only after the
// database deletion succeeds. The session stays active until they are cleaned
// up, so connection shutdown can still reach them.
func (s *Server) deleteSession(r *acp.Request) *acp.Error {
	var params protocol.CloseSessionRequest
	if err := r.Params(&params); err != nil {
		return err
	}
	id := string(params.SessionId)
	release, ok := s.ops.try(id)
	if !ok {
		return s.unavailable()
	}
	go func() {
		defer release()
		if err := s.agent.Store.Delete(id); err != nil {
			r.Fail(internal(err))
			return
		}
		if session := s.session(id); session != nil {
			session.processes.Shutdown()
		}
		s.deactivate(id)
		r.Respond(struct{}{})
	}()
	return nil
}

func (s *Server) deactivate(id string) {
	s.mu.Lock()
	delete(s.active, id)
	s.mu.Unlock()
}

// setConfigOption validates and saves one selection. Saving writes the
// workspace settings file when it exists, otherwise the global file, whose
// values then become the defaults for new sessions.
func (s *Server) setConfigOption(r *acp.Request) *acp.Error {
	var params protocol.SetSessionConfigOptionValueId
	if err := r.Params(&params); err != nil {
		return err
	}
	id := string(params.SessionId)
	value := string(params.Value)
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.active[id]
	if session == nil {
		return inactive(id)
	}
	selected := session.selections
	notAChoice := acp.InvalidParams(fmt.Sprintf("%s is not a choice of configuration option %s", value, params.ConfigId))
	switch params.ConfigId {
	case "model":
		model := s.catalog.Lookup(value)
		if model == nil {
			return notAChoice
		}
		selected.Model = model.QualifiedID()
		if !model.Supports(selected.Effort) {
			selected.Effort = catalog.EffortDefault
		}
	case "effort":
		effort, ok := catalog.ParseEffort(value)
		if !ok || !s.catalog.Lookup(selected.Model).Supports(effort) {
			return notAChoice
		}
		selected.Effort = effort
	case "mode":
		mode, ok := transcript.ParseMode(value)
		if !ok {
			return notAChoice
		}
		selected.Mode = mode
	default:
		return acp.InvalidParams("no configuration option " + string(params.ConfigId))
	}
	saved, acpErr := s.storedSession(id)
	if acpErr != nil {
		return acpErr
	}
	global, err := settings.Save(s.home, saved.Summary.Workspace, selected)
	if err != nil {
		return internal(err)
	}
	if global {
		s.defaults = selected
	}
	session.selections = selected
	r.Respond(protocol.SetSessionConfigOptionResponse{ConfigOptions: configOptions(s.catalog, selected)})
	return nil
}

// prompt rejects a prompt that cannot start, or starts a turn that responds
// when it ends.
func (s *Server) prompt(r *acp.Request) *acp.Error {
	var params protocol.PromptRequest
	if err := r.Params(&params); err != nil {
		return err
	}
	id := string(params.SessionId)
	message, acpErr := promptMessage(params.Prompt)
	if acpErr != nil {
		return acpErr
	}
	ctx, release, ok := s.ops.tryPrompt(id)
	if !ok {
		return s.unavailable()
	}
	session := s.session(id)
	if session == nil {
		release()
		return inactive(id)
	}
	s.mu.Lock()
	selections := session.selections
	s.mu.Unlock()
	client := &acpClient{server: s, sessionID: id, session: session}
	turn, err := s.agent.Start(ctx, agent.Input{
		SessionID: id, Input: dispatch(message, session.skills),
		Model: selections.Model, Effort: selections.Effort,
		SystemPrompt: session.systemPrompt, Processes: session.processes,
	}, client)
	if err != nil {
		release()
		switch {
		case errors.Is(err, agent.ErrImagesUnsupported):
			return acp.InvalidParams(err.Error())
		case errors.Is(err, store.ErrNotFound):
			return acp.ResourceNotFound(id)
		}
		return internal(err)
	}
	client.workspace = turn.Workspace()
	s.prompts.Add(1)
	go func() {
		defer s.prompts.Done()
		result, err := turn.Run(ctx)
		// The client may send its next prompt as soon as it reads the response.
		release()
		if err != nil {
			r.Fail(internal(err))
			return
		}
		r.Respond(protocol.PromptResponse{StopReason: []protocol.StopReason{"end_turn", "cancelled", "max_tokens", "refusal"}[result.Stop]})
	}()
	return nil
}

// Shutdown rejects new operations, cancels active prompts, waits for active
// operations to respond, and stops every background process.
func (s *Server) Shutdown() {
	s.ops.beginShutdown()
	s.mu.Lock()
	var all []*shellproc.Processes
	for _, session := range s.active {
		all = append(all, session.processes)
	}
	s.mu.Unlock()
	for _, processes := range all {
		processes.BeginShutdown()
	}
	s.ops.shutdown()
	s.prompts.Wait()
	// Sessions activated while operations finished are included too.
	s.mu.Lock()
	all = all[:0]
	for _, session := range s.active {
		all = append(all, session.processes)
	}
	s.mu.Unlock()
	var wait sync.WaitGroup
	for _, processes := range all {
		wait.Go(processes.Shutdown)
	}
	wait.Wait()
}

// acpClient sends a turn's events as session updates and asks the ACP client
// for Ask mode permission.
type acpClient struct {
	server    *Server
	sessionID string
	// session stays active while its prompt runs, since close and delete
	// wait for the prompt.
	session   *activeSession
	workspace string
}

func (c *acpClient) Send(event agent.Event) error {
	return c.server.notify(c.sessionID, update(event))
}

func (c *acpClient) Mode() transcript.Mode {
	c.server.mu.Lock()
	defer c.server.mu.Unlock()
	return c.session.selections.Mode
}

func (c *acpClient) Approve(ctx context.Context, call transcript.ToolCall, permission tools.Permission) (bool, error) {
	var response protocol.RequestPermissionResponse
	request := permissionRequest(c.sessionID, c.workspace, call, permission)
	if err := c.server.conn.Call(ctx, acp.MethodRequestPermission, request, &response); err != nil {
		return false, err
	}
	if response.Outcome.Cancelled != nil {
		return false, agent.ErrCancelled
	}
	if selected := response.Outcome.Selected; selected != nil {
		switch selected.OptionId {
		case "approve":
			return true, nil
		case "deny":
			return false, nil
		}
		return false, acp.InternalError("Unknown shell permission option: " + string(selected.OptionId))
	}
	return false, acp.InternalError("Unsupported shell permission outcome")
}
