package e2e

import (
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestNewSessionRequiresACredential(t *testing.T) {
	child := start(t, withEnvironment("OPENROUTER_API_KEY", ""))
	initialize(t, child)

	responseError := child.requestError("session/new", newSessionRequest(child.cwd))
	if responseError.Code != acp.ErrCodeAuthRequired {
		t.Errorf("error code = %d, want %d", responseError.Code, acp.ErrCodeAuthRequired)
	}
	for _, want := range []string{"OPENROUTER_API_KEY", "ox", "openrouter"} {
		if !strings.Contains(responseError.Message, want) {
			t.Errorf("error message = %q, want it to name %s", responseError.Message, want)
		}
	}

	// A round trip after the failure proves Ox keeps answering.
	initialize(t, child)
}

func TestCredentialReachesTheProviderAndNotTheLogs(t *testing.T) {
	const key = "credential-that-must-stay-secret"
	model := startModel(t, sse(evFinishReason("stop")))
	child, session := startSession(t,
		withModel(model),
		withEnvironment("OPENROUTER_API_KEY", "  "+key+"  "),
	)
	prompt(t, child, session, "credential check")

	if got := model.requestFor("credential check").Authorization; got != "Bearer "+key {
		t.Errorf("Authorization = %q, want %q", got, "Bearer "+key)
	}
	child.stop()
	if stderr := child.stderr.String(); strings.Contains(stderr, key) {
		t.Errorf("stderr leaked the credential: %s", stderr)
	}
}
