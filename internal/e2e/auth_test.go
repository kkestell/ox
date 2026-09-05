package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestInitializeAdvertisesTerminalAuthenticationWhenSupported(t *testing.T) {
	child := start(t)
	result := child.request("initialize", acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion,
		ClientCapabilities: &acp.ClientCapabilities{
			Auth: &acp.ClientAuthCapabilities{Terminal: true},
		},
	})
	var response acp.InitializeResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.AuthMethods) != 2 {
		t.Fatalf("authMethods = %#v", response.AuthMethods)
	}
	terminal := response.AuthMethods[1]
	if terminal.ID != "openrouter-terminal" || terminal.Type != "terminal" ||
		len(terminal.Args) != 1 || terminal.Args[0] != "login" {
		t.Errorf("terminal auth method = %#v", terminal)
	}
}

func TestAuthenticateVerifiesCredentialAndEnablesSession(t *testing.T) {
	model := startModel(t)
	child := start(t, withModel(model), withEnvironment("OPENROUTER_API_KEY", "verified-key"))
	initialize(t, child)

	child.request("authenticate", acp.AuthenticateRequest{MethodID: "openrouter"})
	checks := model.checks()
	if len(checks) != 1 || checks[0] != "Bearer verified-key" {
		t.Errorf("credential checks = %#v", checks)
	}
	_ = newSession(t, child, child.cwd)
}

func TestAuthenticateRequiresCredentialWithoutContactingProvider(t *testing.T) {
	model := startModel(t)
	child := start(t, withModel(model), withEnvironment("OPENROUTER_API_KEY", ""))
	initialize(t, child)

	responseError := child.requestError("authenticate", acp.AuthenticateRequest{MethodID: "openrouter"})
	assertE2EAuthError(t, responseError, acp.ErrCodeAuthRequired, "OPENROUTER_API_KEY", "ox", "openrouter")
	if checks := model.checks(); len(checks) != 0 {
		t.Errorf("credential checks = %#v", checks)
	}
}

func TestAuthenticateReportsRejectedCredentialAndKeepsSessionGated(t *testing.T) {
	model := startModel(t)
	model.rejectCredential("User not found.")
	child := start(t, withModel(model), withEnvironment("OPENROUTER_API_KEY", "bad-key"))
	initialize(t, child)

	responseError := child.requestError("authenticate", acp.AuthenticateRequest{MethodID: "openrouter"})
	assertE2EAuthError(t, responseError, acp.ErrCodeAuthRequired, "User not found.")
	// The gate reports the rejection rather than sending someone who has set
	// OPENROUTER_API_KEY back to set it again.
	responseError = child.requestError("session/new", newSessionRequest(child.cwd))
	assertE2EAuthError(t, responseError, acp.ErrCodeAuthRequired, "User not found.")
}

func TestAuthenticateReportsProviderFailureAsInternalError(t *testing.T) {
	model := startModel(t)
	model.failCredential(503, "provider unavailable")
	child := start(t, withModel(model))
	initialize(t, child)

	responseError := child.requestError("authenticate", acp.AuthenticateRequest{MethodID: "openrouter"})
	assertE2EAuthError(t, responseError, -32603, "503")
}

func TestAuthenticateRejectsInvalidMethodRequests(t *testing.T) {
	for _, test := range []struct {
		name   string
		params any
		want   string
	}{
		{name: "no params", params: nil, want: "methodId"},
		{name: "missing method", params: acp.AuthenticateRequest{}, want: "methodId"},
		{name: "unknown method", params: acp.AuthenticateRequest{MethodID: "unknown"}, want: "unknown"},
		{
			name: "terminal method", params: acp.AuthenticateRequest{MethodID: "openrouter-terminal"},
			want: "completed in a terminal",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			child := start(t)
			initialize(t, child)
			responseError := child.requestError("authenticate", test.params)
			assertE2EAuthError(t, responseError, -32602, test.want)
		})
	}
}

func TestLogoutRefusesEnvironmentCredential(t *testing.T) {
	child := start(t, withEnvironment("OPENROUTER_API_KEY", "environment-key"))
	initialize(t, child)
	responseError := child.requestError("logout", acp.LogoutRequest{})
	assertE2EAuthError(t, responseError, -32603, "OPENROUTER_API_KEY")
	_ = newSession(t, child, child.cwd)
}

func TestLoginCommandRefusesDisabledKeyringBeforeReading(t *testing.T) {
	result := runCommand(t, "secret-that-must-not-appear\n", []string{"login"})
	if result.ExitCode != 1 {
		t.Fatalf("exit code = %d, stderr = %s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "OX_KEYRING_DISABLED") {
		t.Errorf("stderr = %q", result.Stderr)
	}
	if strings.Contains(result.Stdout+result.Stderr, "secret-that-must-not-appear") {
		t.Errorf("command output leaked input: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestUnknownArgumentIsAUsageError(t *testing.T) {
	result := runCommand(t, "", []string{"unknown"})
	if result.ExitCode != 2 || !strings.Contains(result.Stderr, "usage: ox [--trace path] | ox login") {
		t.Fatalf("result = %#v", result)
	}
}

func assertE2EAuthError(t *testing.T, got rpcError, wantCode int, messages ...string) {
	t.Helper()
	if got.Code != wantCode {
		t.Fatalf("error code = %d, want %d; message = %q", got.Code, wantCode, got.Message)
	}
	for _, message := range messages {
		if !strings.Contains(got.Message, message) {
			t.Errorf("error message = %q, want %q", got.Message, message)
		}
	}
}
