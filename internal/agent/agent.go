package agent

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/kkestell/ox/internal/acp"
)

type Agent struct {
	name               string
	version            string
	logger             *slog.Logger
	clientCapabilities *acp.ClientCapabilities
}

func New(name, version string, logger *slog.Logger) *Agent {
	return &Agent{name: name, version: version, logger: logger}
}

func (a *Agent) Methods() handler.Map {
	return handler.Map{
		"initialize":       handler.New(a.Initialize),
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
	a.clientCapabilities = request.ClientCapabilities

	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:        false,
			PromptCapabilities: acp.PromptCapabilities{},
		},
		AgentInfo: acp.Implementation{
			Name:    a.name,
			Version: a.version,
		},
		AuthMethods: []json.RawMessage{},
	}, nil
}

func (a *Agent) CancelRequest(ctx context.Context, notification acp.CancelRequestNotification) error {
	if err := notification.Validate(); err != nil {
		return jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}

	a.logger.Debug("cancelling request", "request_id", string(notification.RequestID))
	jrpc2.ServerFromContext(ctx).CancelRequest(string(notification.RequestID))
	return nil
}
