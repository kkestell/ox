package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/workspace"
)

func testInvocation(t *testing.T, arguments string) agent.Invocation {
	t.Helper()
	root, err := workspace.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return agent.Invocation{
		Arguments: json.RawMessage(arguments),
		SessionID: "test-session",
		Root:      root,
		SpillDir:  filepath.Join(t.TempDir(), "session.spill"),
		CallID:    "call-1",
		Emit:      func(string) {},
	}
}

func writeToolFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func toolNamed(t *testing.T, name string) agent.Tool {
	t.Helper()
	for _, tool := range All() {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not registered", name)
	return agent.Tool{}
}

func invoke(t *testing.T, tool agent.Tool, invocation agent.Invocation) (string, error) {
	t.Helper()
	return tool.Execute(context.Background(), invocation)
}

func TestAllDeclaresValidSchemasAndClassifications(t *testing.T) {
	tools := All()
	if len(tools) != 24 {
		t.Fatalf("tool count = %d", len(tools))
	}
	for _, tool := range tools {
		if !json.Valid(tool.InputSchema) {
			t.Errorf("%s schema is invalid: %s", tool.Name, tool.InputSchema)
		}
		if tool.Kind == "" || tool.Execute == nil {
			t.Errorf("%s declaration = %+v", tool.Name, tool)
		}
		mutating := tool.Name == "write_file" || tool.Name == "edit_file"
		shell := tool.Name == "shell"
		todo := tool.Name == "todo"
		question := tool.Name == "question"
		webFetch := tool.Name == "web_fetch"
		memorySearch := tool.Name == "memory_search"
		memoryMutation := tool.Name == "memory_write" || tool.Name == "memory_delete"
		subagent := strings.HasPrefix(tool.Name, "subagent_")
		language := strings.HasPrefix(tool.Name, "lsp_")
		if mutating && (tool.Kind != acp.ToolKindEdit ||
			tool.Approval != agent.ApprovalAsk || tool.ParallelSafe) {
			t.Errorf("%s mutation classification = %+v", tool.Name, tool)
		}
		if shell && (tool.Kind != acp.ToolKindExecute ||
			tool.Approval != agent.ApprovalAsk || tool.ParallelSafe ||
			tool.Suggest == nil || tool.Covered == nil) {
			t.Errorf("shell classification = %+v", tool)
		}
		if todo && (tool.Kind != acp.ToolKindOther ||
			tool.Approval != agent.ApprovalNone || tool.ParallelSafe ||
			!tool.PlanMode) {
			t.Errorf("todo classification = %+v", tool)
		}
		if question && (tool.Kind != acp.ToolKindOther ||
			tool.Approval != agent.ApprovalNone || tool.ParallelSafe ||
			!tool.PlanMode || !tool.RequiresForm) {
			t.Errorf("question classification = %+v", tool)
		}
		if webFetch && (tool.Kind != acp.ToolKindSearch ||
			tool.Approval != agent.ApprovalAsk || !tool.ParallelSafe || !tool.PlanMode) {
			t.Errorf("web_fetch classification = %+v", tool)
		}
		if memorySearch && (tool.Kind != acp.ToolKindSearch ||
			tool.Approval != agent.ApprovalNone || !tool.ParallelSafe || !tool.PlanMode) {
			t.Errorf("memory_search classification = %+v", tool)
		}
		if memoryMutation && (tool.Kind != acp.ToolKindOther ||
			tool.Approval != agent.ApprovalAsk || tool.ParallelSafe || tool.PlanMode) {
			t.Errorf("%s classification = %+v", tool.Name, tool)
		}
		if subagent && (tool.Kind != acp.ToolKindOther || tool.Approval != agent.ApprovalNone ||
			!tool.PlanMode || tool.Scope == agent.ToolScopeAll) {
			t.Errorf("%s classification = %+v", tool.Name, tool)
		}
		// Language queries read without side effects, so plan mode keeps them.
		if language && (tool.Approval != agent.ApprovalNone || !tool.ParallelSafe ||
			!tool.PlanMode || tool.Scope != agent.ToolScopeAll) {
			t.Errorf("%s classification = %+v", tool.Name, tool)
		}
		if !mutating && !shell && !todo && !question && !webFetch && !subagent &&
			!memorySearch && !memoryMutation &&
			(!tool.ParallelSafe || tool.Approval != agent.ApprovalNone) {
			t.Errorf("%s read-only classification = %+v", tool.Name, tool)
		}
	}
}

func TestSubagentToolsDispatchStrictCoordinationCallbacks(t *testing.T) {
	started := false
	invocation := testInvocation(t, `{"name":" scout ","task":" inspect this "}`)
	invocation.StartSubagent = func(name, task string) (agent.SubagentSnapshot, error) {
		started = name == "scout" && task == "inspect this"
		return agent.SubagentSnapshot{ID: "child-1", Name: name, Status: "running"}, nil
	}
	output, err := invoke(t, toolNamed(t, "subagent_start"), invocation)
	if err != nil || !started || !strings.Contains(output, `"id":"child-1"`) {
		t.Fatalf("start output = %q, called = %t, error = %v", output, started, err)
	}

	invocation.Arguments = json.RawMessage(`{"id":" child-1 ","message":" follow up "}`)
	invocation.SendSubagent = func(id, message string) (agent.SubagentSnapshot, error) {
		if id != "child-1" || message != "follow up" {
			t.Fatalf("send = %q, %q", id, message)
		}
		return agent.SubagentSnapshot{ID: id, Status: "running"}, nil
	}
	if _, err := invoke(t, toolNamed(t, "subagent_send"), invocation); err != nil {
		t.Fatal(err)
	}

	invocation.Arguments = json.RawMessage(`{"ids":["child-1"]}`)
	invocation.WaitSubagents = func(_ context.Context, ids []string) ([]agent.SubagentSnapshot, error) {
		if !reflect.DeepEqual(ids, []string{"child-1"}) {
			t.Fatalf("wait IDs = %#v", ids)
		}
		return []agent.SubagentSnapshot{{ID: "child-1", Status: "completed", Result: "done"}}, nil
	}
	output, err = invoke(t, toolNamed(t, "subagent_wait"), invocation)
	if err != nil || !strings.Contains(output, `"result":"done"`) {
		t.Fatalf("wait output = %q, error = %v", output, err)
	}

	invocation.Arguments = json.RawMessage(`{"message":" finding "}`)
	reported := ""
	invocation.ReportToParent = func(message string) error { reported = message; return nil }
	if _, err := invoke(t, toolNamed(t, "subagent_report"), invocation); err != nil || reported != "finding" {
		t.Fatalf("report = %q, error = %v", reported, err)
	}
}

func TestSubagentToolsRejectMalformedArgumentsAndUnavailableScope(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments string
	}{
		{name: "subagent_start", arguments: `{"name":"","task":"work"}`},
		{name: "subagent_start", arguments: `{"name":"child","task":"work","extra":true}`},
		{name: "subagent_send", arguments: `{"id":"child","message":" "}`},
		{name: "subagent_stop", arguments: `{}`},
		{name: "subagent_wait", arguments: `{"ids":[""]}`},
		{name: "subagent_report", arguments: `{"message":"ok"}`},
	} {
		t.Run(test.name+test.arguments, func(t *testing.T) {
			invocation := testInvocation(t, test.arguments)
			if _, err := invoke(t, toolNamed(t, test.name), invocation); err == nil {
				t.Fatal("invalid arguments were accepted")
			}
		})
	}
}

func TestQuestionBuildsFormAndReturnsDistinctOutcomes(t *testing.T) {
	for _, test := range []struct {
		name     string
		action   acp.ElicitationAction
		content  map[string]json.RawMessage
		want     string
		validate func(*testing.T, acp.CreateElicitationRequest)
	}{
		{
			name: "accepted choice", action: acp.ElicitationActionAccept,
			content: map[string]json.RawMessage{"answer": json.RawMessage(`"Balanced"`)},
			want:    `{"outcome":"accepted","answer":"Balanced"}`,
			validate: func(t *testing.T, request acp.CreateElicitationRequest) {
				property := request.RequestedSchema.Properties["answer"]
				if request.SessionID != "test-session" || request.ToolCallID != "call-1" ||
					request.Mode != acp.ElicitationModeForm || request.Message != "How?" ||
					property.Default == nil || *property.Default != "Balanced" ||
					len(property.OneOf) != 2 || property.OneOf[0].Const != "Safe" ||
					property.OneOf[0].Description != "Small changes" {
					t.Fatalf("request = %#v", request)
				}
			},
		},
		{name: "declined", action: acp.ElicitationActionDecline, want: `{"outcome":"declined"}`},
		{name: "cancelled", action: acp.ElicitationActionCancel, want: `{"outcome":"cancelled"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			invocation := testInvocation(t, `{"question":" How? ","options":[{"label":" Safe ","description":" Small changes "},{"label":"Balanced"}],"default":" Balanced "}`)
			invocation.AskQuestion = func(
				_ context.Context,
				request acp.CreateElicitationRequest,
			) (acp.CreateElicitationResponse, error) {
				if err := request.Validate(); err != nil {
					t.Fatal(err)
				}
				if test.validate != nil {
					test.validate(t, request)
				}
				return acp.CreateElicitationResponse{Action: test.action, Content: test.content}, nil
			}
			output, err := invoke(t, toolNamed(t, "question"), invocation)
			if err != nil || output != test.want {
				t.Fatalf("output = %q, error = %v", output, err)
			}
		})
	}
}

func TestQuestionFreeTextDefaultIsNotAnAnswer(t *testing.T) {
	invocation := testInvocation(t, `{"question":"Name?","default":"Ox"}`)
	invocation.AskQuestion = func(
		_ context.Context,
		request acp.CreateElicitationRequest,
	) (acp.CreateElicitationResponse, error) {
		property := request.RequestedSchema.Properties["answer"]
		if property.MinLength == nil || *property.MinLength != 1 ||
			property.Default == nil || *property.Default != "Ox" {
			t.Fatalf("property = %#v", property)
		}
		return acp.CreateElicitationResponse{Action: acp.ElicitationActionAccept}, nil
	}
	_, err := invoke(t, toolNamed(t, "question"), invocation)
	if err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("error = %v", err)
	}
}

func TestQuestionRejectsInvalidArgumentsAndResponses(t *testing.T) {
	tests := []struct {
		arguments string
		response  acp.CreateElicitationResponse
	}{
		{arguments: `{}`},
		{arguments: `{"question":" "}`},
		{arguments: `{"question":"Q?","options":null}`},
		{arguments: `{"question":"Q?","options":[]}`},
		{arguments: `{"question":"Q?","options":[{"label":" "}]}`},
		{arguments: `{"question":"Q?","options":[{"label":"A"},{"label":" A "}]}`},
		{arguments: `{"question":"Q?","options":[{"label":"A","extra":true}]}`},
		{arguments: `{"question":"Q?","options":[{"label":"A"}],"default":"B"}`},
		{arguments: `{"question":"Q?","default":" "}`},
		{arguments: `{"question":"Q?","extra":true}`},
		{arguments: `{"question":"Q?"}`, response: acp.CreateElicitationResponse{Action: "future"}},
	}
	for _, test := range tests {
		invocation := testInvocation(t, test.arguments)
		invocation.AskQuestion = func(
			context.Context,
			acp.CreateElicitationRequest,
		) (acp.CreateElicitationResponse, error) {
			return test.response, nil
		}
		if _, err := invoke(t, toolNamed(t, "question"), invocation); err == nil {
			t.Fatalf("arguments %s succeeded", test.arguments)
		}
	}

	invocation := testInvocation(t, `{"question":"Q?"}`)
	invocation.AskQuestion = func(context.Context, acp.CreateElicitationRequest) (acp.CreateElicitationResponse, error) {
		return acp.CreateElicitationResponse{}, errors.New("client failed")
	}
	if _, err := invoke(t, toolNamed(t, "question"), invocation); err == nil || err.Error() != "client failed" {
		t.Fatalf("callback error = %v", err)
	}
}

func TestSkillLoadsExactCatalogName(t *testing.T) {
	invocation := testInvocation(t, `{"name":"review"}`)
	invocation.LoadSkill = func(name string) (string, error) {
		if name != "review" {
			t.Fatalf("name = %q", name)
		}
		return "Review instructions.\n", nil
	}
	output, err := invoke(t, toolNamed(t, "skill"), invocation)
	if err != nil || output != "Review instructions.\n" {
		t.Fatalf("output = %q, error = %v", output, err)
	}
}

func TestSkillRejectsInvalidOrUnavailableLoads(t *testing.T) {
	for _, test := range []struct {
		arguments  string
		invocation func(*agent.Invocation)
		want       string
	}{
		{arguments: `{}`, want: "must be a string"},
		{arguments: `{"name":""}`, want: "must not be empty"},
		{arguments: `{"name":"missing"}`, want: "unavailable"},
		{arguments: `{"name":"review","extra":true}`, want: "unknown field"},
	} {
		invocation := testInvocation(t, test.arguments)
		if test.invocation != nil {
			test.invocation(&invocation)
		}
		_, err := invoke(t, toolNamed(t, "skill"), invocation)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("arguments %s: error = %v", test.arguments, err)
		}
	}
}

func TestTodoReplacesValidatedCompleteList(t *testing.T) {
	invocation := testInvocation(t, `{"todos":[{"content":"first","status":"pending"},{"content":"second","priority":"high","status":"in_progress"}]}`)
	var got []acp.PlanEntry
	invocation.ReplaceTodo = func(entries []acp.PlanEntry) error {
		got = entries
		return nil
	}
	output, err := invoke(t, toolNamed(t, "todo"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	want := []acp.PlanEntry{
		{Content: "first", Priority: acp.PlanEntryPriorityMedium, Status: acp.PlanEntryStatusPending},
		{Content: "second", Priority: acp.PlanEntryPriorityHigh, Status: acp.PlanEntryStatusInProgress},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replacement = %#v, want %#v", got, want)
	}
	if output != "[pending, medium] first\n[in_progress, high] second\n" {
		t.Fatalf("output = %q", output)
	}
}

func TestTodoClearsAndRejectsInvalidReplacements(t *testing.T) {
	called := false
	clear := testInvocation(t, `{"todos":[]}`)
	clear.ReplaceTodo = func(entries []acp.PlanEntry) error {
		called = true
		if entries == nil || len(entries) != 0 {
			t.Fatalf("clear entries = %#v", entries)
		}
		return nil
	}
	output, err := invoke(t, toolNamed(t, "todo"), clear)
	if err != nil || !called || output != "Todo list cleared" {
		t.Fatalf("clear = output %q, called %v, error %v", output, called, err)
	}

	for _, arguments := range []string{
		`{}`,
		`{"todos":[{"content":" ","status":"pending"}]}`,
		`{"todos":[{"content":"x","priority":"urgent","status":"pending"}]}`,
		`{"todos":[{"content":"x","status":"blocked"}]}`,
		`{"todos":[{"content":"x","status":"in_progress"},{"content":"y","status":"in_progress"}]}`,
	} {
		invocation := testInvocation(t, arguments)
		invocation.ReplaceTodo = func([]acp.PlanEntry) error {
			t.Fatalf("invalid replacement %s executed", arguments)
			return nil
		}
		if _, err := invoke(t, toolNamed(t, "todo"), invocation); err == nil {
			t.Fatalf("invalid replacement %s succeeded", arguments)
		}
	}
}

func TestMemoryToolsValidateAndRenderCallbacks(t *testing.T) {
	fact := agent.MemoryFact{
		ID: "0123456789abcdef0123456789abcdef", Type: "decision", Content: "Use JSON",
		SourceSession: "fedcba9876543210fedcba9876543210",
		CreatedAt:     time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
		ExpiresAt:     time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
	}
	search := testInvocation(t, `{"query":"json store"}`)
	search.SearchMemory = func(query string) ([]agent.MemoryFact, error) {
		if query != "json store" {
			t.Fatalf("query = %q", query)
		}
		return []agent.MemoryFact{fact}, nil
	}
	output, err := invoke(t, toolNamed(t, "memory_search"), search)
	if err != nil || !strings.Contains(output, `"content":"Use JSON"`) {
		t.Fatalf("search output = %q, error = %v", output, err)
	}

	write := testInvocation(t, `{"type":" decision ","content":" Use JSON ","supersedes":"0123456789abcdef0123456789abcdef"}`)
	write.WriteMemory = func(factType, content, supersedes string) (agent.MemoryFact, error) {
		if factType != "decision" || content != "Use JSON" || supersedes != fact.ID {
			t.Fatalf("write = %q, %q, %q", factType, content, supersedes)
		}
		fact.Supersedes = supersedes
		return fact, nil
	}
	output, err = invoke(t, toolNamed(t, "memory_write"), write)
	if err != nil || !strings.Contains(output, `"supersedes":"`+fact.ID+`"`) {
		t.Fatalf("write output = %q, error = %v", output, err)
	}

	deleted := ""
	remove := testInvocation(t, `{"id":"0123456789abcdef0123456789abcdef"}`)
	remove.DeleteMemory = func(id string) error {
		deleted = id
		return nil
	}
	output, err = invoke(t, toolNamed(t, "memory_delete"), remove)
	if err != nil || deleted != fact.ID || output != `{"deleted":"`+fact.ID+`"}` {
		t.Fatalf("delete output = %q, deleted = %q, error = %v", output, deleted, err)
	}
}

func TestMemoryToolsRejectInvalidOrUnavailableOperations(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments string
	}{
		{name: "memory_search", arguments: `{}`},
		{name: "memory_search", arguments: `{"query":"x","extra":true}`},
		{name: "memory_write", arguments: `{}`},
		{name: "memory_write", arguments: `{"type":"profile","content":"x"}`},
		{name: "memory_write", arguments: `{"type":"finding","content":" "}`},
		{name: "memory_write", arguments: `{"type":"finding","content":"x","supersedes":" "}`},
		{name: "memory_delete", arguments: `{}`},
		{name: "memory_delete", arguments: `{"id":" "}`},
	} {
		invocation := testInvocation(t, test.arguments)
		invocation.SearchMemory = func(string) ([]agent.MemoryFact, error) {
			t.Fatal("invalid search executed")
			return nil, nil
		}
		invocation.WriteMemory = func(string, string, string) (agent.MemoryFact, error) {
			t.Fatal("invalid write executed")
			return agent.MemoryFact{}, nil
		}
		invocation.DeleteMemory = func(string) error {
			t.Fatal("invalid delete executed")
			return nil
		}
		if _, err := invoke(t, toolNamed(t, test.name), invocation); err == nil {
			t.Fatalf("%s arguments %s succeeded", test.name, test.arguments)
		}
	}

	for _, name := range []string{"memory_search", "memory_write", "memory_delete"} {
		arguments := `{"query":""}`
		if name == "memory_write" {
			arguments = `{"type":"finding","content":"x"}`
		} else if name == "memory_delete" {
			arguments = `{"id":"0123456789abcdef0123456789abcdef"}`
		}
		if _, err := invoke(t, toolNamed(t, name), testInvocation(t, arguments)); err == nil {
			t.Fatalf("unavailable %s succeeded", name)
		}
	}
}

func TestShellReportsCombinedOutputAndExitCode(t *testing.T) {
	invocation := testInvocation(
		t,
		`{"command":"printf 'stdout\\n'; printf 'stderr\\n' >&2; exit 7"}`,
	)
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if result != "exit code: 7\nstdout\nstderr\n" {
		t.Fatalf("result = %q", result)
	}
}

func TestShellUsesWorkspaceAndDetachedStdin(t *testing.T) {
	invocation := testInvocation(
		t,
		`{"command":"pwd; if read value; then echo unexpected; else echo eof; fi"}`,
	)
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "\n"+invocation.Root+"\neof\n") {
		t.Fatalf("result = %q", result)
	}
}

func TestShellSanitizesEnvironment(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret")
	t.Setenv("OX_TEST_SECRET", "secret")
	t.Setenv("PAGER", "less")
	t.Setenv("TERM", "xterm")
	invocation := testInvocation(t, `{"command":"printf '%s|%s|%s|%s|%s' \"${OPENROUTER_API_KEY-unset}\" \"${OX_TEST_SECRET-unset}\" \"$PAGER\" \"$GIT_TERMINAL_PROMPT\" \"$TERM\""}`)
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if result != "exit code: 0\nunset|unset|cat|0|dumb" {
		t.Fatalf("result = %q", result)
	}
}

func TestShellValidatesArguments(t *testing.T) {
	tests := []string{
		`{}`,
		`{"command":""}`,
		`{"command":"true","timeout":0}`,
		`{"command":"true","timeout":601}`,
		`{"command":"true","timeout":"slow"}`,
		`{"command":"true","extra":1}`,
	}
	for _, arguments := range tests {
		invocation := testInvocation(t, arguments)
		if _, err := invoke(t, toolNamed(t, "shell"), invocation); err == nil {
			t.Errorf("arguments %s unexpectedly succeeded", arguments)
		}
	}
}

func TestShellTimeoutReturnsPartialOutput(t *testing.T) {
	invocation := testInvocation(
		t,
		`{"command":"printf 'before\\n'; sleep 30","timeout":1}`,
	)
	started := time.Now()
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "timed out after 1 seconds") ||
		!strings.Contains(result, "before\n") {
		t.Fatalf("result = %q", result)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("timeout took %s", time.Since(started))
	}
}

func TestShellCancellationKillsTheProcessGroup(t *testing.T) {
	invocation := testInvocation(
		t,
		`{"command":"sleep 300 & echo $!; wait"}`,
	)
	pids := make(chan int, 1)
	invocation.Emit = func(text string) {
		pid, err := strconv.Atoi(strings.TrimSpace(text))
		if err == nil {
			select {
			case pids <- pid:
			default:
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	shell := toolNamed(t, "shell")
	go func() {
		_, err := shell.Execute(ctx, invocation)
		result <- err
	}()
	var pid int
	select {
	case pid = <-pids:
	case <-time.After(5 * time.Second):
		t.Fatal("shell did not report its child pid")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shell did not stop after cancellation")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived cancellation", pid)
}

func TestShellSpillsLargeOutput(t *testing.T) {
	invocation := testInvocation(
		t,
		`{"command":"i=0; while [ $i -lt 10000 ]; do printf 'line %05d xxxxxxxxxxxxxxxxxxxx\\n' \"$i\"; i=$((i + 1)); done"}`,
	)
	var spill string
	invocation.ReportSpill = func(path string) { spill = path }
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if spill == "" || !strings.Contains(result, "full output at "+spill) ||
		!strings.Contains(result, "line 00000") ||
		!strings.Contains(result, "line 09999") {
		t.Fatalf("result = %q, spill = %q", result, spill)
	}
	content, err := os.ReadFile(spill)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(content), "\n"); lines != 10_000 {
		t.Fatalf("spill has %d lines", lines)
	}
}

// A validator that fails loudly reports its own status without the model
// piping it through head, tail, or grep: the recorder bounds the inline
// preview and keeps every line in the spill.
func TestShellSpillsLargeFailingOutputWithItsOwnStatus(t *testing.T) {
	invocation := testInvocation(
		t,
		`{"command":"i=0; while [ $i -lt 10000 ]; do printf 'check %05d ok\\n' \"$i\"; i=$((i + 1)); done; printf 'FAIL: 1 of 10000 checks failed\\n'; exit 3"}`,
	)
	var spill string
	invocation.ReportSpill = func(path string) { spill = path }
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, "exit code: 3\n") ||
		spill == "" ||
		!strings.Contains(result, "FAIL: 1 of 10000 checks failed") ||
		!strings.Contains(result, "full output at "+spill) {
		t.Fatalf("result = %q, spill = %q", result, spill)
	}
	content, err := os.ReadFile(spill)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if len(lines) != 10_001 || lines[len(lines)-1] != "FAIL: 1 of 10000 checks failed" {
		t.Fatalf("spill has %d lines, last = %q", len(lines), lines[len(lines)-1])
	}
}

func TestShellRuleSuggestionAndCoverage(t *testing.T) {
	tool := toolNamed(t, "shell")
	arguments := json.RawMessage(`{"command":"go test ./..."}`)
	if rule := tool.Suggest(arguments); rule != "go test" {
		t.Fatalf("suggestion = %q", rule)
	}
	if !tool.Covered([]string{"go test"}, json.RawMessage(`{"command":"go test ./internal/agent"}`)) {
		t.Fatal("go test rule did not cover a narrower invocation")
	}
	if tool.Covered([]string{"go test"}, json.RawMessage(`{"command":"rm -rf /"}`)) {
		t.Fatal("go test rule covered an unrelated command")
	}
}

type fakeTerminal struct {
	mu             sync.Mutex
	calls          []string
	createRequest  acp.CreateTerminalRequest
	waitResponse   acp.WaitForTerminalExitResponse
	outputResponse acp.TerminalOutputResponse
	fail           map[string]bool
	blockWait      bool
	waitStarted    chan struct{}
	waitOnce       sync.Once
}

func (f *fakeTerminal) operations() agent.ClientTerminal {
	return agent.ClientTerminal{
		Create: func(
			_ context.Context,
			request acp.CreateTerminalRequest,
		) (acp.CreateTerminalResponse, error) {
			f.record("create")
			f.createRequest = request
			if f.failed("create") {
				return acp.CreateTerminalResponse{}, errors.New("create failed")
			}
			return acp.CreateTerminalResponse{TerminalID: "terminal-1"}, nil
		},
		WaitForExit: func(
			ctx context.Context,
			_ acp.WaitForTerminalExitRequest,
		) (acp.WaitForTerminalExitResponse, error) {
			f.record("wait")
			if f.blockWait {
				f.waitOnce.Do(func() { close(f.waitStarted) })
				<-ctx.Done()
				return acp.WaitForTerminalExitResponse{}, ctx.Err()
			}
			if f.failed("wait") {
				return acp.WaitForTerminalExitResponse{}, errors.New("wait failed")
			}
			return f.waitResponse, nil
		},
		Output: func(
			_ context.Context,
			_ acp.TerminalOutputRequest,
		) (acp.TerminalOutputResponse, error) {
			f.record("output")
			if f.failed("output") {
				return acp.TerminalOutputResponse{}, errors.New("output failed")
			}
			return f.outputResponse, nil
		},
		Kill: func(_ context.Context, _ acp.KillTerminalRequest) error {
			f.record("kill")
			if f.failed("kill") {
				return errors.New("kill failed")
			}
			return nil
		},
		Release: func(_ context.Context, _ acp.ReleaseTerminalRequest) error {
			f.record("release")
			if f.failed("release") {
				return errors.New("release failed")
			}
			return nil
		},
	}
}

func (f *fakeTerminal) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeTerminal) failed(call string) bool {
	return f.fail != nil && f.fail[call]
}

func (f *fakeTerminal) recordedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func TestShellDelegatesCommandAndReportsExitAndTruncation(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret")
	t.Setenv("OX_TEST_SECRET", "secret")
	t.Setenv("TERM", "xterm")
	exitCode := 7
	terminal := &fakeTerminal{
		waitResponse: acp.WaitForTerminalExitResponse{ExitCode: &exitCode},
		outputResponse: acp.TerminalOutputResponse{
			Output:    "delegated output\n",
			Truncated: true,
		},
	}
	invocation := testInvocation(t, `{"command":"printf delegated"}`)
	invocation.Terminal = terminal.operations()

	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if result != "exit code: 7\n[client output truncated to the last 10485760 bytes]\ndelegated output\n" {
		t.Fatalf("result = %q", result)
	}
	if !reflect.DeepEqual(terminal.recordedCalls(), []string{"create", "wait", "output", "release"}) {
		t.Fatalf("terminal calls = %v", terminal.recordedCalls())
	}
	request := terminal.createRequest
	if request.SessionID != invocation.SessionID || request.Command != "/bin/sh" ||
		!reflect.DeepEqual(request.Args, []string{"-c", "printf delegated"}) ||
		request.CWD == nil || *request.CWD != invocation.Root ||
		request.OutputByteLimit == nil || *request.OutputByteLimit != workspace.CollectionLimitBytes {
		t.Fatalf("create request = %#v", request)
	}
	environment := make(map[string]string, len(request.Env))
	for _, variable := range request.Env {
		environment[variable.Name] = variable.Value
	}
	if _, ok := environment["OPENROUTER_API_KEY"]; ok {
		t.Fatal("delegated environment exposed OPENROUTER_API_KEY")
	}
	if _, ok := environment["OX_TEST_SECRET"]; ok {
		t.Fatal("delegated environment exposed OX_TEST_SECRET")
	}
	if environment["PAGER"] != "cat" || environment["GIT_TERMINAL_PROMPT"] != "0" ||
		environment["TERM"] != "dumb" || environment["NO_COLOR"] != "1" {
		t.Fatalf("delegated environment = %#v", environment)
	}
}

func TestShellDelegatedSignalAndEmptyOutput(t *testing.T) {
	signal := "SIGTERM"
	terminal := &fakeTerminal{
		waitResponse: acp.WaitForTerminalExitResponse{Signal: &signal},
	}
	invocation := testInvocation(t, `{"command":"stop"}`)
	invocation.Terminal = terminal.operations()
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if result != "signal: SIGTERM\n(no output)" {
		t.Fatalf("result = %q", result)
	}
}

func TestShellUsesLocalFallbackForIncompleteTerminal(t *testing.T) {
	invocation := testInvocation(t, `{"command":"printf local"}`)
	invocation.Terminal.Create = func(
		context.Context,
		acp.CreateTerminalRequest,
	) (acp.CreateTerminalResponse, error) {
		t.Fatal("incomplete terminal was selected")
		return acp.CreateTerminalResponse{}, nil
	}
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if result != "exit code: 0\nlocal" {
		t.Fatalf("result = %q", result)
	}
}

func TestShellDelegatedOutputUsesStreamBoundsAndSpill(t *testing.T) {
	zero := 0
	output := strings.Repeat("delegated line\n", 10_000)
	terminal := &fakeTerminal{
		waitResponse:   acp.WaitForTerminalExitResponse{ExitCode: &zero},
		outputResponse: acp.TerminalOutputResponse{Output: output},
	}
	invocation := testInvocation(t, `{"command":"large"}`)
	invocation.Terminal = terminal.operations()
	var spill string
	invocation.ReportSpill = func(path string) { spill = path }
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if spill == "" || !strings.Contains(result, "full output at "+spill) {
		t.Fatalf("result = %q, spill = %q", result, spill)
	}
	content, err := os.ReadFile(spill)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != output {
		t.Fatalf("spilled output length = %d, want %d", len(content), len(output))
	}
}

func TestShellDelegatedTimeoutKillsBeforeRelease(t *testing.T) {
	signal := "SIGKILL"
	terminal := &fakeTerminal{
		blockWait:   true,
		waitStarted: make(chan struct{}),
		outputResponse: acp.TerminalOutputResponse{
			Output:     "partial\n",
			ExitStatus: &acp.TerminalExitStatus{Signal: &signal},
		},
	}
	invocation := testInvocation(t, `{"command":"sleep 30","timeout":1}`)
	invocation.Terminal = terminal.operations()
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "timed out after 1 seconds") ||
		!strings.Contains(result, "signal: SIGKILL\npartial\n") {
		t.Fatalf("result = %q", result)
	}
	if !reflect.DeepEqual(
		terminal.recordedCalls(),
		[]string{"create", "wait", "kill", "output", "release"},
	) {
		t.Fatalf("terminal calls = %v", terminal.recordedCalls())
	}
}

func TestShellDelegatedCancellationKillsBeforeRelease(t *testing.T) {
	terminal := &fakeTerminal{blockWait: true, waitStarted: make(chan struct{})}
	invocation := testInvocation(t, `{"command":"sleep 30"}`)
	invocation.Terminal = terminal.operations()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	shell := toolNamed(t, "shell")
	go func() {
		_, err := shell.Execute(ctx, invocation)
		result <- err
	}()
	<-terminal.waitStarted
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if !reflect.DeepEqual(
		terminal.recordedCalls(),
		[]string{"create", "wait", "kill", "output", "release"},
	) {
		t.Fatalf("terminal calls = %v", terminal.recordedCalls())
	}
}

func TestShellDelegatedCallbackErrorsStillRelease(t *testing.T) {
	for _, test := range []struct {
		name      string
		fail      string
		wantCalls []string
	}{
		{name: "create", fail: "create", wantCalls: []string{"create"}},
		{name: "wait", fail: "wait", wantCalls: []string{"create", "wait", "release"}},
		{name: "output", fail: "output", wantCalls: []string{"create", "wait", "output", "release"}},
		{name: "release", fail: "release", wantCalls: []string{"create", "wait", "output", "release"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			zero := 0
			terminal := &fakeTerminal{
				waitResponse: acp.WaitForTerminalExitResponse{ExitCode: &zero},
				fail:         map[string]bool{test.fail: true},
			}
			invocation := testInvocation(t, `{"command":"true"}`)
			invocation.Terminal = terminal.operations()
			_, err := invoke(t, toolNamed(t, "shell"), invocation)
			if err == nil || !strings.Contains(err.Error(), "terminal/"+test.fail) {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(terminal.recordedCalls(), test.wantCalls) {
				t.Fatalf("terminal calls = %v, want %v", terminal.recordedCalls(), test.wantCalls)
			}
		})
	}
}

func TestShellDelegatedCleanupPreservesFirstError(t *testing.T) {
	terminal := &fakeTerminal{
		blockWait:   true,
		waitStarted: make(chan struct{}),
		fail: map[string]bool{
			"kill":    true,
			"output":  true,
			"release": true,
		},
	}
	invocation := testInvocation(t, `{"command":"sleep 30","timeout":1}`)
	invocation.Terminal = terminal.operations()
	_, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err == nil || !strings.Contains(err.Error(), "terminal/kill") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(
		terminal.recordedCalls(),
		[]string{"create", "wait", "kill", "output", "release"},
	) {
		t.Fatalf("terminal calls = %v", terminal.recordedCalls())
	}
}

func TestWriteFileCreatesAndReplacesWithoutReadingFirst(t *testing.T) {
	invocation := testInvocation(t, `{"path":"nested/file.txt","content":"first\n"}`)
	write := toolNamed(t, "write_file")
	got, err := invoke(t, write, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Created nested/file.txt") {
		t.Fatalf("create result = %q", got)
	}

	path := filepath.Join(invocation.Root, "nested", "file.txt")
	writeToolFile(t, invocation.Root, "existing.txt", "before\n")
	invocation.Arguments = json.RawMessage(`{"path":"existing.txt","content":"after\n"}`)
	if _, err := invoke(t, write, invocation); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(invocation.Root, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after\n" {
		t.Fatalf("overwrite = %q", data)
	}

	if err := os.WriteFile(filepath.Join(invocation.Root, "existing.txt"), []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	invocation.Arguments = json.RawMessage(`{"path":"existing.txt","content":"again\n"}`)
	if _, err := invoke(t, write, invocation); err != nil {
		t.Fatalf("replace after an outside change: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(invocation.Root, "existing.txt")); err != nil || string(data) != "again\n" {
		t.Fatalf("replacement after outside change = %q, %v", data, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "first\n" {
		t.Fatalf("created file = %q, %v", data, err)
	}
}

func TestShellAndFileMutationsRunInEitherOrder(t *testing.T) {
	invocation := testInvocation(t, `{"command":"printf 'shell\\n' > from-shell.txt"}`)
	path := writeToolFile(t, invocation.Root, "file.txt", "one\n")
	if _, err := invoke(t, toolNamed(t, "shell"), invocation); err != nil {
		t.Fatal(err)
	}

	invocation.Arguments = json.RawMessage(
		`{"path":"file.txt","old_string":"one","new_string":"ONE"}`,
	)
	if _, err := invoke(t, toolNamed(t, "edit_file"), invocation); err != nil {
		t.Fatalf("edit after shell: %v", err)
	}

	invocation.Arguments = json.RawMessage(`{"path":"file.txt","content":"two\n"}`)
	if _, err := invoke(t, toolNamed(t, "write_file"), invocation); err != nil {
		t.Fatal(err)
	}
	invocation.Arguments = json.RawMessage(`{"command":"test \"$(cat file.txt)\" = two"}`)
	result, err := invoke(t, toolNamed(t, "shell"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, "exit code: 0\n") {
		t.Fatalf("shell after write = %q", result)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "two\n" {
		t.Fatalf("final content = %q, %v", data, err)
	}
}

func TestWriteFilePreservesInvisibleTextState(t *testing.T) {
	invocation := testInvocation(t, `{"path":"file.txt"}`)
	path := filepath.Join(invocation.Root, "file.txt")
	original := append(append([]byte(nil), utf8BOM...), []byte("one\r\ntwo")...)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	invocation.Arguments = json.RawMessage(`{"path":"file.txt","content":"three\nfour\n"}`)
	if _, err := invoke(t, toolNamed(t, "write_file"), invocation); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte(nil), utf8BOM...), []byte("three\r\nfour")...)
	if string(got) != string(want) {
		t.Fatalf("written bytes = %q, want %q", got, want)
	}
}

func TestWriteFileFollowsInternalSymlinkSpellings(t *testing.T) {
	invocation := testInvocation(t, `{"path":"link"}`)
	target := writeToolFile(t, invocation.Root, "real/file.txt", "before\n")
	link := filepath.Join(invocation.Root, "link")
	if err := os.Symlink("real/file.txt", link); err != nil {
		t.Fatal(err)
	}
	invocation.Arguments = json.RawMessage(`{"path":"link","content":"after\n"}`)
	if _, err := invoke(t, toolNamed(t, "write_file"), invocation); err != nil {
		t.Fatal(err)
	}
	invocation.Arguments = json.RawMessage(`{"path":"real/file.txt","content":"again\n"}`)
	if _, err := invoke(t, toolNamed(t, "write_file"), invocation); err != nil {
		t.Fatalf("canonical spelling failed: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "again\n" {
		t.Fatalf("target content = %q", data)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("write_file replaced the symlink")
	}
}

func TestReadableSpillNestedUnderWorkspaceCannotAuthorizeOrReceiveMutations(t *testing.T) {
	invocation := testInvocation(t, `{"path":"placeholder"}`)
	invocation.SpillDir = filepath.Join(invocation.Root, ".session", "spill")
	path := writeToolFile(t, invocation.SpillDir, "result.txt", "overflow")
	invocation.Arguments = json.RawMessage(
		`{"path":` + string(mustJSON(t, path)) + `}`,
	)
	if got, err := invoke(t, toolNamed(t, "read_file"), invocation); err != nil ||
		got != "overflow" {
		t.Fatalf("spill read = %q, %v", got, err)
	}
	invocation.Arguments = json.RawMessage(
		`{"path":` + string(mustJSON(t, path)) + `,"content":"changed"}`,
	)
	if _, err := invoke(t, toolNamed(t, "write_file"), invocation); err == nil ||
		!strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("spill write error = %v", err)
	}
	invocation.Arguments = json.RawMessage(
		`{"path":` + string(mustJSON(t, path)) +
			`,"old_string":"overflow","new_string":"changed"}`,
	)
	if _, err := invoke(t, toolNamed(t, "edit_file"), invocation); err == nil ||
		!strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("spill edit error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "overflow" {
		t.Fatalf("spill content = %q", data)
	}
}

func TestEditFileMatchesExactlyAndExplainsRefusals(t *testing.T) {
	invocation := testInvocation(t, `{"path":"file.txt","old_string":"one","new_string":"ONE"}`)
	path := writeToolFile(t, invocation.Root, "file.txt", "one\ntwo\none\n")
	edit := toolNamed(t, "edit_file")
	if _, err := invoke(t, edit, invocation); err == nil ||
		!strings.Contains(err.Error(), "occurs 2 times") ||
		!strings.Contains(err.Error(), "lines 1 and 3") {
		t.Fatalf("ambiguous error = %v", err)
	}
	invocation.Arguments = json.RawMessage(
		`{"path":"file.txt","old_string":"one","new_string":"ONE","replace_all":true}`,
	)
	got, err := invoke(t, edit, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "2 replacement(s)") {
		t.Fatalf("replace-all result = %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ONE\ntwo\nONE\n" {
		t.Fatalf("replace-all content = %q", data)
	}

	invocation.Arguments = json.RawMessage(
		`{"path":"file.txt","old_string":"two extra","new_string":"TWO"}`,
	)
	if _, err := invoke(t, edit, invocation); err == nil ||
		!strings.Contains(err.Error(), "nearest matching line") ||
		!strings.Contains(err.Error(), "2: two") {
		t.Fatalf("missing-match error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) {
		t.Fatal("refused edit changed the file")
	}
}

func TestEditFilePreservesCRLFBOMAndTrailingNewlineState(t *testing.T) {
	invocation := testInvocation(t, `{
		"path":"file.txt",
		"old_string":"one\ntwo",
		"new_string":"three\nfour\n"
	}`)
	path := filepath.Join(invocation.Root, "file.txt")
	original := append(append([]byte(nil), utf8BOM...), []byte("one\r\ntwo")...)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, toolNamed(t, "edit_file"), invocation); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The replacement asked for a trailing newline, so the edit keeps it.
	want := append(append([]byte(nil), utf8BOM...), []byte("three\r\nfour\r\n")...)
	if string(got) != string(want) {
		t.Fatalf("edited bytes = %q, want %q", got, want)
	}
}

func TestEditFileUsesCurrentContentWithoutReadingFirst(t *testing.T) {
	invocation := testInvocation(t, `{"path":"file.txt","old_string":"one","new_string":"ONE"}`)
	path := writeToolFile(t, invocation.Root, "file.txt", "one\ntwo\n")
	edit := toolNamed(t, "edit_file")

	if _, err := invoke(t, edit, invocation); err != nil {
		t.Fatalf("unread edit: %v", err)
	}
	if err := os.WriteFile(path, []byte("ONE\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	invocation.Arguments = json.RawMessage(`{"path":"file.txt","old_string":"two","new_string":"TWO"}`)
	if _, err := invoke(t, edit, invocation); err != nil {
		t.Fatalf("edit after outside change: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "ONE\nTWO\nthree\n" {
		t.Fatalf("edited current content = %q", after)
	}
}

func TestEditFileReplacesNonOverlappingMatchesAndKeepsOtherBytes(t *testing.T) {
	invocation := testInvocation(t, `{"path":"file.txt","old_string":"aa","new_string":"b"}`)
	// "aaa" offers two overlapping candidates but only one replacement, and the
	// CRLF line is untouched by the edit so it must survive as CRLF.
	path := writeToolFile(t, invocation.Root, "file.txt", "aaa\nkeep\r\ntail\n\n\n")
	edit := toolNamed(t, "edit_file")

	got, err := invoke(t, edit, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "1 replacement(s)") {
		t.Fatalf("result = %q", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "ba\nkeep\r\ntail\n\n\n" {
		t.Fatalf("edited bytes = %q", after)
	}
}

func TestReadFileWindowsTruthfullyAndPreservesSmallFiles(t *testing.T) {
	invocation := testInvocation(t, `{"path":"small.txt"}`)
	writeToolFile(t, invocation.Root, "small.txt", "one\ntwo\n")
	read := toolNamed(t, "read_file")
	got, err := invoke(t, read, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if got != "one\ntwo\n" {
		t.Fatalf("small read = %q", got)
	}

	invocation.Arguments = json.RawMessage(`{"path":"small.txt","offset":2,"limit":1}`)
	got, err = invoke(t, read, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "two\n[lines 2-2 of 2") {
		t.Fatalf("window = %q", got)
	}

	invocation.Arguments = json.RawMessage(`{"path":"small.txt","offset":3}`)
	got, err = invoke(t, read, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "past the end of the file (2 lines)") {
		t.Fatalf("past-EOF read = %q", got)
	}

	writeToolFile(t, invocation.Root, "empty.txt", "")
	invocation.Arguments = json.RawMessage(`{"path":"empty.txt"}`)
	got, err = invoke(t, read, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("empty read = %q", got)
	}
}

func TestReadFileValidatesBoundsUTF8AndHugeLines(t *testing.T) {
	invocation := testInvocation(t, `{"path":"file","offset":0}`)
	writeToolFile(t, invocation.Root, "file", "text")
	read := toolNamed(t, "read_file")
	if _, err := invoke(t, read, invocation); err == nil {
		t.Fatal("zero offset was accepted")
	}
	invocation.Arguments = json.RawMessage(`{"path":"file","limit":-1}`)
	if _, err := invoke(t, read, invocation); err == nil {
		t.Fatal("negative limit was accepted")
	}
	invocation.Arguments = json.RawMessage(`{"path":"file","unknown":1}`)
	if _, err := invoke(t, read, invocation); err == nil {
		t.Fatal("unknown argument was accepted")
	}

	if err := os.WriteFile(filepath.Join(invocation.Root, "binary"), []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	invocation.Arguments = json.RawMessage(`{"path":"binary"}`)
	if _, err := invoke(t, read, invocation); err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}

	huge := strings.Repeat("x", workspace.InlineMaxBytes+1) + "\nsecond\n"
	writeToolFile(t, invocation.Root, "huge", huge)
	invocation.Arguments = json.RawMessage(`{"path":"huge"}`)
	got, err := invoke(t, read, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > workspace.InlineMaxBytes {
		t.Fatalf("huge first line returned %d bytes, want at most %d",
			len(got), workspace.InlineMaxBytes)
	}
	if !strings.HasPrefix(got, "xxx") ||
		!strings.Contains(got, fmt.Sprintf("of %d bytes]", workspace.InlineMaxBytes+1)) {
		t.Fatalf("huge first line = %q", got)
	}
}

func TestReadFileBoundsOutputAtTheInlineLimit(t *testing.T) {
	invocation := testInvocation(t, `{"path":"exact"}`)
	read := toolNamed(t, "read_file")

	// A file exactly at the inline bound still comes back whole: the footer is
	// only reserved once a window is needed.
	line := strings.Repeat("a", workspace.InlineMaxBytes/workspace.InlineMaxLines-1) + "\n"
	exact := strings.Repeat(line, workspace.InlineMaxLines)
	writeToolFile(t, invocation.Root, "exact", exact)
	got, err := invoke(t, read, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(exact) != workspace.InlineMaxBytes {
		t.Fatalf("fixture is %d bytes, want exactly %d", len(exact), workspace.InlineMaxBytes)
	}
	if got != exact {
		t.Fatalf("exact-limit read returned %d bytes, want the whole %d byte file",
			len(got), len(exact))
	}

	// A single oversized line with no trailing newline is truncated on a rune
	// boundary, not in the middle of one.
	runes := strings.Repeat("é", workspace.InlineMaxBytes)
	writeToolFile(t, invocation.Root, "one-line", runes)
	invocation.Arguments = json.RawMessage(`{"path":"one-line"}`)
	got, err = invoke(t, read, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > workspace.InlineMaxBytes {
		t.Fatalf("single-line read returned %d bytes, want at most %d",
			len(got), workspace.InlineMaxBytes)
	}
	shown, _, ok := strings.Cut(got, "\n[line 1 of 1;")
	if !ok {
		t.Fatalf("single-line read = %q", got[max(0, len(got)-80):])
	}
	if !utf8.ValidString(shown) {
		t.Fatal("truncation split a rune")
	}
}

func TestReadFileRefusesAFileOverTheWorkspaceLimit(t *testing.T) {
	invocation := testInvocation(t, `{"path":"big"}`)
	path := filepath.Join(invocation.Root, "big")
	if err := os.WriteFile(path, make([]byte, workspace.MaxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := invoke(t, toolNamed(t, "read_file"), invocation)
	if err == nil || !strings.Contains(err.Error(), "exceeds the") {
		t.Fatalf("oversized read error = %v", err)
	}
}

func TestGrepModesGlobFilterAndBinaryTail(t *testing.T) {
	invocation := testInvocation(t, `{"pattern":"needle"}`)
	writeToolFile(t, invocation.Root, "a.go", "needle\nno\nneedle\n")
	writeToolFile(t, invocation.Root, "nested/b.go", "needle\n")
	writeToolFile(t, invocation.Root, "nested/c.txt", "needle\n")
	if err := os.WriteFile(
		filepath.Join(invocation.Root, "binary.go"),
		[]byte("needle\n\xff"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	grep := toolNamed(t, "grep")

	got, err := invoke(t, grep, invocation)
	if err != nil {
		t.Fatal(err)
	}
	// The binary file matches on its first line and only then becomes
	// unreadable, so that match survives and the file says where it stopped.
	if !strings.Contains(got, "./binary.go:1: needle") ||
		!strings.Contains(got, "./binary.go:2: [scan stopped:") ||
		!strings.Contains(got, "./a.go:1: needle") ||
		!strings.Contains(got, "./nested/c.txt:1: needle") {
		t.Fatalf("content output = %q", got)
	}

	invocation.Arguments = json.RawMessage(
		`{"pattern":"needle","path":"nested","glob":"*.go","output_mode":"files_with_matches"}`,
	)
	got, err = invoke(t, grep, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if got != "nested/b.go" {
		t.Fatalf("filtered files output = %q", got)
	}

	invocation.Arguments = json.RawMessage(`{"pattern":"needle","output_mode":"count"}`)
	got, err = invoke(t, grep, invocation)
	if err != nil {
		t.Fatal(err)
	}
	// A count cut short says so rather than reporting a quietly low number.
	if !strings.Contains(got, "./a.go: 2") ||
		!strings.Contains(got, "./binary.go: 1") ||
		!strings.Contains(got, "./binary.go:2: [scan stopped:") {
		t.Fatalf("count output = %q", got)
	}
}

func TestGrepRejectsInvalidValuesAndSkipsOverlongLines(t *testing.T) {
	invocation := testInvocation(t, `{"pattern":"["}`)
	grep := toolNamed(t, "grep")
	if _, err := invoke(t, grep, invocation); err == nil ||
		!strings.Contains(err.Error(), "invalid regex: [") {
		t.Fatalf("invalid regex error = %v", err)
	}
	invocation.Arguments = json.RawMessage(`{"pattern":"x","output_mode":"wat"}`)
	if _, err := invoke(t, grep, invocation); err == nil ||
		!strings.Contains(err.Error(), "wat") {
		t.Fatalf("invalid mode error = %v", err)
	}
	writeToolFile(
		t,
		invocation.Root,
		"huge.txt",
		"needle "+strings.Repeat("x", maxScanLineBytes)+"\n",
	)
	invocation.Arguments = json.RawMessage(`{"pattern":"needle"}`)
	got, err := invoke(t, grep, invocation)
	if err != nil {
		t.Fatal(err)
	}
	// Reporting the stop keeps an unsearchable file from reading as a file with
	// nothing in it.
	if !strings.Contains(got, "./huge.txt:1: [scan stopped:") {
		t.Fatalf("overlong file output = %q", got)
	}
}

// TestGrepStopsAtTheFirstMatchForFilenames checks that a filename-only search
// does not keep reading a file after it has its answer, which also means an
// unreadable later line cannot turn a match into a diagnostic.
func TestGrepStopsAtTheFirstMatchForFilenames(t *testing.T) {
	invocation := testInvocation(t, `{"pattern":"needle","output_mode":"files_with_matches"}`)
	if err := os.WriteFile(
		filepath.Join(invocation.Root, "binary.go"),
		[]byte("needle\n\xff"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	got, err := invoke(t, toolNamed(t, "grep"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if got != "./binary.go" {
		t.Fatalf("filenames output = %q", got)
	}
}

// TestDiscoverySkipsHiddenPaths holds glob and grep to the hidden-path rule the
// specification states.
func TestDiscoverySkipsHiddenPaths(t *testing.T) {
	invocation := testInvocation(t, `{"pattern":"**/*.go"}`)
	writeToolFile(t, invocation.Root, "visible.go", "needle\n")
	writeToolFile(t, invocation.Root, ".hidden.go", "needle\n")
	writeToolFile(t, invocation.Root, ".config/inside.go", "needle\n")

	globbed, err := invoke(t, toolNamed(t, "glob"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if globbed != "./visible.go" {
		t.Fatalf("glob output = %q", globbed)
	}

	invocation.Arguments = json.RawMessage(`{"pattern":"needle"}`)
	grepped, err := invoke(t, toolNamed(t, "grep"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if grepped != "./visible.go:1: needle" {
		t.Fatalf("grep output = %q", grepped)
	}
}

func TestGlobMatchesAtAnyDepthAnchorsPathsAndSorts(t *testing.T) {
	invocation := testInvocation(t, `{"pattern":"*.go"}`)
	writeToolFile(t, invocation.Root, "z.go", "")
	writeToolFile(t, invocation.Root, "src/b.go", "")
	writeToolFile(t, invocation.Root, "src/a.go", "")
	writeToolFile(t, invocation.Root, "other/a.txt", "")
	glob := toolNamed(t, "glob")

	got, err := invoke(t, glob, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if got != "./src/a.go\n./src/b.go\n./z.go" {
		t.Fatalf("slash-free glob = %q", got)
	}
	invocation.Arguments = json.RawMessage(`{"pattern":"src/*.go"}`)
	got, err = invoke(t, glob, invocation)
	if err != nil {
		t.Fatal(err)
	}
	if got != "./src/a.go\n./src/b.go" {
		t.Fatalf("anchored glob = %q", got)
	}
	invocation.Arguments = json.RawMessage(`{"pattern":"["}`)
	if _, err := invoke(t, glob, invocation); err == nil {
		t.Fatal("invalid glob was accepted")
	}
}

func TestGrepSpillCanBeReadBack(t *testing.T) {
	invocation := testInvocation(t, `{"pattern":"needle"}`)
	var content strings.Builder
	for index := 0; index <= workspace.InlineMaxLines; index++ {
		content.WriteString("needle\n")
	}
	writeToolFile(t, invocation.Root, "many.txt", content.String())
	got, err := invoke(t, toolNamed(t, "grep"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	const marker = "full output at "
	start := strings.Index(got, marker)
	if start < 0 {
		t.Fatalf("grep did not spill: %q", got)
	}
	path := got[start+len(marker):]
	path = strings.TrimSuffix(path, "]")
	if !utf8.ValidString(path) {
		t.Fatal("spill path is not UTF-8")
	}
	readInvocation := invocation
	readInvocation.Arguments = json.RawMessage(`{"path":` + string(mustJSON(t, path)) + `}`)
	readBack, err := invoke(t, toolNamed(t, "read_file"), readInvocation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readBack, "many.txt:1: needle") {
		t.Fatalf("spill read = %q", readBack)
	}
}

func mustJSON(t *testing.T, value string) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
