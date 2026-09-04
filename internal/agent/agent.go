package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
	"github.com/kkestell/ox/internal/workspace"
)

const openRouterAuthMethodID = "openrouter"

type Model interface {
	Stream(context.Context, openrouter.Request, func(openrouter.Delta)) (*openrouter.Completion, error)
	ModelInfo(context.Context, string) (*openrouter.Model, error)
}

type Config struct {
	Name        string
	Version     string
	Logger      *slog.Logger
	Credentials *credentials.Store
	// ModelOverride wins over both settings layers. Empty means the settings
	// files decide.
	ModelOverride string
	// SettingsPath is the global settings layer. Empty skips it.
	SettingsPath string
	// SessionDir is the directory containing durable JSONL session logs. Empty
	// uses an isolated temporary store, which is useful for embedded clients and
	// tests. The production runtime always supplies the XDG data path.
	SessionDir string
	Client     Model
	Tools      []Tool
}

type Agent struct {
	name                 string
	version              string
	logger               *slog.Logger
	credentials          *credentials.Store
	modelOverride        string
	settingsPath         string
	client               Model
	primaryTools         toolSet
	subagentTools        toolSet
	store                *fileStore
	clientCapabilitiesMu sync.RWMutex
	clientFS             acp.FileSystemCapabilities
	clientTerminal       bool
	authMu               sync.Mutex
	rejectedKey          string
	rejectionText        string
	sessionsMu           sync.RWMutex
	sessions             map[string]*session
}

func New(config Config) (*Agent, error) {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	tools := append([]Tool(nil), config.Tools...)
	for _, tool := range tools {
		if tool.Name == "" {
			return nil, errors.New("tool name is empty")
		}
		if (tool.Suggest == nil) != (tool.Covered == nil) {
			return nil, fmt.Errorf("tool %q must set both Suggest and Covered", tool.Name)
		}
		if tool.Delegates && tool.Label == nil {
			return nil, fmt.Errorf("delegating tool %q must set Label", tool.Name)
		}
		if !tool.Delegates && tool.Label != nil {
			return nil, fmt.Errorf("non-delegating tool %q cannot set Label", tool.Name)
		}
	}
	primaryTools, err := newToolSet(tools)
	if err != nil {
		return nil, err
	}
	subagentTools, err := newToolSet(slices.DeleteFunc(
		append([]Tool(nil), tools...),
		func(tool Tool) bool { return tool.Delegates },
	))
	if err != nil {
		return nil, err
	}
	if len(primaryTools.tools) > 0 && len(subagentTools.tools) == 0 {
		return nil, errors.New("subagent tool set is empty")
	}
	store, err := newFileStore(config.SessionDir)
	if err != nil {
		return nil, err
	}
	return &Agent{
		name:          config.Name,
		version:       config.Version,
		logger:        config.Logger,
		credentials:   config.Credentials,
		modelOverride: config.ModelOverride,
		settingsPath:  config.SettingsPath,
		client:        config.Client,
		primaryTools:  primaryTools,
		subagentTools: subagentTools,
		store:         store,
		sessions:      make(map[string]*session),
	}, nil
}

func newToolSet(tools []Tool) (toolSet, error) {
	result := toolSet{
		tools:      append([]Tool(nil), tools...),
		byName:     make(map[string]int, len(tools)),
		modelTools: make([]openrouter.Tool, len(tools)),
	}
	for index, tool := range result.tools {
		if _, exists := result.byName[tool.Name]; exists {
			return toolSet{}, fmt.Errorf("duplicate tool name %q", tool.Name)
		}
		result.byName[tool.Name] = index
		result.modelTools[index] = openrouter.Tool{
			Type: "function",
			Function: openrouter.ToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  append([]byte(nil), tool.InputSchema...),
			},
		}
	}
	return result, nil
}

func (a *Agent) Methods() handler.Map {
	return handler.Map{
		"initialize":       handler.New(a.Initialize),
		"authenticate":     handler.New(a.Authenticate),
		"logout":           handler.New(a.Logout),
		"session/new":      handler.New(a.NewSession),
		"session/list":     handler.New(a.ListSessions),
		"session/load":     handler.New(a.LoadSession),
		"session/resume":   handler.New(a.ResumeSession),
		"session/close":    handler.New(a.CloseSession),
		"session/delete":   handler.New(a.DeleteSession),
		"session/prompt":   handler.New(a.Prompt),
		"session/cancel":   handler.New(a.Cancel),
		"$/cancel_request": handler.New(a.CancelRequest),
	}
}

func (a *Agent) Initialize(
	_ context.Context,
	request acp.InitializeRequest,
) (acp.InitializeResponse, error) {
	if err := request.Validate(); err != nil {
		return acp.InitializeResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	a.logger.Info("initializing client", "requested_protocol_version", request.ProtocolVersion)

	a.clientCapabilitiesMu.Lock()
	a.clientFS = acp.FileSystemCapabilities{}
	a.clientTerminal = false
	if request.ClientCapabilities != nil && request.ClientCapabilities.FS != nil {
		a.clientFS = *request.ClientCapabilities.FS
	}
	if request.ClientCapabilities != nil {
		a.clientTerminal = request.ClientCapabilities.Terminal
	}
	a.clientCapabilitiesMu.Unlock()

	authMethods := []acp.AuthMethod{{
		ID:          openRouterAuthMethodID,
		Name:        "OpenRouter API key",
		Description: "Use the configured OpenRouter API key.",
	}}
	if request.ClientCapabilities != nil && request.ClientCapabilities.Auth != nil &&
		request.ClientCapabilities.Auth.Terminal {
		authMethods = append(authMethods, acp.AuthMethod{
			ID:          "openrouter-terminal",
			Type:        "terminal",
			Name:        "OpenRouter login",
			Description: "Store an OpenRouter API key with ox login.",
			Args:        []string{"login"},
		})
	}
	response := acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion,
		AgentCapabilities: &acp.AgentCapabilities{
			PromptCapabilities: &acp.PromptCapabilities{
				Image:           true,
				Audio:           true,
				EmbeddedContext: true,
			},
			LoadSession: true,
			SessionCapabilities: &acp.SessionCapabilities{
				List:   &acp.SessionListCapabilities{},
				Delete: &acp.SessionDeleteCapabilities{},
				Resume: &acp.SessionResumeCapabilities{},
				Close:  &acp.SessionCloseCapabilities{},
			},
			Auth: &acp.AgentAuthCapabilities{
				Logout: &acp.LogoutCapabilities{},
			},
		},
		AuthMethods: authMethods,
		AgentInfo: &acp.Implementation{
			Name:    a.name,
			Version: a.version,
		},
	}
	if request.ProtocolVersion == acp.ProtocolVersion {
		response.ProtocolVersion = request.ProtocolVersion
	}

	a.logger.Info("client initialized", "protocol_version", response.ProtocolVersion)
	return response, nil
}

func (a *Agent) Authenticate(
	ctx context.Context,
	request acp.AuthenticateRequest,
) (acp.AuthenticateResponse, error) {
	if err := request.Validate(); err != nil {
		return acp.AuthenticateResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	if request.MethodID != openRouterAuthMethodID {
		if request.MethodID == "openrouter-terminal" {
			return acp.AuthenticateResponse{}, jrpc2.Errorf(
				jrpc2.InvalidParams,
				"openrouter-terminal authentication must be completed in a terminal",
			)
		}
		return acp.AuthenticateResponse{}, jrpc2.Errorf(
			jrpc2.InvalidParams,
			"unknown authentication method",
		)
	}
	if a.credentials == nil {
		return acp.AuthenticateResponse{}, authRequiredError()
	}

	a.credentials.Refresh()
	if a.credentials.Key() == "" {
		return acp.AuthenticateResponse{}, authRequiredError()
	}
	if verifier, ok := a.client.(interface {
		VerifyCredential(context.Context, string) error
	}); ok {
		if err := verifier.VerifyCredential(ctx, a.credentials.Key()); err != nil {
			if errors.Is(err, openrouter.ErrCredentialRejected) {
				a.rejectCredential(a.credentials.Key(), err.Error())
				return acp.AuthenticateResponse{}, jrpc2.Errorf(
					acp.ErrCodeAuthRequired,
					"OpenRouter rejected the configured credential: %v",
					err,
				)
			}
			return acp.AuthenticateResponse{}, jrpc2.Errorf(
				jrpc2.InternalError,
				"verify OpenRouter credential: %v",
				err,
			)
		}
	}
	a.acceptCredential(a.credentials.Key())
	a.logger.Info("client authenticated", "source", a.credentials.Source())
	return acp.AuthenticateResponse{}, nil
}

func (a *Agent) Logout(
	_ context.Context,
	_ acp.LogoutRequest,
) (acp.LogoutResponse, error) {
	if a.credentials != nil {
		if err := a.credentials.Clear(); err != nil {
			return acp.LogoutResponse{}, jrpc2.Errorf(
				jrpc2.InternalError,
				"log out of OpenRouter: %v",
				err,
			)
		}
	}
	a.acceptCredential("")
	a.logger.Info("client logged out", "source", a.credentialSource())
	return acp.LogoutResponse{}, nil
}

func (a *Agent) NewSession(
	ctx context.Context,
	request acp.NewSessionRequest,
) (acp.NewSessionResponse, error) {
	if err := request.Validate(); err != nil {
		return acp.NewSessionResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	cwd, err := a.validateActivation(
		request.CWD,
		request.AdditionalDirectories,
		request.MCPServers,
		true,
	)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}

	configuration, err := a.resolveConfiguration(ctx, cwd)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}

	id, err := randomID()
	if err != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("generate session ID: %w", err)
	}
	record, err := newRecord(1, recordSessionCreated, sessionCreated{
		SessionID:     id,
		CWD:           cwd,
		Configuration: configuration,
	})
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	state, err := foldRecords([]sessionRecord{record})
	if err != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("fold new session: %w", err)
	}
	log, err := a.store.create(id, record)
	if err != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("persist new session: %w", err)
	}
	value := &session{
		id:    id,
		state: state,
		log:   log,
	}
	a.sessionsMu.Lock()
	a.sessions[id] = value
	a.sessionsMu.Unlock()

	a.logger.Info(
		"session created",
		"session_id", id,
		"context_window", configuration.ContextWindow,
		"settings", configuration.Settings,
		"system_prompt_length", len(configuration.SystemPrompt),
		"system_prompt_digest", promptDigest(configuration.SystemPrompt),
	)
	return acp.NewSessionResponse{SessionID: id}, nil
}

func (a *Agent) LoadSession(
	ctx context.Context,
	request acp.LoadSessionRequest,
) (acp.LoadSessionResponse, error) {
	value, err := a.activateSession(
		ctx,
		request.SessionID,
		request.CWD,
		request.AdditionalDirectories,
		request.MCPServers,
		true,
	)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	updates, err := a.replay(value.state)
	if err != nil {
		_ = a.closeActive(value.id)
		return acp.LoadSessionResponse{}, fmt.Errorf("project session replay: %w", err)
	}
	server := jrpc2.ServerFromContext(ctx)
	for _, update := range updates {
		if err := server.Notify(ctx, "session/update", acp.SessionNotification{
			SessionID: value.id,
			Update:    update,
		}); err != nil {
			_ = a.closeActive(value.id)
			return acp.LoadSessionResponse{}, fmt.Errorf("replay session update: %w", err)
		}
	}
	a.logger.Info("session loaded", "session_id", value.id, "updates", len(updates))
	return acp.LoadSessionResponse{}, nil
}

func (a *Agent) ResumeSession(
	ctx context.Context,
	request acp.ResumeSessionRequest,
) (acp.ResumeSessionResponse, error) {
	_, err := a.activateSession(
		ctx,
		request.SessionID,
		request.CWD,
		request.AdditionalDirectories,
		request.MCPServers,
		false,
	)
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	a.logger.Info("session resumed", "session_id", request.SessionID)
	return acp.ResumeSessionResponse{}, nil
}

func (a *Agent) activateSession(
	ctx context.Context,
	id string,
	cwd string,
	additionalDirectories []string,
	mcpServers []json.RawMessage,
	requireMCP bool,
) (*session, error) {
	if !validSessionID(id) {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "invalid session ID")
	}
	canonicalCWD, err := a.validateActivation(
		cwd,
		additionalDirectories,
		mcpServers,
		requireMCP,
	)
	if err != nil {
		return nil, err
	}
	if a.findSession(id) != nil {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "session is already active")
	}
	log, records, repaired, err := a.store.open(id)
	if err != nil {
		return nil, fmt.Errorf("activate session: %w", err)
	}
	activated := false
	value := &session{id: id, log: log}
	defer func() {
		if !activated {
			log.close()
		}
	}()
	value.state, err = foldRecords(records)
	if err != nil {
		return nil, fmt.Errorf("fold session: %w", err)
	}
	if value.state.id != id {
		return nil, errors.New("session ID does not match its filename")
	}
	if value.state.cwd != canonicalCWD {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "session cwd does not match")
	}
	if value.state.openTurn != "" {
		outcomeID, idErr := randomID()
		if idErr != nil {
			return nil, idErr
		}
		if err := a.commit(value, recordTurnFinished, turnFinishedRecord{
			TurnID:    value.state.openTurn,
			Kind:      "interrupted",
			MessageID: outcomeID,
		}); err != nil {
			return nil, fmt.Errorf("recover interrupted session: %w", err)
		}
	}
	configuration, err := a.resolveConfiguration(ctx, canonicalCWD)
	if err != nil {
		return nil, err
	}
	if !sameRequestConfiguration(value.state.configuration, configuration) {
		if err := a.commit(value, recordConfigChanged, configurationChanged{
			Configuration: configuration,
		}); err != nil {
			return nil, fmt.Errorf("persist session configuration: %w", err)
		}
	}
	a.sessionsMu.Lock()
	if a.sessions[id] != nil {
		a.sessionsMu.Unlock()
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "session is already active")
	}
	a.sessions[id] = value
	a.sessionsMu.Unlock()
	activated = true
	if repaired {
		a.logger.Info("session torn tail repaired", "session_id", id)
	}
	a.logger.Info(
		"session activated",
		"session_id", id,
		"records", len(value.state.records),
		"system_prompt_length", len(value.state.configuration.SystemPrompt),
		"system_prompt_digest", promptDigest(value.state.configuration.SystemPrompt),
	)
	return value, nil
}

func (a *Agent) ListSessions(
	_ context.Context,
	request acp.ListSessionsRequest,
) (acp.ListSessionsResponse, error) {
	if request.CWD != "" {
		if !filepath.IsAbs(request.CWD) {
			return acp.ListSessionsResponse{}, jrpc2.Errorf(
				jrpc2.InvalidParams,
				"session cwd must be absolute",
			)
		}
		canonical, err := workspace.Canonical(request.CWD)
		if err != nil {
			return acp.ListSessionsResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
		}
		request.CWD = canonical
	}
	var cursor listCursor
	if request.Cursor != "" {
		var err error
		cursor, err = decodeCursor(request.Cursor)
		if err != nil || cursor.CWD != request.CWD {
			return acp.ListSessionsResponse{}, jrpc2.Errorf(
				jrpc2.InvalidParams,
				"cursor does not match the session filter",
			)
		}
	}
	states, err := a.store.list()
	if err != nil {
		return acp.ListSessionsResponse{}, fmt.Errorf("list sessions: %w", err)
	}
	filtered := make([]durableState, 0, len(states))
	for _, state := range states {
		if request.CWD == "" || state.cwd == request.CWD {
			filtered = append(filtered, state)
		}
	}
	start := 0
	if request.Cursor != "" {
		found := false
		for index := range filtered {
			if filtered[index].id == cursor.SessionID &&
				filtered[index].updatedAt.Format(time.RFC3339Nano) == cursor.UpdatedAt {
				start = index + 1
				found = true
				break
			}
		}
		if !found {
			return acp.ListSessionsResponse{}, jrpc2.Errorf(
				jrpc2.InvalidParams,
				"session cursor is stale",
			)
		}
	}
	const pageSize = 50
	end := min(start+pageSize, len(filtered))
	response := acp.ListSessionsResponse{
		Sessions: make([]acp.SessionInfo, 0, end-start),
	}
	for _, state := range filtered[start:end] {
		response.Sessions = append(response.Sessions, acp.SessionInfo{
			SessionID: state.id,
			CWD:       state.cwd,
			Title:     state.title,
			UpdatedAt: state.updatedAt.Format(time.RFC3339Nano),
		})
	}
	if end < len(filtered) {
		last := filtered[end-1]
		response.NextCursor = encodeCursor(listCursor{
			CWD:       request.CWD,
			UpdatedAt: last.updatedAt.Format(time.RFC3339Nano),
			SessionID: last.id,
		})
	}
	a.logger.Info(
		"sessions listed",
		"cwd", request.CWD,
		"count", len(response.Sessions),
		"has_next", response.NextCursor != "",
	)
	return response, nil
}

func (a *Agent) CloseSession(
	_ context.Context,
	request acp.CloseSessionRequest,
) (acp.CloseSessionResponse, error) {
	if !validSessionID(request.SessionID) {
		return acp.CloseSessionResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "invalid session ID")
	}
	if err := a.closeActive(request.SessionID); err != nil {
		return acp.CloseSessionResponse{}, err
	}
	a.logger.Info("session closed", "session_id", request.SessionID)
	return acp.CloseSessionResponse{}, nil
}

func (a *Agent) DeleteSession(
	_ context.Context,
	request acp.DeleteSessionRequest,
) (acp.DeleteSessionResponse, error) {
	if !validSessionID(request.SessionID) {
		return acp.DeleteSessionResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "invalid session ID")
	}
	if a.findSession(request.SessionID) != nil {
		return acp.DeleteSessionResponse{}, jrpc2.Errorf(
			jrpc2.InvalidParams,
			"cannot delete an active session",
		)
	}
	if err := a.store.delete(request.SessionID); err != nil {
		return acp.DeleteSessionResponse{}, err
	}
	a.logger.Info("session deleted", "session_id", request.SessionID)
	return acp.DeleteSessionResponse{}, nil
}

func (a *Agent) closeActive(id string) error {
	a.sessionsMu.Lock()
	value := a.sessions[id]
	if value == nil {
		a.sessionsMu.Unlock()
		return jrpc2.Errorf(jrpc2.InvalidParams, "unknown active session")
	}
	delete(a.sessions, id)
	a.sessionsMu.Unlock()
	value.close()
	value.log.close()
	return nil
}

func (a *Agent) validateActivation(
	cwd string,
	additionalDirectories []string,
	mcpServers []json.RawMessage,
	requireMCP bool,
) (string, error) {
	if !filepath.IsAbs(cwd) {
		return "", jrpc2.Errorf(jrpc2.InvalidParams, "session cwd must be absolute")
	}
	canonicalCWD, err := workspace.Canonical(cwd)
	if err != nil {
		return "", jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	if len(additionalDirectories) != 0 {
		return "", jrpc2.Errorf(
			jrpc2.InvalidParams,
			"additional directories are not supported",
		)
	}
	if requireMCP && mcpServers == nil {
		return "", jrpc2.Errorf(jrpc2.InvalidParams, "mcpServers is required")
	}
	if len(mcpServers) != 0 {
		return "", jrpc2.Errorf(jrpc2.InvalidParams, "MCP servers are not supported")
	}
	if problem := a.credentialProblem(); problem != "" {
		return "", jrpc2.Errorf(acp.ErrCodeAuthRequired, "%s", problem)
	}
	return canonicalCWD, nil
}

func (a *Agent) resolveConfiguration(
	ctx context.Context,
	cwd string,
) (requestConfiguration, error) {
	resolved, err := a.resolveSettings(cwd)
	if err != nil {
		return requestConfiguration{}, jrpc2.Errorf(jrpc2.InternalError, "%v", err)
	}
	if a.client == nil {
		return requestConfiguration{}, jrpc2.Errorf(
			jrpc2.InternalError,
			"an OpenRouter client is required to activate a session",
		)
	}
	entry, err := a.client.ModelInfo(ctx, resolved.Model)
	if err != nil {
		return requestConfiguration{}, jrpc2.Errorf(
			jrpc2.InternalError,
			"resolve model from %s: %v",
			resolved.ModelSource,
			err,
		)
	}
	if err := settings.Validate(entry, resolved); err != nil {
		return requestConfiguration{}, jrpc2.Errorf(jrpc2.InternalError, "%v", err)
	}
	now := time.Now()
	return requestConfiguration{
		Settings:      resolved,
		ContextWindow: entry.ContextWindow(),
		SystemPrompt:  composePrompt(cwd, now),
		Tools:         cloneTools(a.primaryTools.modelTools),
		ToolKinds:     a.configuredToolKinds(),
		Subagent: subagentConfiguration{
			SystemPrompt: composeSubagentPrompt(cwd, now),
			Tools:        cloneTools(a.subagentTools.modelTools),
		},
	}, nil
}

func (a *Agent) configuredToolKinds() map[string]acp.ToolKind {
	kinds := make(map[string]acp.ToolKind)
	for _, tool := range a.primaryTools.tools {
		if tool.Kind != "" {
			kinds[tool.Name] = tool.Kind
		}
	}
	if len(kinds) == 0 {
		return nil
	}
	return kinds
}

func (a *Agent) commit(value *session, kind string, payload any) error {
	value.stateMu.Lock()
	defer value.stateMu.Unlock()
	if value.poisoned {
		return errors.New("session activation is poisoned by an earlier persistence failure")
	}
	record, err := newRecord(value.state.sequence+1, kind, payload)
	if err != nil {
		return err
	}
	next := value.state.clone()
	if err := next.apply(record); err != nil {
		return fmt.Errorf("validate %s mutation: %w", kind, err)
	}
	if err := value.log.append(record); err != nil {
		value.poisoned = true
		return err
	}
	value.state = next
	a.logger.Info(
		"session record committed",
		"session_id", value.id,
		"record", kind,
		"sequence", record.Sequence,
	)
	return nil
}

// resolveSettings reads both settings layers and folds them into the frozen
// configuration one session sends with every request. Both files are read here
// rather than at startup: the runtime does not learn a workspace until cwd
// arrives, and re-reading per session keeps the two layers on one rule with no
// cache to invalidate.
func (a *Agent) resolveSettings(cwd string) (settings.Resolved, error) {
	workspacePath := settings.WorkspacePath(cwd)
	consulted := make([]string, 0, 2)

	global, err := settings.Load(a.settingsPath)
	if err != nil {
		return settings.Resolved{}, err
	}
	if a.settingsPath != "" {
		consulted = append(consulted, a.settingsPath)
	}
	workspace, err := settings.Load(workspacePath)
	if err != nil {
		return settings.Resolved{}, err
	}
	consulted = append(consulted, workspacePath)
	a.logger.Info(
		"session settings loaded",
		"global_path", a.settingsPath,
		"global_present", global != nil,
		"workspace_path", workspacePath,
		"workspace_present", workspace != nil,
	)

	resolved, err := settings.Resolve(settings.Merge(global, workspace), a.modelOverride)
	if errors.Is(err, settings.ErrNoModel) {
		message := "a model is required to create a session: set OX_MODEL"
		if len(consulted) > 0 {
			message += ` or "model" in ` + strings.Join(consulted, " or ")
		}
		return settings.Resolved{}, errors.New(message)
	}
	if err != nil {
		return settings.Resolved{}, fmt.Errorf(
			"resolve settings from %s: %w",
			strings.Join(consulted, " and "),
			err,
		)
	}
	return resolved, err
}

func (a *Agent) credentialSource() credentials.Source {
	if a.credentials == nil {
		return credentials.SourceNone
	}
	return a.credentials.Source()
}

func authRequiredError() error {
	return jrpc2.Errorf(acp.ErrCodeAuthRequired, "%s", credentials.NoCredentialMessage)
}

func (a *Agent) credentialProblem() string {
	if a.credentials == nil || a.credentials.Key() == "" {
		return credentials.NoCredentialMessage
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if a.credentials.Key() == a.rejectedKey {
		return a.rejectionText
	}
	return ""
}

func (a *Agent) rejectCredential(key, message string) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	a.rejectedKey = key
	a.rejectionText = message
}

func (a *Agent) acceptCredential(key string) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if key == "" || key == a.rejectedKey {
		a.rejectedKey = ""
		a.rejectionText = ""
	}
}

func (a *Agent) Prompt(
	ctx context.Context,
	request acp.PromptRequest,
) (acp.PromptResponse, error) {
	if err := request.Validate(); err != nil {
		return acp.PromptResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	value := a.findSession(request.SessionID)
	if value == nil {
		return acp.PromptResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "unknown session")
	}
	userMessage, err := promptMessage(request.Prompt)
	if err != nil {
		return acp.PromptResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	_ = userMessage
	var promptMeta acp.Metadata
	if len(request.Prompt) > 0 {
		promptMeta = request.Prompt[0].Meta
	}
	turnID, err := randomID()
	if err != nil {
		return acp.PromptResponse{}, err
	}
	messageID, err := metadataString(promptMeta, acp.MetaMessageID)
	if err != nil {
		return acp.PromptResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	if messageID == "" {
		messageID, err = randomID()
		if err != nil {
			return acp.PromptResponse{}, err
		}
	}
	runCtx, active, release, err := value.claim(ctx, turnID)
	if err != nil {
		return acp.PromptResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	adapter := newAdapter(value.id, func(notification acp.SessionNotification) error {
		return jrpc2.ServerFromContext(ctx).Notify(ctx, "session/update", notification)
	})
	server := jrpc2.ServerFromContext(ctx)
	fileSystem := a.clientFileSystem(server, value.id)
	terminal := a.clientTerminalOperations(server, value.id)
	requestPermission := func(
		requestCtx context.Context,
		request acp.RequestPermissionRequest,
	) (acp.RequestPermissionResponse, error) {
		response, err := server.Callback(
			requestCtx,
			acp.MethodSessionRequestPermission,
			request,
		)
		if err != nil {
			return acp.RequestPermissionResponse{}, err
		}
		var result acp.RequestPermissionResponse
		if err := response.UnmarshalResult(&result); err != nil {
			return acp.RequestPermissionResponse{}, err
		}
		return result, nil
	}
	defer adapter.close()
	if err := a.commit(value, recordUserMessage, userMessageRecord{
		TurnID:    turnID,
		MessageID: messageID,
		Content:   append([]acp.ContentBlock(nil), request.Prompt...),
	}); err != nil {
		return acp.PromptResponse{}, fmt.Errorf("persist user message: %w", err)
	}

	a.logger.Info(
		"prompt started",
		"session_id", value.id,
		"turn_id", turnID,
		"message_id", messageID,
	)
	events := make(chan event)
	outcome := make(chan loopOutcome, 1)
	go func() {
		outcome <- a.run(runCtx, value, active, requestPermission, fileSystem, terminal, events)
		close(events)
	}()

	var notifyErr error
	for events != nil {
		select {
		case current, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if notifyErr == nil {
				if err := adapter.handle(current); err != nil {
					notifyErr = a.adapterFailed(value.id, active, err)
				}
			}
		case <-adapter.tick():
			if notifyErr == nil {
				if err := adapter.flushDirty(); err != nil {
					notifyErr = a.adapterFailed(value.id, active, err)
				}
			}
		}
	}
	result := <-outcome
	cancelledByClient := release()
	released = true
	if notifyErr != nil {
		return acp.PromptResponse{}, notifyErr
	}
	if cancelledByClient {
		result.response.StopReason = acp.StopReasonCancelled
		result.response.Usage = value.state.usage.acp()
		result.err = nil
	}
	if result.err != nil {
		if errors.Is(result.err, context.Canceled) {
			return acp.PromptResponse{}, jrpc2.Errorf(
				acp.ErrCodeRequestCancelled,
				"request cancelled",
			)
		}
		return acp.PromptResponse{}, result.err
	}
	a.logger.Info(
		"prompt stopped",
		"session_id", value.id,
		"turn_id", turnID,
		"stop_reason", result.response.StopReason,
	)
	return result.response, nil
}

func (a *Agent) clientFileSystem(server *jrpc2.Server, sessionID string) ClientFileSystem {
	a.clientCapabilitiesMu.RLock()
	capabilities := a.clientFS
	a.clientCapabilitiesMu.RUnlock()

	var fileSystem ClientFileSystem
	if capabilities.ReadTextFile {
		fileSystem.ReadTextFile = func(
			ctx context.Context,
			path string,
			line *int,
			limit *int,
		) (string, error) {
			request := acp.ReadTextFileRequest{
				SessionID: sessionID,
				Path:      path,
				Line:      line,
				Limit:     limit,
			}
			if err := request.Validate(); err != nil {
				return "", fmt.Errorf("validate %s request: %w", acp.MethodFSReadTextFile, err)
			}
			response, err := server.Callback(ctx, acp.MethodFSReadTextFile, request)
			if err != nil {
				return "", err
			}
			var result acp.ReadTextFileResponse
			if err := response.UnmarshalResult(&result); err != nil {
				return "", err
			}
			return result.Content, nil
		}
	}
	if capabilities.WriteTextFile {
		fileSystem.WriteTextFile = func(ctx context.Context, path, content string) error {
			request := acp.WriteTextFileRequest{
				SessionID: sessionID,
				Path:      path,
				Content:   content,
			}
			if err := request.Validate(); err != nil {
				return fmt.Errorf("validate %s request: %w", acp.MethodFSWriteTextFile, err)
			}
			response, err := server.Callback(ctx, acp.MethodFSWriteTextFile, request)
			if err != nil {
				return err
			}
			var result acp.WriteTextFileResponse
			return response.UnmarshalResult(&result)
		}
	}
	return fileSystem
}

func (a *Agent) clientTerminalOperations(
	server *jrpc2.Server,
	sessionID string,
) ClientTerminal {
	a.clientCapabilitiesMu.RLock()
	available := a.clientTerminal
	a.clientCapabilitiesMu.RUnlock()
	if !available {
		return ClientTerminal{}
	}

	callback := func(ctx context.Context, method string, request any, result any) error {
		response, err := server.Callback(ctx, method, request)
		if err != nil {
			return err
		}
		return response.UnmarshalResult(result)
	}
	return ClientTerminal{
		Create: func(
			ctx context.Context,
			request acp.CreateTerminalRequest,
		) (acp.CreateTerminalResponse, error) {
			request.SessionID = sessionID
			if err := request.Validate(); err != nil {
				return acp.CreateTerminalResponse{}, fmt.Errorf(
					"validate %s request: %w", acp.MethodTerminalCreate, err,
				)
			}
			var result acp.CreateTerminalResponse
			if err := callback(ctx, acp.MethodTerminalCreate, request, &result); err != nil {
				return acp.CreateTerminalResponse{}, err
			}
			if err := result.Validate(); err != nil {
				return acp.CreateTerminalResponse{}, fmt.Errorf(
					"validate %s response: %w", acp.MethodTerminalCreate, err,
				)
			}
			return result, nil
		},
		Output: func(
			ctx context.Context,
			request acp.TerminalOutputRequest,
		) (acp.TerminalOutputResponse, error) {
			request.SessionID = sessionID
			if err := request.Validate(); err != nil {
				return acp.TerminalOutputResponse{}, fmt.Errorf(
					"validate %s request: %w", acp.MethodTerminalOutput, err,
				)
			}
			var result acp.TerminalOutputResponse
			return result, callback(ctx, acp.MethodTerminalOutput, request, &result)
		},
		WaitForExit: func(
			ctx context.Context,
			request acp.WaitForTerminalExitRequest,
		) (acp.WaitForTerminalExitResponse, error) {
			request.SessionID = sessionID
			if err := request.Validate(); err != nil {
				return acp.WaitForTerminalExitResponse{}, fmt.Errorf(
					"validate %s request: %w", acp.MethodTerminalWaitForExit, err,
				)
			}
			var result acp.WaitForTerminalExitResponse
			return result, callback(ctx, acp.MethodTerminalWaitForExit, request, &result)
		},
		Kill: func(ctx context.Context, request acp.KillTerminalRequest) error {
			request.SessionID = sessionID
			if err := request.Validate(); err != nil {
				return fmt.Errorf("validate %s request: %w", acp.MethodTerminalKill, err)
			}
			var result acp.KillTerminalResponse
			return callback(ctx, acp.MethodTerminalKill, request, &result)
		},
		Release: func(ctx context.Context, request acp.ReleaseTerminalRequest) error {
			request.SessionID = sessionID
			if err := request.Validate(); err != nil {
				return fmt.Errorf("validate %s request: %w", acp.MethodTerminalRelease, err)
			}
			var result acp.ReleaseTerminalResponse
			return callback(ctx, acp.MethodTerminalRelease, request, &result)
		},
	}
}

func (a *Agent) adapterFailed(sessionID string, active *activeTurn, err error) error {
	a.logger.Error("session update failed", "session_id", sessionID, "error", err)
	active.cancel()
	return fmt.Errorf("send session update: %w", err)
}

func (a *Agent) Cancel(_ context.Context, request acp.CancelNotification) error {
	if err := request.Validate(); err != nil {
		return jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	value := a.findSession(request.SessionID)
	if value == nil {
		return jrpc2.Errorf(jrpc2.InvalidParams, "unknown session")
	}
	if value.cancel() {
		a.logger.Info(
			"prompt cancellation requested",
			"session_id", value.id,
		)
	}
	return nil
}

func (a *Agent) CancelRequest(
	ctx context.Context,
	notification acp.CancelRequestNotification,
) error {
	if err := notification.Validate(); err != nil {
		return jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	a.logger.Debug("cancelling request", "request_id", string(notification.RequestID))
	jrpc2.ServerFromContext(ctx).CancelRequest(string(notification.RequestID))
	return nil
}

func metadataString(meta acp.Metadata, key string) (string, error) {
	value, present := meta[key]
	if !present {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return text, nil
}

func (a *Agent) findSession(id string) *session {
	a.sessionsMu.RLock()
	defer a.sessionsMu.RUnlock()
	return a.sessions[id]
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

type session struct {
	id           string
	state        durableState
	log          *sessionLog
	stateMu      sync.Mutex
	poisoned     bool
	mu           sync.Mutex
	nextTurn     uint64
	active       *activeTurn
	closing      bool
	grants       map[string][]string
	reads        fileReads
	approvalMu   sync.Mutex
	exclusiveMu  sync.Mutex
	callIDsMu    sync.Mutex
	callIDs      map[string]struct{}
	readScopesMu sync.Mutex
	readScopes   map[*fileReads]struct{}
}

func (s *session) granted(tool Tool, arguments json.RawMessage) bool {
	s.mu.Lock()
	rules := append([]string(nil), s.grants[tool.Name]...)
	s.mu.Unlock()
	if tool.Covered != nil {
		return tool.Covered(rules, arguments)
	}
	return slices.Contains(rules, "")
}

func (s *session) grant(name, rule string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grants == nil {
		s.grants = make(map[string][]string)
	}
	if !slices.Contains(s.grants[name], rule) {
		s.grants[name] = append(s.grants[name], rule)
	}
}

type activeTurn struct {
	id                uint64
	turnID            string
	cancel            context.CancelFunc
	cancelledByClient atomic.Bool
	done              chan struct{}
}

func (s *session) claim(
	parent context.Context,
	turnID string,
) (context.Context, *activeTurn, func() bool, error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, nil, nil, errors.New("session is closing")
	}
	if s.active != nil {
		s.mu.Unlock()
		return nil, nil, nil, errors.New("session already has an active prompt")
	}
	ctx, cancel := context.WithCancel(parent)
	s.nextTurn++
	active := &activeTurn{
		id:     s.nextTurn,
		turnID: turnID,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	s.active = active
	s.mu.Unlock()
	var releaseOnce sync.Once
	var cancelledByClient bool
	return ctx, active, func() bool {
		releaseOnce.Do(func() {
			cancel()
			s.mu.Lock()
			cancelledByClient = active.cancelledByClient.Load()
			if s.active != nil && s.active.id == active.id {
				s.active = nil
			}
			close(active.done)
			s.mu.Unlock()
		})
		return cancelledByClient
	}, nil
}

func (s *session) close() {
	s.mu.Lock()
	s.closing = true
	active := s.active
	if active != nil {
		active.cancelledByClient.Store(true)
	}
	s.mu.Unlock()
	if active == nil {
		return
	}
	active.cancel()
	<-active.done
}

func (s *session) cancel() bool {
	s.mu.Lock()
	active := s.active
	if active != nil {
		active.cancelledByClient.Store(true)
	}
	s.mu.Unlock()
	if active == nil {
		return false
	}
	active.cancel()
	return true
}
