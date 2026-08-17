package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/server"
	"github.com/zalando/go-keyring"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
)

func TestAuthMethodsFollowTerminalCapability(t *testing.T) {
	wantStored := acp.AuthMethod{
		ID: openRouterAuthMethodID, Name: "OpenRouter credential",
		Description: "Use an OpenRouter API key already available to Ox.",
	}
	for _, test := range []struct {
		name         string
		capabilities *acp.ClientCapabilities
		count        int
	}{
		{name: "omitted", count: 1},
		{name: "auth omitted", capabilities: &acp.ClientCapabilities{Terminal: true}, count: 1},
		{
			name: "terminal auth", count: 2,
			capabilities: &acp.ClientCapabilities{Auth: &acp.ClientAuthCapabilities{Terminal: true}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			methods := authMethods(test.capabilities)
			if len(methods) != test.count {
				t.Fatalf("auth methods = %#v", methods)
			}
			if !reflect.DeepEqual(methods[0], wantStored) {
				t.Errorf("stored method = %#v", methods[0])
			}
			if test.count == 2 {
				terminal := methods[1]
				if terminal.ID != openRouterTerminalAuthMethodID || terminal.Type != "terminal" ||
					len(terminal.Args) != 1 || terminal.Args[0] != "login" {
					t.Errorf("terminal method = %#v", terminal)
				}
			}
		})
	}
}

func TestAuthenticateRefreshesAndVerifiesCredential(t *testing.T) {
	keyring.MockInit()
	store := credentials.NewStore("", false, authTestLogger())
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	agent := authTestAgent(store, server.URL)

	// Store the key behind the cache after the agent has started.
	if err := keyring.Set("ox", "openrouter", "late-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Authenticate(t.Context(), acp.AuthenticateRequest{MethodID: openRouterAuthMethodID}); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer late-key" {
		t.Errorf("Authorization = %q", authorization)
	}
	if _, err := agent.NewSession(t.Context(), newAuthTestSessionRequest(t)); err != nil {
		t.Fatalf("session/new after authenticate: %v", err)
	}
}

func TestAuthenticateMapsFailures(t *testing.T) {
	t.Run("missing credential", func(t *testing.T) {
		keyring.MockInit()
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
		t.Cleanup(server.Close)
		agent := authTestAgent(credentials.NewStore("", false, authTestLogger()), server.URL)
		_, err := agent.Authenticate(t.Context(), acp.AuthenticateRequest{MethodID: openRouterAuthMethodID})
		assertAuthErrorCode(t, err, acp.ErrCodeAuthRequired)
		if requests != 0 {
			t.Errorf("provider requests = %d", requests)
		}
	})

	t.Run("rejected credential", func(t *testing.T) {
		keyring.MockInit()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(writer, `{"error":{"message":"User not found."}}`)
		}))
		t.Cleanup(server.Close)
		agent := authTestAgent(credentials.NewStore("bad-key", false, authTestLogger()), server.URL)
		_, err := agent.Authenticate(t.Context(), acp.AuthenticateRequest{MethodID: openRouterAuthMethodID})
		assertAuthErrorCode(t, err, acp.ErrCodeAuthRequired, "User not found.")
		// The gate repeats why the credential is unusable instead of claiming
		// there is none.
		_, err = agent.NewSession(t.Context(), newAuthTestSessionRequest(t))
		assertAuthErrorCode(t, err, acp.ErrCodeAuthRequired, "User not found.")
	})

	t.Run("provider failure", func(t *testing.T) {
		keyring.MockInit()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "later", http.StatusServiceUnavailable)
		}))
		t.Cleanup(server.Close)
		agent := authTestAgent(credentials.NewStore("key", false, authTestLogger()), server.URL)
		_, err := agent.Authenticate(t.Context(), acp.AuthenticateRequest{MethodID: openRouterAuthMethodID})
		if err == nil || jrpc2.ErrorCode(err) == jrpc2.Code(acp.ErrCodeAuthRequired) || !strings.Contains(err.Error(), "503") {
			t.Fatalf("authenticate error = %v", err)
		}
	})
}

func TestAuthenticateRejectsInvalidMethods(t *testing.T) {
	agent := testAgent()
	for _, test := range []struct {
		name    string
		request acp.AuthenticateRequest
		want    string
	}{
		{name: "missing", want: "methodId"},
		{name: "unknown", request: acp.AuthenticateRequest{MethodID: "unknown"}, want: "unknown"},
		{
			name: "terminal", request: acp.AuthenticateRequest{MethodID: openRouterTerminalAuthMethodID},
			want: "completed in a terminal",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := agent.Authenticate(t.Context(), test.request)
			assertAuthErrorCode(t, err, int(jrpc2.InvalidParams))
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("authenticate error = %v", err)
			}
		})
	}
}

func TestLogoutKeepsSessionAndGatesLaterPrompts(t *testing.T) {
	keyring.MockInit()
	store := credentials.NewStore("", false, authTestLogger())
	if err := store.Set("key"); err != nil {
		t.Fatal(err)
	}
	agent := authTestAgent(store, "http://127.0.0.1:1")
	created, err := agent.NewSession(t.Context(), newAuthTestSessionRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Logout(t.Context(), acp.LogoutRequest{}); err != nil {
		t.Fatal(err)
	}
	if agent.findSession(created.SessionID) == nil {
		t.Fatal("logout removed the existing session")
	}
	_, err = agent.Prompt(t.Context(), acp.PromptRequest{
		SessionID: created.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	})
	assertAuthErrorCode(t, err, acp.ErrCodeAuthRequired, credentials.NoCredentialMessage)
	_, err = agent.NewSession(t.Context(), newAuthTestSessionRequest(t))
	assertAuthErrorCode(t, err, acp.ErrCodeAuthRequired, credentials.NoCredentialMessage)

	if _, err := agent.Logout(t.Context(), acp.LogoutRequest{}); err != nil {
		t.Fatalf("second logout: %v", err)
	}
}

func TestLogoutRefusesEnvironmentCredential(t *testing.T) {
	keyring.MockInit()
	store := credentials.NewStore("environment-key", false, authTestLogger())
	agent := authTestAgent(store, "")
	_, err := agent.Logout(t.Context(), acp.LogoutRequest{})
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("logout error = %v", err)
	}
	if store.Key() != "environment-key" {
		t.Errorf("credential changed to %q", store.Key())
	}
}

func TestAuthenticationChangesDoNotInterruptRunningPrompt(t *testing.T) {
	for _, action := range []string{"authenticate", "logout"} {
		t.Run(action, func(t *testing.T) {
			keyring.MockInit()
			store := credentials.NewStore("", false, authTestLogger())
			if err := store.Set("key"); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			release := make(chan struct{})
			model := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/key":
					writer.WriteHeader(http.StatusOK)
				case "/chat/completions":
					close(started)
					<-release
					writer.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				default:
					http.NotFound(writer, request)
				}
			}))
			t.Cleanup(model.Close)
			agent := authTestAgent(store, model.URL)
			created, err := agent.NewSession(t.Context(), newAuthTestSessionRequest(t))
			if err != nil {
				t.Fatal(err)
			}

			local := server.NewLocal(agent.Methods(), &server.LocalOptions{
				Server: &jrpc2.ServerOptions{AllowPush: true, Concurrency: 2},
			})
			defer func() {
				if err := local.Close(); err != nil {
					t.Errorf("close local server: %v", err)
				}
			}()
			promptDone := make(chan error, 1)
			go func() {
				_, err := local.Client.Call(context.Background(), "session/prompt", acp.PromptRequest{
					SessionID: created.SessionID,
					Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
				})
				promptDone <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("prompt did not reach the provider")
			}

			switch action {
			case "authenticate":
				_, err = agent.Authenticate(t.Context(), acp.AuthenticateRequest{MethodID: openRouterAuthMethodID})
			case "logout":
				_, err = agent.Logout(t.Context(), acp.LogoutRequest{})
			}
			if err != nil {
				t.Fatal(err)
			}
			close(release)
			select {
			case err := <-promptDone:
				if err != nil {
					t.Fatalf("running prompt failed: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("running prompt did not finish")
			}
		})
	}
}

func authTestAgent(store *credentials.Store, baseURL string) *Agent {
	logger := authTestLogger()
	client := &openrouter.Client{APIKey: store.Key, BaseURL: baseURL, Logger: logger}
	return New(
		"ox", "0.0.1", config.Environment{ModelOverride: "test/model"},
		store, client, logger,
	)
}

func authTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newAuthTestSessionRequest(t *testing.T) acp.NewSessionRequest {
	t.Helper()
	return acp.NewSessionRequest{CWD: t.TempDir(), MCPServers: []json.RawMessage{}}
}

func assertAuthErrorCode(t *testing.T, err error, want int, messages ...string) {
	t.Helper()
	if err == nil || jrpc2.ErrorCode(err) != jrpc2.Code(want) {
		t.Fatalf("error = %v, code = %d, want %d", err, jrpc2.ErrorCode(err), want)
	}
	for _, message := range messages {
		if !strings.Contains(err.Error(), message) {
			t.Errorf("error = %v, want %q", err, message)
		}
	}
}
