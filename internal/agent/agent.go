package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
)

type Agent struct {
	name               string
	version            string
	environment        config.Environment
	credentials        *credentials.Store
	client             *openrouter.Client
	logger             *slog.Logger
	clientCapabilities atomic.Pointer[acp.ClientCapabilities]

	sessionsMu sync.Mutex
	sessions   map[string]*session
}

func New(
	name, version string,
	environment config.Environment,
	credentialStore *credentials.Store,
	client *openrouter.Client,
	logger *slog.Logger,
) *Agent {
	return &Agent{
		name:        name,
		version:     version,
		environment: environment,
		credentials: credentialStore,
		client:      client,
		logger:      logger,
		sessions:    make(map[string]*session),
	}
}

func (a *Agent) Methods() handler.Map {
	return handler.Map{
		"initialize":       handler.New(a.Initialize),
		"session/new":      handler.New(a.NewSession),
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
	a.clientCapabilities.Store(request.ClientCapabilities)

	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: false,
			PromptCapabilities: acp.PromptCapabilities{
				Image:           true,
				Audio:           true,
				EmbeddedContext: true,
			},
		},
		AgentInfo: acp.Implementation{
			Name:    a.name,
			Version: a.version,
		},
		AuthMethods: []json.RawMessage{},
	}, nil
}

// CancelRequest must never wait for the request it cancels: jrpc2 does not
// dispatch the next input batch until every previously issued notification
// handler returns.
func (a *Agent) CancelRequest(ctx context.Context, notification acp.CancelRequestNotification) error {
	if err := notification.Validate(); err != nil {
		return jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}

	a.logger.Debug("cancelling request", "request_id", string(notification.RequestID))
	jrpc2.ServerFromContext(ctx).CancelRequest(string(notification.RequestID))
	return nil
}
