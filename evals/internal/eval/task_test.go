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
		"schema":              {Schema: 2, ID: "task", Budget: Budget{1, 1}, Phases: []Phase{{Action: "prompt", Prompt: "x", Permission: "allow"}}, Success: Success{Files: map[string]string{"x": "x"}}},
		"budget":              {Schema: 1, ID: "task", Phases: []Phase{{Action: "prompt", Prompt: "x", Permission: "allow"}}, Success: Success{Files: map[string]string{"x": "x"}}},
		"path":                {Schema: 1, ID: "task", Budget: Budget{1, 1}, Phases: []Phase{{Action: "prompt", Prompt: "x", Permission: "allow"}}, Success: Success{Files: map[string]string{"../x": "x"}}},
		"permission":          {Schema: 1, ID: "task", Budget: Budget{1, 1}, Phases: []Phase{{Action: "prompt", Prompt: "x", Permission: "maybe"}}, Success: Success{Files: map[string]string{"x": "x"}}},
		"negative rejections": {Schema: 1, ID: "task", Budget: Budget{1, 1}, Phases: []Phase{{Action: "prompt", Prompt: "x", Permission: "deny"}}, Success: Success{Files: map[string]string{"x": "x"}, MinimumPermissionRejections: -1}},
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

func TestMutationFixtureIsValidatedAndAppliedOnlyAtPhase(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "note.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "mutation"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mutation", "note.txt"), []byte("changed outside Ox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestTask(t, root, Task{
		Schema: 1, ID: "stale", Budget: Budget{1, 1},
		Phases: []Phase{
			{Action: "prompt", Prompt: "read note", Permission: "allow"},
			{Action: "mutate", Overlay: "mutation"},
			{Action: "prompt", Prompt: "edit note", Permission: "allow"},
		},
		Success: Success{Files: map[string]string{"note.txt": "done\n"}},
	})
	task, err := LoadTask(root)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "run-workspace")
	if err := seedWorkspace(task, destination); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(destination, "note.txt"))
	if err != nil || string(before) != "before\n" {
		t.Fatalf("seed before mutation = %q, %v", before, err)
	}
	if err := copyOverlay(filepath.Join(root, task.Phases[1].Overlay), destination); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(destination, "note.txt"))
	if err != nil || string(after) != "changed outside Ox\n" {
		t.Fatalf("seed after mutation = %q, %v", after, err)
	}
}

func TestMutationFixtureRejectsMissingAndEarlyOverlay(t *testing.T) {
	for name, phases := range map[string][]Phase{
		"before prompt": {{Action: "mutate", Overlay: "mutation"}, {Action: "prompt", Prompt: "x", Permission: "allow"}},
		"missing":       {{Action: "prompt", Prompt: "x", Permission: "allow"}, {Action: "mutate", Overlay: "missing"}},
		"unsafe":        {{Action: "prompt", Prompt: "x", Permission: "allow"}, {Action: "mutate", Overlay: "../outside"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "mutation"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestTask(t, root, Task{
				Schema: 1, ID: "bad", Budget: Budget{1, 1}, Phases: phases,
				Success: Success{Files: map[string]string{"x": "x"}},
			})
			if _, err := LoadTask(root); err == nil {
				t.Fatal("invalid mutation fixture loaded")
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

func TestVerifierRejectsExpectedFileSymlink(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("expected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "answer.txt")); err != nil {
		t.Fatal(err)
	}
	task := Task{Success: Success{Files: map[string]string{"answer.txt": "expected\n"}}}
	if _, err := verifyTask(context.Background(), task, workspace); err == nil {
		t.Fatal("verifier accepted an expected-file symlink")
	}
}

func TestRunRejectsExistingRepetitionDirectory(t *testing.T) {
	taskRoot := t.TempDir()
	writeTestTask(t, taskRoot, Task{
		Schema: 1, ID: "task", Budget: Budget{TimeoutMS: 1000, ProviderRequests: 1},
		Phases:  []Phase{{Action: "prompt", Prompt: "finish", Permission: "allow"}},
		Success: Success{Files: map[string]string{"answer.txt": "expected\n"}},
	})
	output := t.TempDir()
	if err := os.Mkdir(filepath.Join(output, "run-001"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(context.Background(), Config{
		OxBinary: executable, Candidate: CandidateExact, TaskPath: taskRoot, OutputDir: output, Model: "test/model",
		Repetitions: 1, Upstream: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	})
	if err == nil || !strings.Contains(err.Error(), "fresh repetition directory") {
		t.Fatalf("Run error = %v, want existing repetition rejection", err)
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
	if !permissionRejected(options, permissionOutcome(options, "deny")) {
		t.Fatal("deny selection was not tracked as a rejection")
	}
	if permissionRejected(options, permissionOutcome(options, "allow")) {
		t.Fatal("allow selection was tracked as a rejection")
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
	for _, field := range []string{`"total_tokens":null`, `"input_tokens":null`, `"output_tokens":null`, `"cost_usd":null`} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("result = %s, missing %s", raw, field)
		}
	}
}

func TestUsageTracksCumulativePromptPhasesAndNewScopes(t *testing.T) {
	result := RunResult{UsageComplete: true}
	accumulateUsage(&result, acp.Usage{TotalTokens: 12, InputTokens: 9, OutputTokens: 3})
	accumulateUsage(&result, acp.Usage{TotalTokens: 20, InputTokens: 15, OutputTokens: 5})
	if result.TotalTokens == nil || *result.TotalTokens != 20 || *result.InputTokens != 15 || *result.OutputTokens != 5 {
		t.Fatalf("usage = %#v", result)
	}
	accumulateUsage(&result, acp.Usage{TotalTokens: 7, InputTokens: 5, OutputTokens: 2})
	if *result.TotalTokens != 27 || *result.InputTokens != 20 || *result.OutputTokens != 7 {
		t.Fatalf("new-scope usage = %#v", result)
	}
	clearUsage(&result)
	accumulateUsage(&result, acp.Usage{TotalTokens: 100, InputTokens: 90, OutputTokens: 10})
	if result.UsageComplete || result.TotalTokens != nil || result.InputTokens != nil || result.OutputTokens != nil {
		t.Fatalf("incomplete usage became complete: %#v", result)
	}
}

func TestBinaryDigestAndTraceMetrics(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ox")
	if err := os.WriteFile(binary, []byte("candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := fileDigest(binary)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:dda18a0e21ae47c53b4309434cbc02ae8bf764fa83a6defbb719431242722aa7" {
		t.Fatalf("digest = %q", digest)
	}
	private := t.TempDir()
	trace := strings.Join([]string{
		`{"type":"provider_request_started"}`,
		`{"type":"provider_request_started"}`,
		`{"type":"tool_completed","tool_name":"edit_file","outcome":"failed"}`,
		`{"type":"tool_completed","tool_name":"edit_file","outcome":"completed"}`,
		`{"type":"tool_completed","tool_name":"write_file","outcome":"failed"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(private, "trace-1.jsonl"), []byte(trace), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := logicalProviderRequests(private); got != 2 {
		t.Fatalf("logical provider requests = %d", got)
	}
	if got := failedEditAttempts(private); got != 1 {
		t.Fatalf("failed edit attempts = %d", got)
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
