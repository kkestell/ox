package agent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	fake "ox/internal/openroutertest"
	"ox/internal/settings"
	"ox/internal/transcript"
)

// hookReply is the output of a hook that adds context.
func hookReply(context string) string {
	data, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PostToolUse", "additionalContext": context}})
	return "printf '%s' '" + string(data) + "'"
}

// captureStderr returns what run writes to os.Stderr.
func captureStderr(t *testing.T, run func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	read := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		read <- string(data)
	}()
	defer func() { os.Stderr = saved }()
	run()
	w.Close()
	return <-read
}

func toolMessage(t *testing.T, body map[string]any) string {
	t.Helper()
	for _, message := range body["messages"].([]any) {
		if message := message.(map[string]any); message["role"] == "tool" {
			return message["content"].(string)
		}
	}
	t.Fatal("the request has no tool result")
	return ""
}

func TestHookContextReachesTheNextModelRequest(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret")
	t.Setenv("OX_HOOK_TEST", "inherited")
	f := newFixture(t, fake.Shell("printf one"), fake.Text("Done."))
	f.agent.Hooks = settings.Hooks{PostToolUse: []settings.Hook{
		{Command: `cat > input.json; { pwd; printenv OX_HOOK_TEST; printenv OPENROUTER_API_KEY || echo unset; } > env.txt; ` + hookReply("Message from the operator"), Timeout: 10 * time.Second},
		{Matcher: regexp.MustCompile("^(?:apply_patch)$"), Command: "touch unmatched", Timeout: 10 * time.Second},
	}}
	// One approval: the hooks themselves need none.
	client := &recorder{mode: transcript.ModeAsk, answers: []bool{true}}
	result, err := f.run(t, context.Background(), f.input("Run it"), client)
	if err != nil || result.Answer != "Done." {
		t.Fatalf("result %+v, %v", result, err)
	}
	read := toolMessage(t, f.server.Bodies()[1])
	if !strings.HasPrefix(read, "Exit code: 0") || !strings.HasSuffix(read, "(empty)\n\nPostToolUse hook additional context:\nMessage from the operator") {
		t.Errorf("the model read %q, want the result followed by the context", read)
	}
	saved := f.saved(t)[1].(*transcript.AssistantBatch).Outcomes[0]
	if saved.Text != read {
		t.Errorf("saved %q", saved.Text)
	}
	for _, event := range client.events {
		if finished, ok := event.(ToolFinished); ok && finished.Outcome.Text != saved.Text {
			t.Errorf("the client was sent %q", finished.Outcome.Text)
		}
	}
	var input map[string]any
	data, _ := os.ReadFile(filepath.Join(f.workspace, "input.json"))
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatalf("hook input %q: %v", data, err)
	}
	response := input["tool_response"].(map[string]any)
	if input["session_id"] != f.session || input["cwd"] != f.workspace || input["hook_event_name"] != "PostToolUse" ||
		input["tool_name"] != "shell" || input["tool_use_id"] != "shell-0" || input["tool_input"].(map[string]any)["command"] != "printf one" ||
		response["status"] != "completed" || !strings.Contains(response["text"].(string), "one") || strings.Contains(response["text"].(string), "operator") {
		t.Errorf("hook input = %s", data)
	}
	workspace, _ := filepath.EvalSymlinks(f.workspace)
	if env, _ := os.ReadFile(filepath.Join(f.workspace, "env.txt")); string(env) != workspace+"\ninherited\nunset\n" {
		t.Errorf("hook environment = %q", env)
	}
	if _, err := os.Stat(filepath.Join(f.workspace, "unmatched")); err == nil {
		t.Error("a hook ran for a tool its matcher does not match")
	}
}

func TestFailedHooksAreReportedAndIgnored(t *testing.T) {
	f := newFixture(t, fake.Shell("printf one"), fake.Text("Done."))
	f.agent.Hooks = settings.Hooks{PostToolUse: []settings.Hook{
		{Command: "echo broken >&2; exit 3", Timeout: 10 * time.Second},
		{Command: "echo not json", Timeout: 10 * time.Second},
		{Command: "sleep 30", Timeout: 200 * time.Millisecond},
		{Command: "true", Timeout: 10 * time.Second},
		{Command: hookReply("still delivered"), Timeout: 10 * time.Second},
	}}
	var result Result
	var err error
	started := time.Now()
	stderr := captureStderr(t, func() {
		result, err = f.run(t, context.Background(), f.input("Run it"), &recorder{mode: transcript.ModeAuto})
	})
	if err != nil || result.Answer != "Done." {
		t.Fatalf("result %+v, %v", result, err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("the timed-out hook was not stopped: %s", elapsed)
	}
	if got := toolMessage(t, f.server.Bodies()[1]); !strings.HasSuffix(got, "\n\nPostToolUse hook additional context:\nstill delivered") || strings.Count(got, "PostToolUse") != 1 {
		t.Errorf("the model read %q", got)
	}
	want := `ox: PostToolUse hook "echo broken >&2; exit 3": Exit code: 3; stderr: broken
ox: PostToolUse hook "echo not json": output is not a JSON object with hookSpecificOutput: invalid character 'o' in literal null (expecting 'u')
ox: PostToolUse hook "sleep 30": timed out after 200ms
`
	if stderr != want {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestCancellationStopsARunningHook(t *testing.T) {
	f := newFixture(t, fake.Shell("printf one"))
	f.agent.Hooks = settings.Hooks{PostToolUse: []settings.Hook{
		{Command: "touch started; sleep 30", Timeout: time.Minute},
		{Command: "touch later", Timeout: time.Minute},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for !exists(filepath.Join(f.workspace, "started")) {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	var result Result
	var err error
	started := time.Now()
	captureStderr(t, func() {
		result, err = f.run(t, ctx, f.input("Run it"), &recorder{mode: transcript.ModeAuto})
	})
	if err != nil || result.Stop != Cancelled {
		t.Fatalf("result %+v, %v", result, err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("cancellation did not stop the hook: %s", elapsed)
	}
	if exists(filepath.Join(f.workspace, "later")) {
		t.Error("a hook ran after the turn was cancelled")
	}
	if got := outcomes(f.saved(t)); len(got) != 1 || got[0] != "completed: Exit code: 0" {
		t.Errorf("outcomes = %q", got)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
