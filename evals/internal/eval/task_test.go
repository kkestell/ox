package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestLoadTaskValidatesAndRevisionTracksContent(t *testing.T) {
	root := t.TempDir()
	writeTestTask(t, root, Task{
		Schema: 1, ID: "edit", Budget: Budget{TimeoutMS: 1000, ProviderRequests: 2},
		Phases:  []Phase{{Action: "prompt", Prompt: "edit", Permission: "allow"}},
		Success: Success{Files: map[string]string{"answer.txt": "yes\n"}},
	})
	first, err := LoadTask(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "seed.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := LoadTask(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.revision == second.revision {
		t.Fatal("task revision did not change with fixture content")
	}
}

func TestLoadTaskRejectsUnsafeAndInvalidManifests(t *testing.T) {
	for name, task := range map[string]Task{
		"schema":     {Schema: 2, ID: "task", Budget: Budget{1, 1}, Phases: []Phase{{Action: "prompt", Prompt: "x", Permission: "allow"}}, Success: Success{Files: map[string]string{"x": "x"}}},
		"budget":     {Schema: 1, ID: "task", Phases: []Phase{{Action: "prompt", Prompt: "x", Permission: "allow"}}, Success: Success{Files: map[string]string{"x": "x"}}},
		"path":       {Schema: 1, ID: "task", Budget: Budget{1, 1}, Phases: []Phase{{Action: "prompt", Prompt: "x", Permission: "allow"}}, Success: Success{Files: map[string]string{"../x": "x"}}},
		"permission": {Schema: 1, ID: "task", Budget: Budget{1, 1}, Phases: []Phase{{Action: "prompt", Prompt: "x", Permission: "maybe"}}, Success: Success{Files: map[string]string{"x": "x"}}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeTestTask(t, root, task)
			if _, err := LoadTask(root); err == nil {
				t.Fatal("invalid task loaded")
			}
		})
	}
}

func TestVerifyTaskUsesFilesAndProtectedOverlay(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace-run")
	if err := os.MkdirAll(filepath.Join(root, "verifier"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "answer.txt"), []byte("yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "verifier", "check.sh"), []byte("test \"$(cat answer.txt)\" = yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task := Task{
		Budget: Budget{TimeoutMS: 1000}, path: root,
		Success: Success{
			Files:   map[string]string{"answer.txt": "yes\n"},
			Overlay: "verifier", Command: []string{"sh", "check.sh"},
		},
	}
	if _, err := verifyTask(context.Background(), task, workspace); err != nil {
		t.Fatal(err)
	}
}

func TestVerifierRejectsWorkspaceSymlinkTraversal(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "linked")); err != nil {
		t.Fatal(err)
	}
	task := Task{Success: Success{Files: map[string]string{"linked/answer.txt": "x"}}}
	if _, err := verifyTask(context.Background(), task, workspace); err == nil {
		t.Fatal("verifier followed workspace symlink")
	}
}

func TestGatewayEnforcesBudgetAndForgetsAuthorization(t *testing.T) {
	var authorization string
	upstream := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusOK)
	})
	gateway, err := startGateway(1, upstream, "")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	for index := 0; index < 2; index++ {
		request, _ := http.NewRequest(http.MethodPost, gateway.BaseURL()+"/chat/completions", nil)
		request.Header.Set("Authorization", "Bearer secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	attempts, exceeded := gateway.Counts()
	if attempts != 2 || !exceeded || authorization != "Bearer secret" {
		t.Fatalf("attempts = %d, exceeded = %t, authorization forwarded = %q", attempts, exceeded, authorization)
	}
}

func TestPermissionOutcomeSelectsRequestedPolicy(t *testing.T) {
	options := []acp.PermissionOption{
		{OptionID: "once", Kind: acp.PermissionOptionAllowOnce},
		{OptionID: "deny", Kind: acp.PermissionOptionRejectOnce},
	}
	if got := permissionOutcome(options, "allow"); got.OptionID != "once" {
		t.Fatalf("allow outcome = %#v", got)
	}
	if got := permissionOutcome(options, "deny"); got.OptionID != "deny" {
		t.Fatalf("deny outcome = %#v", got)
	}
	if got := permissionOutcome(nil, "allow"); got.Outcome != "cancelled" {
		t.Fatalf("empty outcome = %#v", got)
	}
}

func TestSanitizedEnvironmentRemovesCredentials(t *testing.T) {
	t.Setenv("EXAMPLE_API_KEY", "secret-sentinel")
	t.Setenv("OX_EXAMPLE", "ox-sentinel")
	joined := strings.Join(sanitizedEnvironment(), "\n")
	if strings.Contains(joined, "secret-sentinel") || strings.Contains(joined, "ox-sentinel") {
		t.Fatalf("sanitized environment retained a credential: %s", joined)
	}
}

func TestOnlyLoopbackProviderEndpointsAreLocal(t *testing.T) {
	for endpoint, want := range map[string]bool{
		"http://127.0.0.1:8080/api/v1": true,
		"http://[::1]:8080/api/v1":     true,
		"http://localhost:8080/api/v1": true,
		"https://openrouter.ai/api/v1": false,
		"not-a-url":                    false,
	} {
		if got := localProviderURL(endpoint); got != want {
			t.Errorf("localProviderURL(%q) = %t, want %t", endpoint, got, want)
		}
	}
}

func TestRunResultPreservesUnknownUsageAsNull(t *testing.T) {
	raw, err := json.Marshal(RunResult{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"input_tokens":null`, `"output_tokens":null`, `"cost_usd":null`} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("result = %s, missing %s", raw, field)
		}
	}
}

func writeTestTask(t *testing.T, root string, task Task) {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "task.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
