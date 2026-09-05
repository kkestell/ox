//go:build evalsmoke

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var smokeOxBinary string

const fakeModelCatalog = `{"data":[{"id":"test/model","context_length":128000,"supported_parameters":["tools","temperature","max_tokens"]}]}`

func TestMain(m *testing.M) {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	module, err := filepath.Abs(filepath.Join(root, "..", "..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	buildDirectory, err := os.MkdirTemp("", "ox-eval-smoke-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	smokeOxBinary = filepath.Join(buildDirectory, "ox")
	build := exec.Command("go", "build", "-o", smokeOxBinary, "./cmd/ox")
	build.Dir = module
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build Ox: %v\n%s", err, output)
		_ = os.RemoveAll(buildDirectory)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(buildDirectory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}

func TestFakeProviderSmoke(t *testing.T) {
	taskRoot := t.TempDir()
	writeTestTask(t, taskRoot, Task{
		Schema: 1, ID: "smoke", Budget: Budget{TimeoutMS: 5000, ProviderRequests: 3},
		Phases:  []Phase{{Action: "prompt", Prompt: "write note.txt", Permission: "allow"}},
		Success: Success{Files: map[string]string{"note.txt": "hello\n"}},
	})
	responses := []string{
		sse(
			evToolCall("call-1", "write_file", `{"path":"note.txt","content":"hello\n"}`),
			evFinish("tool_calls"), evUsage(20, 4, 24),
		),
		sse(evText("done"), evFinish("stop"), evUsage(30, 2, 32)),
	}
	var mutex sync.Mutex
	upstream := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/models") {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(fakeModelCatalog))
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		if len(responses) == 0 {
			http.Error(writer, "unexpected provider request", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(responses[0]))
		responses = responses[1:]
	})
	output := filepath.Join(t.TempDir(), "artifacts")
	index, err := Run(context.Background(), Config{
		OxBinary: smokeOxBinary, OxRevision: "test-revision", Candidate: CandidateExact, TaskPath: taskRoot,
		OutputDir: output, Model: "test/model", Provider: "fake",
		Repetitions: 1, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Results) != 1 || !index.Results[0].Success {
		stderr, _ := os.ReadFile(filepath.Join(output, "run-001/private/stderr.log"))
		events, _ := os.ReadFile(filepath.Join(output, "run-001/private/events.jsonl"))
		trace, _ := os.ReadFile(filepath.Join(output, "run-001/private/trace-1.jsonl"))
		t.Fatalf("failure = %+v, result = %#v\nstderr=%s\nevents=%s\ntrace=%s", index.Results[0].Failure, index.Results, stderr, events, trace)
	}
	if index.Results[0].ProviderAttempts != 2 || index.Results[0].ProviderRetries != 0 || index.Results[0].Permissions != 1 {
		t.Fatalf("metrics = %#v", index.Results[0])
	}
	for _, path := range []string{
		"index.json", "run-001/result.json", "run-001/private/events.jsonl",
		"run-001/private/stderr.log", "run-001/private/trace-1.jsonl",
		"run-001/private/verifier.log", "run-001/workspace/note.txt",
	} {
		if _, err := os.Stat(filepath.Join(output, path)); err != nil {
			t.Errorf("artifact %s: %v", path, err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(output, "index.json")); err != nil || !json.Valid(raw) {
		t.Fatalf("index artifact is invalid: %v", err)
	}
	if err := filepath.WalkDir(output, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		raw, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(raw), "evaluation-placeholder") {
			t.Errorf("artifact %s contains the provider credential", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSmokeFailureClassifications(t *testing.T) {
	badBinary := filepath.Join(t.TempDir(), "invalid-ox")
	if err := os.WriteFile(badBinary, []byte("not an executable format"), 0o700); err != nil {
		t.Fatal(err)
	}
	final := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/models") {
			_, _ = writer.Write([]byte(fakeModelCatalog))
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(sse(evFinish("stop"))))
	})
	providerFailure := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/models") {
			final.ServeHTTP(writer, request)
			return
		}
		http.Error(writer, `{"error":{"message":"provider failed"}}`, http.StatusBadRequest)
	})
	hold := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/models") {
			final.ServeHTTP(writer, request)
			return
		}
		select {
		case <-request.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	})
	for name, test := range map[string]struct {
		binary   string
		upstream http.Handler
		timeout  int
		files    map[string]string
		want     string
	}{
		"setup":        {badBinary, final, 1000, map[string]string{"sentinel.txt": "ok\n"}, "setup"},
		"protocol":     {"/bin/echo", final, 1000, map[string]string{"sentinel.txt": "ok\n"}, "protocol"},
		"process exit": {"true", final, 1000, map[string]string{"sentinel.txt": "ok\n"}, "process_exit"},
		"provider":     {smokeOxBinary, providerFailure, 1000, map[string]string{"sentinel.txt": "ok\n"}, "provider"},
		"timeout":      {smokeOxBinary, hold, 100, map[string]string{"sentinel.txt": "ok\n"}, "timeout"},
		"verifier":     {smokeOxBinary, final, 1000, map[string]string{"missing.txt": "no\n"}, "verifier"},
	} {
		t.Run(name, func(t *testing.T) {
			taskRoot := t.TempDir()
			if err := os.MkdirAll(filepath.Join(taskRoot, "workspace"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(taskRoot, "workspace", "sentinel.txt"), []byte("ok\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			writeTestTask(t, taskRoot, Task{
				Schema: 1, ID: "failure", Budget: Budget{TimeoutMS: test.timeout, ProviderRequests: 2},
				Phases:  []Phase{{Action: "prompt", Prompt: "finish", Permission: "allow"}},
				Success: Success{Files: test.files},
			})
			index, err := Run(context.Background(), Config{
				OxBinary: test.binary, OxRevision: "test", Candidate: CandidateExact, TaskPath: taskRoot,
				OutputDir: filepath.Join(t.TempDir(), "artifacts"), Model: "test/model",
				Provider: "fake", Repetitions: 1, Upstream: test.upstream,
			})
			if err != nil {
				t.Fatal(err)
			}
			failure := index.Results[0].Failure
			if failure == nil || failure.Class != test.want {
				t.Fatalf("failure = %#v, want %q", failure, test.want)
			}
		})
	}
}

func TestFakeProviderRestart(t *testing.T) {
	taskRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(taskRoot, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskRoot, "workspace", "sentinel.txt"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestTask(t, taskRoot, Task{
		Schema: 1, ID: "restart", Budget: Budget{TimeoutMS: 5000, ProviderRequests: 3},
		Phases: []Phase{
			{Action: "prompt", Prompt: "remember", Permission: "allow"},
			{Action: "restart"},
			{Action: "prompt", Prompt: "continue", Permission: "allow"},
		},
		Success: Success{Files: map[string]string{"sentinel.txt": "ok\n"}},
	})
	responses := []string{
		sse(evText("first"), evFinish("stop")),
		sse(evText("second"), evFinish("stop")),
	}
	var mutex sync.Mutex
	upstream := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/models") {
			_, _ = writer.Write([]byte(fakeModelCatalog))
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(responses[0]))
		responses = responses[1:]
	})
	output := filepath.Join(t.TempDir(), "artifacts")
	index, err := Run(context.Background(), Config{
		OxBinary: smokeOxBinary, OxRevision: "test", Candidate: CandidateExact, TaskPath: taskRoot,
		OutputDir: output, Model: "test/model", Provider: "fake",
		Repetitions: 1, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !index.Results[0].Success || index.Results[0].ProviderAttempts != 2 {
		t.Fatalf("result = %#v", index.Results[0])
	}
	if _, err := os.Stat(filepath.Join(output, "run-001/private/trace-2.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestFakeProviderMutationAndMultiPhaseMetrics(t *testing.T) {
	taskRoot := t.TempDir()
	for _, directory := range []string{"workspace", "mutation"} {
		if err := os.MkdirAll(filepath.Join(taskRoot, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(taskRoot, "workspace", "note.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskRoot, "mutation", "note.txt"), []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestTask(t, taskRoot, Task{
		Schema: 1, ID: "mutation", Budget: Budget{TimeoutMS: 5000, ProviderRequests: 6},
		Phases: []Phase{
			{Action: "prompt", Prompt: "read note.txt", Permission: "allow"},
			{Action: "mutate", Overlay: "mutation"},
			{Action: "prompt", Prompt: "change note.txt to after", Permission: "allow"},
		},
		Success: Success{Files: map[string]string{"note.txt": "after\n"}},
	})
	responses := []string{
		sse(evToolCall("read", "read_file", `{"path":"note.txt"}`), evFinish("tool_calls"), evUsage(10, 2, 12)),
		sse(evText("read"), evFinish("stop"), evUsage(11, 1, 12)),
		sse(evToolCall("stale", "edit_file", `{"path":"note.txt","old_string":"before\n","new_string":"after\n"}`), evFinish("tool_calls"), evUsage(12, 2, 14)),
		sse(evToolCall("reread", "read_file", `{"path":"note.txt"}`), evFinish("tool_calls"), evUsage(13, 2, 15)),
		sse(evToolCall("retry", "edit_file", `{"path":"note.txt","old_string":"outside\n","new_string":"after\n"}`), evFinish("tool_calls"), evUsage(14, 2, 16)),
		sse(evText("done"), evFinish("stop"), evUsage(15, 1, 16)),
	}
	var mutex sync.Mutex
	upstream := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/models") {
			_, _ = writer.Write([]byte(fakeModelCatalog))
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(responses[0]))
		responses = responses[1:]
	})
	index, err := Run(context.Background(), Config{
		OxBinary: smokeOxBinary, OxRevision: "test", Candidate: CandidateExact,
		TaskPath: taskRoot, OutputDir: filepath.Join(t.TempDir(), "artifacts"),
		Model: "test/model", Provider: "fake", Repetitions: 1, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := index.Results[0]
	var totalTokens uint64
	if result.TotalTokens != nil {
		totalTokens = *result.TotalTokens
	}
	if !result.Success || !result.UsageComplete || result.TotalTokens == nil || *result.TotalTokens != 85 ||
		result.ProviderAttempts != 6 || result.ProviderRetries != 0 || result.FailedEditAttempts != 1 {
		t.Fatalf("result = %#v, total tokens = %d", result, totalTokens)
	}
	if index.Candidate != CandidateExact || !strings.HasPrefix(index.BinaryDigest, "sha256:") || index.PromptDigest == "" {
		t.Fatalf("index identity = %#v", index)
	}
}

func TestFakeProviderRequiresPermissionRejection(t *testing.T) {
	for name, test := range map[string]struct {
		responses []string
		success   bool
		rejected  int
	}{
		"rejected request": {
			responses: []string{
				sse(evToolCall("call-1", "write_file", `{"path":"protected.txt","content":"changed\n"}`), evFinish("tool_calls")),
				sse(evText("denied"), evFinish("stop")),
			},
			success: true, rejected: 1,
		},
		"no request": {responses: []string{sse(evText("done"), evFinish("stop"))}},
	} {
		t.Run(name, func(t *testing.T) {
			taskRoot := t.TempDir()
			if err := os.MkdirAll(filepath.Join(taskRoot, "workspace"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(taskRoot, "workspace", "protected.txt"), []byte("keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			writeTestTask(t, taskRoot, Task{
				Schema: 1, ID: "permission", Budget: Budget{TimeoutMS: 5000, ProviderRequests: 2},
				Phases: []Phase{{Action: "prompt", Prompt: "finish", Permission: "deny"}},
				Success: Success{
					Files: map[string]string{"protected.txt": "keep\n"}, MinimumPermissionRejections: 1,
				},
			})
			response := 0
			upstream := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.HasSuffix(request.URL.Path, "/models") {
					_, _ = writer.Write([]byte(fakeModelCatalog))
					return
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte(test.responses[response]))
				response++
			})
			index, err := Run(context.Background(), Config{
				OxBinary: smokeOxBinary, Candidate: CandidateExact, TaskPath: taskRoot, OutputDir: filepath.Join(t.TempDir(), "artifacts"),
				Model: "test/model", Provider: "fake", Repetitions: 1, Upstream: upstream,
			})
			if err != nil {
				t.Fatal(err)
			}
			result := index.Results[0]
			if result.Success != test.success || result.PermissionRejections != test.rejected {
				t.Fatalf("result = %#v, want success %t and %d rejection(s)", result, test.success, test.rejected)
			}
			if !test.success && (result.Failure == nil || result.Failure.Class != "verifier") {
				t.Fatalf("failure = %#v, want verifier failure", result.Failure)
			}
		})
	}
}

func TestFailureClassification(t *testing.T) {
	deadline, cancel := context.WithCancel(context.Background())
	cancel()
	for name, test := range map[string]struct {
		ctx  context.Context
		err  error
		want string
	}{
		"timeout":  {deadline, context.DeadlineExceeded, "timeout"},
		"provider": {context.Background(), fmt.Errorf("OpenRouter unavailable"), "provider"},
		"process":  {context.Background(), fmt.Errorf("read: EOF"), "process_exit"},
		"protocol": {context.Background(), fmt.Errorf("bad response"), "protocol"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := classifyError(test.ctx, test.err); got != test.want {
				t.Fatalf("classification = %q, want %q", got, test.want)
			}
		})
	}
}

func sse(chunks ...string) string {
	var result strings.Builder
	for _, chunk := range chunks {
		result.WriteString("data: ")
		result.WriteString(chunk)
		result.WriteString("\n\n")
	}
	result.WriteString("data: [DONE]\n\n")
	return result.String()
}

func evToolCall(id, name, arguments string) string {
	idJSON, _ := json.Marshal(id)
	nameJSON, _ := json.Marshal(name)
	argumentsJSON, _ := json.Marshal(arguments)
	return fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":%s,"type":"function","function":{"name":%s,"arguments":%s}}]}}]}`, idJSON, nameJSON, argumentsJSON)
}

func evText(text string) string {
	raw, _ := json.Marshal(text)
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%s}}]}`, raw)
}

func evFinish(reason string) string {
	raw, _ := json.Marshal(reason)
	return fmt.Sprintf(`{"choices":[{"delta":{},"finish_reason":%s}]}`, raw)
}

func evUsage(input, output, total int) string {
	return fmt.Sprintf(`{"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,"cost":0.001}}`, input, output, total)
}
