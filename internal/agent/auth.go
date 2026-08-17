package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/creachadair/jrpc2"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
)

const (
	openRouterAuthMethodID         = "openrouter"
	openRouterTerminalAuthMethodID = "openrouter-terminal"
)

func authMethods(capabilities *acp.ClientCapabilities) []acp.AuthMethod {
	methods := []acp.AuthMethod{{
		ID:          openRouterAuthMethodID,
		Name:        "OpenRouter credential",
		Description: "Use an OpenRouter API key already available to Ox.",
	}}
	if capabilities != nil && capabilities.Auth != nil && capabilities.Auth.Terminal {
		methods = append(methods, acp.AuthMethod{
			ID:          openRouterTerminalAuthMethodID,
			Type:        "terminal",
			Name:        "Log in to OpenRouter",
			Description: "Enter and store an OpenRouter API key in a terminal.",
			Args:        []string{"login"},
		})
	}
	return methods
}

func (a *Agent) Authenticate(
	ctx context.Context,
	request acp.AuthenticateRequest,
) (acp.AuthenticateResponse, error) {
	if err := request.Validate(); err != nil {
		return acp.AuthenticateResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	switch request.MethodID {
	case openRouterAuthMethodID:
	case openRouterTerminalAuthMethodID:
		return acp.AuthenticateResponse{}, jrpc2.Errorf(
			jrpc2.InvalidParams,
			"authentication method %q is completed in a terminal",
			request.MethodID,
		)
	default:
		return acp.AuthenticateResponse{}, jrpc2.Errorf(
			jrpc2.InvalidParams,
			"unknown authentication method %q",
			request.MethodID,
		)
	}

	a.credentials.Refresh()
	key := a.credentials.Key()
	if key == "" {
		return acp.AuthenticateResponse{}, authRequiredError(credentials.NoCredentialMessage)
	}
	if err := a.client.VerifyCredential(ctx, key); err != nil {
		if errors.Is(err, openrouter.ErrCredentialRejected) {
			a.rejectCredential(key, err.Error())
			return acp.AuthenticateResponse{}, authRequiredError(err.Error())
		}
		return acp.AuthenticateResponse{}, jrpc2.Errorf(
			jrpc2.InternalError,
			"%v",
			fmt.Errorf("verify OpenRouter credential: %w", err),
		)
	}
	a.acceptCredential(key)
	a.logger.Info("client authenticated", "credential_source", a.credentials.Source())
	return acp.AuthenticateResponse{}, nil
}

func (a *Agent) Logout(
	_ context.Context,
	_ acp.LogoutRequest,
) (acp.LogoutResponse, error) {
	if err := a.credentials.Clear(); err != nil {
		return acp.LogoutResponse{}, jrpc2.Errorf(
			jrpc2.InternalError,
			"%v",
			fmt.Errorf("log out of OpenRouter: %w", err),
		)
	}
	a.acceptCredential("")
	a.logger.Info("client logged out")
	return acp.LogoutResponse{}, nil
}

// credentialProblem reports why the resolved credential cannot be used, or an
// empty string when it can. It never verifies the credential: authenticate
// answered that already, and paying a round trip per turn to re-answer it
// would be waste.
func (a *Agent) credentialProblem() string {
	key := a.credentials.Key()
	if key == "" {
		return credentials.NoCredentialMessage
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if key == a.rejectedKey {
		return a.rejectionMessage
	}
	return ""
}

func (a *Agent) rejectCredential(key, message string) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	a.rejectedKey, a.rejectionMessage = key, message
}

// acceptCredential forgets a rejection that no longer applies, because key has
// just been verified or logout left no credential to reject.
func (a *Agent) acceptCredential(key string) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if key == "" || key == a.rejectedKey {
		a.rejectedKey, a.rejectionMessage = "", ""
	}
}

func authRequiredError(message string) error {
	return jrpc2.Errorf(jrpc2.Code(acp.ErrCodeAuthRequired), "%s", message)
}
