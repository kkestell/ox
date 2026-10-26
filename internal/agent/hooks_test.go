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

// toolMessage returns the request's tool result and the message after it.
func toolMessage(t *testing.T, body map[string]any) (result string, next map[string]any) {
	t.Helper()
	messages := body["messages"].([]any)
	for i, message := range messages {
		if message := message.(map[string]any); message["role"] == "tool" {
			if i+1 < len(messages) {
				next = messages[i+1].(map[string]any)
			}
			return message["content"].(string), next
		}
	}
	t.Fatal("the request has no tool result")
	return "", nil
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
	want := "Context added by the user's hooks:\n\nMessage from the operator"
	read, next := toolMessage(t, f.server.Bodies()[1])
	if !strings.HasPrefix(read, "Exit code: 0") || strings.Contains(read, "operator") {
		t.Errorf("the tool result read %q, want it without the context", read)
	}
	if next["role"] != "user" || next["content"] != want {
		t.Errorf("the message after the tool result is %v, want the context as a user message", next)
	}
	saved := f.saved(t)[1].(*transcript.AssistantBatch)
	if saved.Outcomes[0].Text != read || len(saved.HookContext) != 1 || saved.HookContext[0] != "Message from the operator" {
		t.Errorf("saved %+v", saved)
	}
	var sent []HookContext
	for _, event := range client.events {
		if context, ok := event.(HookContext); ok {
			sent = append(sent, context)
		}
	}
	if len(sent) != 1 || string(sent[0]) != want {
		t.Errorf("the client was sent %q", sent)
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
	if _, next := toolMessage(t, f.server.Bodies()[1]); next["content"] != "Context added by the user's hooks:\n\nstill delivered" {
		t.Errorf("the message after the tool result is %v", next)
	}
	want := `ox: PostToolUse hook "echo broken >&2; exit 3": Exit code: 3; stderr: broken
ox: PostToolUse hook "echo not json": output is not a JSON object: invalid character 'o' in literal null (expecting 'u')
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

// messageAfter returns the request's message after the user message text.
func messageAfter(t *testing.T, body map[string]any, text string) map[string]any {
	t.Helper()
	messages := body["messages"].([]any)
	for i, message := range messages {
		if message := message.(map[string]any); message["role"] == "user" && message["content"] == text && i+1 < len(messages) {
			return messages[i+1].(map[string]any)
		}
	}
	t.Fatalf("the request has no message after %q", text)
	return nil
}

func TestPromptHooksAddContextToTheTurn(t *testing.T) {
	f := newFixture(t, fake.Text("Done."), fake.Text("Again."))
	f.agent.Hooks = settings.Hooks{
		SessionStart:     []settings.Hook{{Command: "cat > start.json; " + hookReply("Facts"), Timeout: 10 * time.Second}},
		UserPromptSubmit: []settings.Hook{{Command: "cat > prompt.json; " + hookReply("Messages"), Timeout: 10 * time.Second}},
	}
	first := f.input("Hello")
	first.SessionStart = true
	client := &recorder{mode: transcript.ModeAuto}
	if result, err := f.run(t, context.Background(), first, client); err != nil || result.Answer != "Done." {
		t.Fatalf("result %+v, %v", result, err)
	}
	want := "Context added by the user's hooks:\n\nFacts\n\nMessages"
	if next := messageAfter(t, f.server.Bodies()[0], "Hello"); next["role"] != "user" || next["content"] != want {
		t.Errorf("the message after the prompt is %v", next)
	}
	if sent, ok := client.events[1].(HookContext); !ok || string(sent) != want {
		t.Errorf("the client was sent %v after the session info", client.events[1])
	}
	for file, want := range map[string]string{"start.json": `"source":"startup"`, "prompt.json": `"prompt":"Hello"`} {
		data, _ := os.ReadFile(filepath.Join(f.workspace, file))
		if !strings.Contains(string(data), want) || !strings.Contains(string(data), `"session_id":"`+f.session+`"`) {
			t.Errorf("%s = %s", file, data)
		}
	}

	if result, err := f.run(t, context.Background(), f.input("More"), &recorder{mode: transcript.ModeAuto}); err != nil || result.Answer != "Again." {
		t.Fatalf("result %+v, %v", result, err)
	}
	saved := f.saved(t)
	if got := saved[0].(*transcript.TurnStart).HookContext; strings.Join(got, "|") != "Facts|Messages" {
		t.Errorf("first turn saved context %q", got)
	}
	if got := saved[2].(*transcript.TurnStart).HookContext; strings.Join(got, "|") != "Messages" {
		t.Errorf("a later turn ran SessionStart hooks: %q", got)
	}
}

func TestStopHookContinuesTheTurn(t *testing.T) {
	f := newFixture(t, fake.Text("First."), fake.Text("Second."))
	f.agent.Hooks = settings.Hooks{Stop: []settings.Hook{{
		Command: `cat >> stop.json; echo >> stop.json; [ -e blocked ] || { touch blocked; printf '{"decision":"block","reason":"New message"}'; }`,
		Timeout: 10 * time.Second,
	}}}
	result, err := f.run(t, context.Background(), f.input("Hello"), &recorder{mode: transcript.ModeAuto})
	if err != nil || result.Stop != EndTurn || result.Answer != "Second." {
		t.Fatalf("result %+v, %v", result, err)
	}
	bodies := f.server.Bodies()
	if len(bodies) != 2 {
		t.Fatalf("%d model requests, want 2", len(bodies))
	}
	messages := bodies[1]["messages"].([]any)
	if last := messages[len(messages)-1].(map[string]any); last["role"] != "user" || last["content"] != "Context added by the user's hooks:\n\nNew message" {
		t.Errorf("the second request ends with %v", last)
	}
	saved := f.saved(t)
	if got := saved[1].(*transcript.AssistantBatch).HookContext; strings.Join(got, "|") != "New message" {
		t.Errorf("the first answer saved context %q", got)
	}
	data, _ := os.ReadFile(filepath.Join(f.workspace, "stop.json"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"stop_hook_active":false`) || !strings.Contains(lines[1], `"stop_hook_active":true`) {
		t.Errorf("Stop hook inputs = %s", data)
	}
}
