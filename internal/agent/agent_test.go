package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/zalando/go-keyring"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
)

func TestInitializeNegotiatesSupportedVersion(t *testing.T) {
	instance, err := New(Config{
		Name:    "ox",
		Version: "0.0.1",
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := instance.Initialize(context.Background(), acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion,
	})
	if err != nil {
		t.Fatal(err)
	}

	if response.ProtocolVersion != acp.ProtocolVersion {
		t.Fatalf("protocol version = %d", response.ProtocolVersion)
	}
	if response.AgentInfo == nil || response.AgentInfo.Name != "ox" {
		t.Fatalf("agent info = %#v", response.AgentInfo)
	}
	if response.AgentCapabilities == nil {
		t.Fatal("agent capabilities were omitted")
	}
	if !response.AgentCapabilities.LoadSession ||
		response.AgentCapabilities.SessionCapabilities == nil ||
		response.AgentCapabilities.SessionCapabilities.List == nil ||
		response.AgentCapabilities.SessionCapabilities.Delete == nil ||
		response.AgentCapabilities.SessionCapabilities.Resume == nil ||
		response.AgentCapabilities.SessionCapabilities.Close == nil {
		t.Fatalf("session capabilities = %#v", response.AgentCapabilities)
	}
	if len(response.AuthMethods) != 1 ||
		response.AuthMethods[0].ID != openRouterAuthMethodID {
		t.Fatalf("auth methods = %#v", response.AuthMethods)
	}
	if response.AgentCapabilities.Auth == nil ||
		response.AgentCapabilities.Auth.Logout == nil {
		t.Fatalf("auth capabilities = %#v", response.AgentCapabilities.Auth)
	}
}

func TestAuthenticateAndLogoutUpdateRunningAgent(t *testing.T) {
	store := emptyCredentialStore(t)
	instance, err := New(Config{
		Logger:        discardLogger(),
		Credentials:   store,
		ModelOverride: "test/model",
		Client:        &staticCompletionModel{},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = instance.NewSession(context.Background(), validNewSessionRequest(t))
	assertErrorCode(t, err, acp.ErrCodeAuthRequired)
	_, err = instance.Authenticate(context.Background(), acp.AuthenticateRequest{
		MethodID: "unknown",
	})
	assertErrorCode(t, err, jrpc2.InvalidParams)
	_, err = instance.Authenticate(context.Background(), acp.AuthenticateRequest{
		MethodID: openRouterAuthMethodID,
	})
	assertErrorCode(t, err, acp.ErrCodeAuthRequired)
	const key = "never-serialize-this-key"
	if err := keyring.Set("ox", "openrouter", key); err != nil {
		t.Fatal(err)
	}
	response, err := instance.Authenticate(context.Background(), acp.AuthenticateRequest{
		MethodID: openRouterAuthMethodID,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatalf("authenticate response leaked key: %s", raw)
	}
	if store.Key() != key || store.Source() != credentials.SourceKeyring {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	if _, err := instance.NewSession(context.Background(), validNewSessionRequest(t)); err != nil {
		t.Fatalf("new session after authentication: %v", err)
	}

	_, err = instance.Logout(context.Background(), acp.LogoutRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if store.Key() != "" || store.Source() != credentials.SourceNone {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	_, err = instance.NewSession(context.Background(), validNewSessionRequest(t))
	assertErrorCode(t, err, acp.ErrCodeAuthRequired)
}

func TestAuthenticateRefreshesCredentialProvidedOutOfBand(t *testing.T) {
	store := emptyCredentialStore(t)
	instance, err := New(Config{Logger: discardLogger(), Credentials: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set("ox", "openrouter", "out-of-band"); err != nil {
		t.Fatal(err)
	}

	_, err = instance.Authenticate(context.Background(), acp.AuthenticateRequest{
		MethodID: openRouterAuthMethodID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.Key() != "out-of-band" || store.Source() != credentials.SourceKeyring {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
}

func TestLogoutRejectsEnvironmentCredential(t *testing.T) {
	t.Setenv("OX_KEYRING_DISABLED", "1")
	t.Setenv("OPENROUTER_API_KEY", "environment-key")
	store := credentials.NewStore(discardLogger())
	instance, err := New(Config{Logger: discardLogger(), Credentials: store})
	if err != nil {
		t.Fatal(err)
	}

	_, err = instance.Logout(context.Background(), acp.LogoutRequest{})
	assertErrorCode(t, err, jrpc2.InternalError)
	if !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("logout error = %v", err)
	}
	if store.Key() != "environment-key" ||
		store.Source() != credentials.SourceEnvironment {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
}

func TestInitializeReturnsLatestVersionWhenRequestedVersionIsUnsupported(t *testing.T) {
	instance, err := New(Config{
		Name:    "ox",
		Version: "0.0.1",
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := instance.Initialize(context.Background(), acp.InitializeRequest{
		ProtocolVersion: 999,
	})
	if err != nil {
		t.Fatal(err)
	}

	if response.ProtocolVersion != acp.ProtocolVersion {
		t.Fatalf("protocol version = %d, want %d", response.ProtocolVersion, acp.ProtocolVersion)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewRejectsDuplicateToolNames(t *testing.T) {
	_, err := New(Config{Tools: []Tool{{Name: "same"}, {Name: "same"}}})
	if err == nil || !strings.Contains(err.Error(), "duplicate tool") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewBuildsSubagentToolSetWithoutDelegators(t *testing.T) {
	_, err := New(Config{Tools: []Tool{{
		Name:  "read",
		Label: func(json.RawMessage) string { return "Read" },
	}}})
	if err == nil || !strings.Contains(err.Error(), "cannot set Label") {
		t.Fatalf("non-delegating label error = %v", err)
	}

	_, err = New(Config{Tools: []Tool{{
		Name:      "task",
		Delegates: true,
	}}})
	if err == nil || !strings.Contains(err.Error(), "must set Label") {
		t.Fatalf("missing label error = %v", err)
	}

	_, err = New(Config{Tools: []Tool{{
		Name:      "task",
		Delegates: true,
		Label:     func(json.RawMessage) string { return "Task" },
	}}})
	if err == nil || !strings.Contains(err.Error(), "subagent tool set is empty") {
		t.Fatalf("empty subagent set error = %v", err)
	}

	instance, err := New(Config{Tools: []Tool{
		{
			Name:      "task",
			Delegates: true,
			Label:     func(json.RawMessage) string { return "Task" },
		},
		{Name: "read"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(instance.primaryTools.tools) != 2 ||
		len(instance.subagentTools.tools) != 1 ||
		instance.subagentTools.tools[0].Name != "read" {
		t.Fatalf(
			"primary = %#v, subagent = %#v",
			instance.primaryTools.tools,
			instance.subagentTools.tools,
		)
	}
}

func TestPromptMessageConvertsPromptContent(t *testing.T) {
	message, err := promptMessage([]acp.ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "resource_link", Name: "a [guide]", URI: "file:///guide).md"},
		{Type: "image", MIMEType: "image/png", Data: "aW1hZ2U="},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 3 {
		t.Fatalf("content = %#v", message.Content)
	}
	if got := message.Content[1].Text; got != `[a \[guide\]](file:///guide\).md)` {
		t.Fatalf("resource link = %q", got)
	}
	if got := message.Content[2].ImageURL; got != "data:image/png;base64,aW1hZ2U=" {
		t.Fatalf("image URL = %q", got)
	}

	_, err = promptMessage([]acp.ContentBlock{{Type: "future"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("unsupported content error = %v", err)
	}
}

func TestPartitionUsesExclusiveCallsAsFences(t *testing.T) {
	instance, err := New(Config{Tools: []Tool{
		{Name: "parallel", ParallelSafe: true},
		{Name: "exclusive"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	calls := []openrouter.ToolCall{
		toolCall("1", "parallel"),
		toolCall("2", "parallel"),
		toolCall("3", "exclusive"),
		toolCall("4", "missing"),
		toolCall("5", "parallel"),
	}
	groups := instance.partition(calls)
	want := []toolGroup{{0, 2}, {2, 3}, {3, 4}, {4, 5}}
	if len(groups) != len(want) {
		t.Fatalf("groups = %#v", groups)
	}
	for index := range want {
		if groups[index] != want[index] {
			t.Fatalf("group %d = %#v, want %#v", index, groups[index], want[index])
		}
	}
}

func TestParallelToolResultsJoinInCallOrder(t *testing.T) {
	started := make(chan string, 2)
	release := map[string]chan struct{}{
		"1": make(chan struct{}),
		"2": make(chan struct{}),
	}
	instance, err := New(Config{Logger: discardLogger(), Tools: []Tool{{
		Name:         "parallel",
		Approval:     ApprovalNone,
		ParallelSafe: true,
		Execute: func(
			_ context.Context,
			invocation Invocation,
		) (string, error) {
			id := strings.Trim(string(invocation.Arguments), `"`)
			started <- id
			<-release[id]
			return "result-" + id, nil
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan event)
	var collected []event
	var eventWait sync.WaitGroup
	eventWait.Add(1)
	go func() {
		defer eventWait.Done()
		for current := range events {
			collected = append(collected, current)
		}
	}()
	done := make(chan []toolResult, 1)
	go func() {
		done <- instance.executeBatch(context.Background(), ephemeralToolSession(t), []openrouter.ToolCall{
			toolCallWithArguments("call-1", "parallel", `"1"`),
			toolCallWithArguments("call-2", "parallel", `"2"`),
		}, nil, events)
		close(events)
	}()

	first := <-started
	second := <-started
	if first == second {
		t.Fatalf("tools did not start independently: %q, %q", first, second)
	}
	close(release["2"])
	close(release["1"])
	results := <-done
	eventWait.Wait()

	if results[0].content != "result-1" {
		t.Fatalf("first result = %#v", results[0])
	}
	if results[1].content != "result-2" {
		t.Fatalf("second result = %#v", results[1])
	}
	if len(collected) == 0 {
		t.Fatal("no tool events were emitted")
	}
}

func TestExclusiveToolFencesParallelGroups(t *testing.T) {
	parallelStarted := make(chan string, 3)
	parallelRelease := map[string]chan struct{}{
		"one":   make(chan struct{}),
		"two":   make(chan struct{}),
		"three": make(chan struct{}),
	}
	exclusiveStarted := make(chan struct{}, 1)
	exclusiveRelease := make(chan struct{})
	instance, err := New(Config{Logger: discardLogger(), Tools: []Tool{
		{
			Name:         "parallel",
			Approval:     ApprovalNone,
			ParallelSafe: true,
			Execute: func(
				_ context.Context,
				invocation Invocation,
			) (string, error) {
				id := strings.Trim(string(invocation.Arguments), `"`)
				parallelStarted <- id
				<-parallelRelease[id]
				return id, nil
			},
		},
		{
			Name:     "exclusive",
			Approval: ApprovalNone,
			Execute: func(
				context.Context,
				Invocation,
			) (string, error) {
				exclusiveStarted <- struct{}{}
				<-exclusiveRelease
				return "exclusive", nil
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan event)
	go func() {
		for range events {
		}
	}()
	done := make(chan struct{})
	go func() {
		instance.executeBatch(context.Background(), ephemeralToolSession(t), []openrouter.ToolCall{
			toolCallWithArguments("1", "parallel", `"one"`),
			toolCallWithArguments("2", "parallel", `"two"`),
			toolCall("3", "exclusive"),
			toolCallWithArguments("4", "parallel", `"three"`),
		}, nil, events)
		close(events)
		close(done)
	}()

	first := <-parallelStarted
	second := <-parallelStarted
	if first == second {
		t.Fatalf("first parallel group = %q, %q", first, second)
	}
	select {
	case <-exclusiveStarted:
		t.Fatal("exclusive tool started before the first parallel group joined")
	default:
	}
	close(parallelRelease["one"])
	close(parallelRelease["two"])
	<-exclusiveStarted
	select {
	case id := <-parallelStarted:
		t.Fatalf("later parallel group started during exclusive tool: %q", id)
	default:
	}
	close(exclusiveRelease)
	if id := <-parallelStarted; id != "three" {
		t.Fatalf("later parallel tool = %q", id)
	}
	close(parallelRelease["three"])
	<-done
}

func TestExclusiveToolSerializesAcrossConcurrentBatches(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	instance, err := New(Config{Logger: discardLogger(), Tools: []Tool{{
		Name:     "exclusive",
		Approval: ApprovalNone,
		Execute: func(
			_ context.Context,
			invocation Invocation,
		) (string, error) {
			id := strings.Trim(string(invocation.Arguments), `"`)
			started <- id
			if id == "first" {
				<-release
			}
			return id, nil
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	value := ephemeralToolSession(t)
	done := make(chan []toolResult, 2)
	events := make(chan event, 16)
	for _, id := range []string{"first", "second"} {
		go func() {
			done <- instance.executeBatch(
				context.Background(),
				value,
				[]openrouter.ToolCall{
					toolCallWithArguments(id, "exclusive", `"`+id+`"`),
				},
				nil,
				events,
			)
		}()
		if id == "first" {
			if call := <-started; call != "first" {
				t.Fatalf("first execution = %q", call)
			}
		}
	}
	for {
		current := <-events
		if current.call.ID == "second" && current.kind == eventToolStarted {
			t.Fatal("waiting exclusive tool was announced as running")
		}
		if current.call.ID == "second" && current.kind == eventToolPending {
			break
		}
	}
	select {
	case call := <-started:
		t.Fatalf("exclusive sibling started early: %q", call)
	default:
	}
	close(release)
	if call := <-started; call != "second" {
		t.Fatalf("second execution = %q", call)
	}
	for range 2 {
		results := <-done
		if len(results) != 1 || results[0].failed {
			t.Fatalf("results = %#v", results)
		}
	}
}

func TestToolFailuresEachProduceOneTerminalResult(t *testing.T) {
	instance, err := New(Config{Logger: discardLogger(), Tools: []Tool{
		{
			Name:     "panic",
			Approval: ApprovalNone,
			Execute: func(
				context.Context,
				Invocation,
			) (string, error) {
				panic("boom")
			},
		},
		{
			Name:     "error",
			Approval: ApprovalNone,
			Execute: func(
				context.Context,
				Invocation,
			) (string, error) {
				return "", errors.New("bad arguments")
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan event)
	var failed int
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for current := range events {
			if current.kind == eventToolFailed {
				failed++
			}
		}
	}()
	results := instance.executeBatch(context.Background(), ephemeralToolSession(t), []openrouter.ToolCall{
		toolCall("1", "missing"),
		toolCall("2", "panic"),
		toolCall("3", "error"),
	}, nil, events)
	close(events)
	wait.Wait()

	if len(results) != 3 || failed != 0 {
		t.Fatalf("results = %d, failed terminal events = %d", len(results), failed)
	}
	for index, result := range results {
		if !result.failed || result.content == "" {
			t.Fatalf("result %d = %#v", index, result)
		}
	}
}

func TestValidateToolCallIDsRejectsExistingAndBatchDuplicates(t *testing.T) {
	value := ephemeralToolSession(t)
	value.state.toolCallIDs = map[string]struct{}{"existing": {}}
	for _, calls := range [][]openrouter.ToolCall{
		{toolCall("existing", "tool")},
		{toolCall("same", "tool"), toolCall("same", "tool")},
		{toolCall("", "tool")},
	} {
		if err := validateToolCallIDs(value, calls); err == nil {
			t.Fatalf("tool calls were accepted: %#v", calls)
		}
	}
}

func TestEditToolTargetsAreCanonicalAndConfined(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	instance, err := New(Config{Tools: []Tool{
		{Name: "write_file", Kind: acp.ToolKindEdit},
		{Name: "read_file", Kind: acp.ToolKindRead},
	}})
	if err != nil {
		t.Fatal(err)
	}
	calls := []openrouter.ToolCall{
		toolCallWithArguments("write", "write_file", `{"path":"alias/note.txt"}`),
		toolCallWithArguments("escape", "write_file", `{"path":"../outside.txt"}`),
		toolCallWithArguments("missing", "write_file", `{}`),
		toolCallWithArguments("read", "read_file", `{"path":"real/note.txt"}`),
	}
	targets := normalizedToolTargets(root, instance.primaryTools, calls)
	if !reflect.DeepEqual(targets, map[string]string{"write": "real/note.txt"}) {
		t.Fatalf("targets = %#v", targets)
	}
	request := instance.permissionRequest(
		"session",
		root,
		instance.primaryTools.tools[instance.primaryTools.byName["write_file"]],
		calls[0],
		"",
		"",
		targets["write"],
	)
	if len(request.ToolCall.Locations) != 1 ||
		request.ToolCall.Locations[0].Path != filepath.Join(realDir, "note.txt") {
		t.Fatalf("permission locations = %#v", request.ToolCall.Locations)
	}
}

func TestAdapterBoundsAndFlushesOutputBeforeTerminal(t *testing.T) {
	var notifications []acp.SessionNotification
	adapter := newAdapter("session", "/workspace", func(notification acp.SessionNotification) error {
		notifications = append(notifications, notification)
		return nil
	})
	defer adapter.close()
	call := toolCall("call", "tool")
	if err := adapter.handle(event{kind: eventToolPending, call: call, target: "a.go"}); err != nil {
		t.Fatal(err)
	}
	pending := notifications[0].Update.(acp.ToolCall)
	if len(pending.Locations) != 1 || pending.Locations[0].Path != "/workspace/a.go" {
		t.Fatalf("pending locations = %#v", pending.Locations)
	}
	notifications = nil
	output := strings.Repeat("a", maxToolOutputTail) + "newest"
	if err := adapter.handle(event{kind: eventToolOutput, call: call, text: output}); err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 0 {
		t.Fatalf("output was not rate limited: %#v", notifications)
	}
	if err := adapter.handle(event{kind: eventToolCompleted, call: call}); err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 {
		t.Fatalf("notifications = %#v", notifications)
	}
	outputUpdate := notifications[0].Update.(acp.ToolCallUpdate)
	if len(outputUpdate.Content) != 1 {
		t.Fatalf("output update = %#v", outputUpdate)
	}
	got := outputUpdate.Content[0].Content.Text
	if len(got) != maxToolOutputTail || !strings.HasSuffix(got, "newest") {
		t.Fatalf("bounded output length = %d, suffix = %q", len(got), got[len(got)-6:])
	}
	terminal := notifications[1].Update.(acp.ToolCallUpdate)
	if terminal.Status != acp.ToolCallStatusCompleted {
		t.Fatalf("terminal update = %#v", terminal)
	}
}

func TestSessionRejectsOverlappingPromptsAndCancelIsIdempotent(t *testing.T) {
	value := &session{}
	_, active, release, err := value.claim(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, _, _, err := value.claim(context.Background(), ""); err == nil {
		t.Fatal("overlapping claim succeeded")
	}
	if !value.cancel() {
		t.Fatal("active cancellation was ignored")
	}
	if !active.cancelledByClient.Load() {
		t.Fatal("active turn was not marked as client-cancelled")
	}
	if !release() {
		t.Fatal("turn completion did not observe the accepted cancellation")
	}
	if value.cancel() {
		t.Fatal("idle cancellation was not a no-op")
	}
}

func TestPrefixFingerprintIsStableAndSensitiveToToolOrderAndSettings(t *testing.T) {
	first, err := New(Config{
		Tools: []Tool{
			{Name: "a", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "b", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Config{
		Tools: []Tool{
			{Name: "b", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "a", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	value := &session{state: durableState{configuration: requestConfiguration{
		Settings:     settings.Resolved{Model: "model"},
		SystemPrompt: "system prompt",
		Tools:        first.primaryTools.modelTools,
	}}}
	fingerprint := first.prefixFingerprint(value)
	if fingerprint != first.prefixFingerprint(value) {
		t.Fatal("fingerprint changed without a configuration change")
	}
	secondValue := &session{state: durableState{configuration: requestConfiguration{
		Settings:     settings.Resolved{Model: "model"},
		SystemPrompt: "system prompt",
		Tools:        second.primaryTools.modelTools,
	}}}
	if fingerprint == second.prefixFingerprint(secondValue) {
		t.Fatal("fingerprint ignored tool registration order")
	}

	for name, other := range map[string]*session{
		"system prompt": {state: durableState{configuration: requestConfiguration{
			Settings:     settings.Resolved{Model: "model"},
			SystemPrompt: "changed system prompt",
			Tools:        first.primaryTools.modelTools,
		}}},
		"model": {state: durableState{configuration: requestConfiguration{
			Settings:     settings.Resolved{Model: "other"},
			SystemPrompt: "system prompt",
			Tools:        first.primaryTools.modelTools,
		}}},
		"temperature": {state: durableState{configuration: requestConfiguration{
			Settings: settings.Resolved{
				Model:       "model",
				Temperature: floatPointer(0.5),
			},
			SystemPrompt: "system prompt",
			Tools:        first.primaryTools.modelTools,
		}}},
		"reasoning": {state: durableState{configuration: requestConfiguration{
			Settings: settings.Resolved{
				Model:     "model",
				Reasoning: &openrouter.Reasoning{Effort: "high"},
			},
			SystemPrompt: "system prompt",
			Tools:        first.primaryTools.modelTools,
		}}},
		"provider": {state: durableState{configuration: requestConfiguration{
			Settings: settings.Resolved{
				Model:    "model",
				Provider: &openrouter.Provider{Order: []string{"alpha"}},
			},
			SystemPrompt: "system prompt",
			Tools:        first.primaryTools.modelTools,
		}}},
	} {
		if first.prefixFingerprint(other) == fingerprint {
			t.Fatalf("fingerprint ignored the session's %s setting", name)
		}
	}
}

func TestLoopStopsAtMaximumModelRequestsWithReplayableHistory(t *testing.T) {
	model := &repeatingToolModel{}
	instance, err := New(Config{
		Logger:        discardLogger(),
		ModelOverride: "model",
		Client:        model,
		Tools: []Tool{{
			Name:        "again",
			Approval:    ApprovalNone,
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Execute: func(
				context.Context,
				Invocation,
			) (string, error) {
				return "ok", nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	value := durableTestSession(t, instance, requestConfiguration{
		Settings:      settings.Resolved{Model: "model"},
		ContextWindow: 1_000_000,
		Tools:         instance.primaryTools.modelTools,
	}, "turn-max")
	events := make(chan event)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for range events {
		}
	}()
	outcome := instance.run(
		context.Background(),
		value,
		&activeTurn{turnID: "turn-max"},
		nil,
		ClientFileSystem{},
		ClientTerminal{},
		events,
	)
	close(events)
	wait.Wait()

	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.response.StopReason != acp.StopReasonMaxTurnRequests {
		t.Fatalf("stop reason = %q", outcome.response.StopReason)
	}
	if model.requests != maxTurnRequests {
		t.Fatalf("model requests = %d", model.requests)
	}
	if len(value.state.history) != 1+2*maxTurnRequests {
		t.Fatalf("history length = %d", len(value.state.history))
	}
}

func TestDelegateResumesCompactedChildFromOpenTurnCheckpoint(t *testing.T) {
	model := &capturingCompletionModel{completion: &openrouter.Completion{
		Text: "child done", FinishReason: "stop",
		Usage: &openrouter.Usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12},
	}}
	instance, err := New(Config{
		Logger: discardLogger(), Client: model,
		Tools: []Tool{
			{Name: "task", Delegates: true, Label: func(json.RawMessage) string { return "Task" }},
			{Name: "read", Approval: ApprovalNone},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "model"}, ContextWindow: 2400,
		SystemPrompt: "parent", Tools: cloneTools(instance.primaryTools.modelTools),
		ToolKinds: map[string]acp.ToolKind{"task": acp.ToolKindOther, "read": acp.ToolKindRead},
		Subagent: subagentConfiguration{
			SystemPrompt: "child", Tools: cloneTools(instance.subagentTools.modelTools),
		},
	}
	value := durableTestSession(t, instance, configuration, "turn")
	parentCall := openrouter.ToolCall{
		ID: "parent", Type: "function",
		Function: openrouter.ToolCallFunction{Name: "task", Arguments: `{"prompt":"inspect"}`},
	}
	if err := instance.commit(value, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{parentCall}, RequestCount: 1,
		Usage: &openrouter.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}); err != nil {
		t.Fatal(err)
	}
	prompt := textMessage(openrouter.RoleUser, "inspect")
	firstAssistant := openrouter.Message{
		Role: openrouter.RoleAssistant,
		ToolCalls: []openrouter.ToolCall{{
			ID: "provider-old", Type: "function",
			Function: openrouter.ToolCallFunction{Name: "read", Arguments: `{}`},
		}},
	}
	firstTool := openrouter.Message{
		Role: openrouter.RoleTool, ToolCallID: "provider-old",
		Content: []openrouter.ContentBlock{{Type: "text", Text: "old result"}},
	}
	recentAssistant := openrouter.Message{
		Role: openrouter.RoleAssistant,
		ToolCalls: []openrouter.ToolCall{{
			ID: "provider-recent", Type: "function",
			Function: openrouter.ToolCallFunction{Name: "read", Arguments: `{}`},
		}},
	}
	recentTool := openrouter.Message{
		Role: openrouter.RoleTool, ToolCallID: "provider-recent",
		Content: []openrouter.ContentBlock{{Type: "text", Text: "recent result"}},
	}
	record := delegationRecord{Prompt: "inspect", History: []openrouter.Message{prompt}}
	if err := instance.persistChildContext(value, "parent", record, nil); err != nil {
		t.Fatal(err)
	}
	firstUsage := openrouter.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}
	record.History = append(record.History, firstAssistant, firstTool)
	record.Calls = append(record.Calls, delegatedCall{
		CallID: "child-public-old", Name: "read", Arguments: json.RawMessage(`{}`), Content: "old result",
	})
	record.Usage = append(record.Usage, firstUsage)
	record.RequestCount = 1
	record.Occupancy = 5
	if err := instance.persistChildContext(value, "parent", record, nil); err != nil {
		t.Fatal(err)
	}
	secondUsage := openrouter.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}
	record.History = append(record.History, recentAssistant, recentTool)
	record.Calls = append(record.Calls, delegatedCall{
		CallID: "child-public-recent", Name: "read", Arguments: json.RawMessage(`{}`), Content: "recent result",
	})
	record.Usage = append(record.Usage, secondUsage)
	record.RequestCount = 2
	record.Occupancy = 7
	if err := instance.persistChildContext(value, "parent", record, nil); err != nil {
		t.Fatal(err)
	}
	summaryUsage := openrouter.Usage{PromptTokens: 6, CompletionTokens: 2, TotalTokens: 8}
	summary := newSummaryMessage("old facts")
	record.History = []openrouter.Message{prompt, summary, recentAssistant, recentTool}
	record.Usage = append(record.Usage, summaryUsage)
	record.Occupancy = 11
	if err := instance.persistChildContext(value, "parent", record, &compactionRecord{
		TurnID: "turn", ParentCallID: "parent", HeadEnd: 1, TailStart: 3,
		Summary: summary, Usage: &summaryUsage, Occupancy: 11,
	}); err != nil {
		t.Fatal(err)
	}

	restored, err := foldRecords(value.state.records)
	if err != nil {
		t.Fatal(err)
	}
	value.state = restored
	answer, delegation, err := instance.delegate(
		context.Background(), value, "parent", "inspect", nil,
		ClientFileSystem{}, ClientTerminal{}, make(chan event, 8),
		instance.trace.Turn(value.id, "turn"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if answer != "child done" || delegation.RequestCount != 3 || len(model.requests) != 1 {
		t.Fatalf("resumed child = %q, %#v, requests %d", answer, delegation, len(model.requests))
	}
	messages := model.requests[0].Messages[1:]
	if len(messages) != 4 || messages[1].Content[0].Text != summary.Content[0].Text ||
		messages[3].ToolCallID != "provider-recent" {
		t.Fatalf("resumed provider history = %#v", messages)
	}
	if err := instance.commit(value, recordModelExchange, modelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{parentCall},
		ToolResults: []storedToolResult{{
			CallID: "parent", Content: answer, Delegation: delegation,
		}},
		Usage: &openrouter.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}); err != nil {
		t.Fatal(err)
	}
	if err := instance.commit(value, recordTurnFinished, turnFinishedRecord{
		TurnID: "turn", Kind: "completed", StopReason: acp.StopReasonEndTurn,
	}); err != nil {
		t.Fatal(err)
	}
	if value.state.usage.input != 30 || value.state.usage.output != 11 {
		t.Fatalf("usage after child completion = %#v", value.state.usage)
	}
	updates, err := instance.replay(value.state)
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range updates {
		chunk, ok := update.(acp.AgentMessageChunk)
		if ok && (strings.Contains(chunk.Content.Text, "old result") ||
			strings.Contains(chunk.Content.Text, "recent result") ||
			strings.Contains(chunk.Content.Text, "old facts")) {
			t.Fatalf("replay exposed child provider history: %#v", chunk)
		}
	}
}

func TestLoopDoesNotDispatchCallsFromIncompleteCompletions(t *testing.T) {
	for _, test := range []struct {
		name       string
		finish     string
		stopReason acp.StopReason
		history    int
	}{
		{name: "length", finish: "length", stopReason: acp.StopReasonMaxTokens, history: 2},
		{
			name:       "content filter",
			finish:     "content_filter",
			stopReason: acp.StopReasonRefusal,
			history:    0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var executions int
			model := &staticCompletionModel{completion: &openrouter.Completion{
				Text:         "partial",
				FinishReason: test.finish,
				ToolCalls:    []openrouter.ToolCall{toolCall("call", "unsafe")},
			}}
			instance, err := New(Config{
				Logger:        discardLogger(),
				ModelOverride: "model",
				Client:        model,
				Tools: []Tool{{
					Name:     "unsafe",
					Approval: ApprovalNone,
					Execute: func(
						context.Context,
						Invocation,
					) (string, error) {
						executions++
						return "executed", nil
					},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			value := durableTestSession(t, instance, requestConfiguration{
				Settings: settings.Resolved{Model: "model"},
				Tools:    instance.primaryTools.modelTools,
			}, "turn-finish")
			events := make(chan event)
			go func() {
				for range events {
				}
			}()
			outcome := instance.run(
				context.Background(),
				value,
				&activeTurn{turnID: "turn-finish"},
				nil,
				ClientFileSystem{},
				ClientTerminal{},
				events,
			)
			close(events)

			if executions != 0 {
				t.Fatal("incomplete tool call was executed")
			}
			if outcome.response.StopReason != test.stopReason {
				t.Fatalf("stop reason = %q", outcome.response.StopReason)
			}
			if len(value.state.history) != test.history {
				t.Fatalf("history length = %d", len(value.state.history))
			}
			if test.history != 0 && len(value.state.history[1].ToolCalls) != 0 {
				t.Fatal("incomplete tool call was committed to history")
			}
		})
	}
}

func TestNewSessionValidatesCapabilitiesAndConfiguration(t *testing.T) {
	instance, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = instance.NewSession(context.Background(), acp.NewSessionRequest{CWD: "relative"})
	if err == nil {
		t.Fatal("relative cwd was accepted")
	}
	_, err = instance.NewSession(context.Background(), acp.NewSessionRequest{
		CWD:        t.TempDir(),
		MCPServers: []json.RawMessage{},
	})
	assertErrorCode(t, err, acp.ErrCodeAuthRequired)

	t.Setenv("OX_KEYRING_DISABLED", "1")
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	store := credentials.NewStore(discardLogger())
	instance, err = New(Config{Logger: discardLogger(), Credentials: store})
	if err != nil {
		t.Fatal(err)
	}
	_, err = instance.NewSession(context.Background(), validNewSessionRequest(t))
	assertErrorCode(t, err, jrpc2.InternalError)
	if !strings.Contains(err.Error(), "OX_MODEL") {
		t.Fatalf("configuration error = %v", err)
	}
}

func TestNewSessionResolvesBothSettingsLayers(t *testing.T) {
	workspace := t.TempDir()
	global := writeSettings(t, filepath.Join(t.TempDir(), "settings.json"), `{
		"model": "global/model",
		"temperature": 0.2,
		"max_tokens": 256,
		"provider": {"order": ["alpha"]}
	}`)
	writeSettings(t, settings.WorkspacePath(workspace), `{"temperature": 0.9}`)
	instance := settingsAgent(t, &staticCompletionModel{}, global, "")

	resolved := instance.mustResolve(t, newSessionRequest(workspace, nil))
	if resolved.Model != "global/model" || resolved.ModelSource != settings.SourceGlobal {
		t.Fatalf("model = %q from %q", resolved.Model, resolved.ModelSource)
	}
	if resolved.Temperature == nil || *resolved.Temperature != 0.9 {
		t.Fatalf("temperature = %v", resolved.Temperature)
	}
	if resolved.MaxTokens == nil || *resolved.MaxTokens != 256 {
		t.Fatalf("max tokens = %v", resolved.MaxTokens)
	}
	if resolved.Provider == nil || strings.Join(resolved.Provider.Order, ",") != "alpha" {
		t.Fatalf("provider = %#v", resolved.Provider)
	}

	writeSettings(t, settings.WorkspacePath(workspace), `{"model": "workspace/model"}`)
	resolved = instance.mustResolve(t, newSessionRequest(workspace, nil))
	if resolved.Model != "workspace/model" ||
		resolved.ModelSource != settings.SourceWorkspace {
		t.Fatalf("workspace model = %q from %q", resolved.Model, resolved.ModelSource)
	}

	override := settingsAgent(t, &staticCompletionModel{}, global, "environment/model")
	resolved = override.mustResolve(t, newSessionRequest(workspace, nil))
	if resolved.Model != "environment/model" ||
		resolved.ModelSource != settings.SourceEnvironment {
		t.Fatalf("overridden model = %q from %q", resolved.Model, resolved.ModelSource)
	}
}

func TestSessionActivationFreezesExecutorCapabilities(t *testing.T) {
	instance := settingsAgent(t, &staticCompletionModel{}, "", "test/model")
	if _, err := instance.Initialize(context.Background(), acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion,
	}); err != nil {
		t.Fatal(err)
	}
	request := validNewSessionRequest(t)
	created, err := instance.NewSession(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	value := instance.findSession(created.SessionID)
	if value.state.configuration.ExecutorCapabilities != (executorCapabilities{}) {
		t.Fatalf("new-session capabilities = %#v", value.state.configuration.ExecutorCapabilities)
	}
	if _, err := instance.CloseSession(context.Background(), acp.CloseSessionRequest{
		SessionID: created.SessionID,
	}); err != nil {
		t.Fatal(err)
	}

	delegated := &acp.ClientCapabilities{
		FS:       &acp.FileSystemCapabilities{ReadTextFile: true},
		Terminal: true,
	}
	if _, err := instance.Initialize(context.Background(), acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion,
		ClientCapabilities: delegated,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.ResumeSession(context.Background(), acp.ResumeSessionRequest{
		SessionID: created.SessionID,
		CWD:       request.CWD,
	}); err != nil {
		t.Fatal(err)
	}
	value = instance.findSession(created.SessionID)
	want := executorCapabilities{FileSystemRead: true, Terminal: true}
	if value.state.configuration.ExecutorCapabilities != want {
		t.Fatalf("reactivated capabilities = %#v, want %#v",
			value.state.configuration.ExecutorCapabilities, want)
	}
	if countRecords(value.state.records, recordConfigChanged) != 1 {
		t.Fatalf("configuration changes = %d, want 1",
			countRecords(value.state.records, recordConfigChanged))
	}
	if _, err := instance.CloseSession(context.Background(), acp.CloseSessionRequest{
		SessionID: created.SessionID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.ResumeSession(context.Background(), acp.ResumeSessionRequest{
		SessionID: created.SessionID,
		CWD:       request.CWD,
	}); err != nil {
		t.Fatal(err)
	}
	value = instance.findSession(created.SessionID)
	if countRecords(value.state.records, recordConfigChanged) != 1 {
		t.Fatalf("same-capability configuration changes = %d, want 1",
			countRecords(value.state.records, recordConfigChanged))
	}
}

func TestPromptExecutorsUseFrozenSessionCapabilitiesIndependently(t *testing.T) {
	instance, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	instance.clientFS = acp.FileSystemCapabilities{
		ReadTextFile:  true,
		WriteTextFile: true,
	}
	instance.clientTerminal = true

	tests := []struct {
		name         string
		capabilities executorCapabilities
		read         bool
		write        bool
		terminal     bool
	}{
		{name: "local"},
		{name: "filesystem read", capabilities: executorCapabilities{FileSystemRead: true}, read: true},
		{name: "filesystem write", capabilities: executorCapabilities{FileSystemWrite: true}, write: true},
		{name: "terminal", capabilities: executorCapabilities{Terminal: true}, terminal: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := &session{id: "session", state: durableState{
				configuration: requestConfiguration{ExecutorCapabilities: test.capabilities},
			}}
			fileSystem, terminal := instance.promptExecutors(nil, value)
			if (fileSystem.ReadTextFile != nil) != test.read ||
				(fileSystem.WriteTextFile != nil) != test.write ||
				terminal.Available() != test.terminal {
				t.Fatalf("executors = read %t, write %t, terminal %t",
					fileSystem.ReadTextFile != nil,
					fileSystem.WriteTextFile != nil,
					terminal.Available())
			}
		})
	}
}

func countRecords(records []sessionRecord, kind string) int {
	var count int
	for _, record := range records {
		if record.Type == kind {
			count++
		}
	}
	return count
}

func TestNewSessionValidatesSettingsAgainstTheCatalog(t *testing.T) {
	reasoningModel := func(mandatory bool, efforts ...string) *openrouter.Model {
		return &openrouter.Model{
			ID:            "reasoning/model",
			ContextLength: 100,
			Reasoning: &openrouter.ModelReasoning{
				Mandatory:        mandatory,
				SupportedEfforts: efforts,
			},
		}
	}
	for _, test := range []struct {
		name     string
		body     string
		client   *staticCompletionModel
		contains []string
	}{
		{
			name: "unknown model",
			body: `{"model": "nope/nope"}`,
			client: &staticCompletionModel{
				entryErr: fmt.Errorf("%w: nope/nope", openrouter.ErrUnknownModel),
			},
			contains: []string{"nope/nope", string(settings.SourceGlobal)},
		},
		{
			name:     "reasoning on a model without reasoning",
			body:     `{"model": "plain/model", "reasoning": {"effort": "high"}}`,
			client:   &staticCompletionModel{},
			contains: []string{"plain/model", "does not support reasoning"},
		},
		{
			name:     "unsupported effort",
			body:     `{"model": "reasoning/model", "reasoning": {"effort": "medium"}}`,
			client:   &staticCompletionModel{entry: reasoningModel(false, "high", "low")},
			contains: []string{"medium", "high, low"},
		},
		{
			name:     "reasoning disabled on a mandatory model",
			body:     `{"model": "reasoning/model", "reasoning": {"enabled": false}}`,
			client:   &staticCompletionModel{entry: reasoningModel(true)},
			contains: []string{"reasoning/model", "cannot be false"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			global := writeSettings(t, filepath.Join(t.TempDir(), "settings.json"), test.body)
			instance := settingsAgent(t, test.client, global, "")

			_, err := instance.NewSession(context.Background(), validNewSessionRequest(t))
			assertErrorCode(t, err, jrpc2.InternalError)
			for _, want := range test.contains {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want mention of %q", err, want)
				}
			}
			if len(instance.sessions) != 0 {
				t.Fatalf("sessions = %#v", instance.sessions)
			}
		})
	}

	t.Run("effort accepted when the model publishes no vocabulary", func(t *testing.T) {
		global := writeSettings(
			t,
			filepath.Join(t.TempDir(), "settings.json"),
			`{"model": "reasoning/model", "reasoning": {"effort": "high"}}`,
		)
		client := &staticCompletionModel{entry: reasoningModel(false)}
		instance := settingsAgent(t, client, global, "")

		resolved := instance.mustResolve(t, validNewSessionRequest(t))
		if resolved.Reasoning == nil || resolved.Reasoning.Effort != "high" {
			t.Fatalf("reasoning = %#v", resolved.Reasoning)
		}
	})
}

func TestNewSessionReportsAuthenticationBeforeSettings(t *testing.T) {
	global := writeSettings(
		t,
		filepath.Join(t.TempDir(), "settings.json"),
		`{"models": "typo/model"}`,
	)
	store := emptyCredentialStore(t)
	instance, err := New(Config{
		Logger:       discardLogger(),
		Credentials:  store,
		SettingsPath: global,
		Client:       &staticCompletionModel{},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = instance.NewSession(context.Background(), validNewSessionRequest(t))
	assertErrorCode(t, err, acp.ErrCodeAuthRequired)
}

func TestNewSessionReportsTheFileThatFailedToParse(t *testing.T) {
	workspace := t.TempDir()
	path := writeSettings(t, settings.WorkspacePath(workspace), `{"models": "typo/model"}`)
	instance := settingsAgent(t, &staticCompletionModel{}, "", "test/model")

	_, err := instance.NewSession(context.Background(), newSessionRequest(workspace, nil))
	assertErrorCode(t, err, jrpc2.InternalError)
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "models") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestConcurrentCloseHasOneOwnerAndCancelsActivePrompt(t *testing.T) {
	instance := settingsAgent(t, &staticCompletionModel{}, "", "test/model")
	created, err := instance.NewSession(context.Background(), validNewSessionRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	value := instance.findSession(created.SessionID)
	ctx, _, release, err := value.claim(context.Background(), "turn")
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		<-ctx.Done()
		release()
		close(released)
	}()

	errs := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			errs <- instance.closeActive(created.SessionID)
		}()
	}
	close(start)
	first, second := <-errs, <-errs
	<-released
	if (first == nil) == (second == nil) {
		t.Fatalf("close errors = %v, %v; want exactly one success", first, second)
	}
	if instance.findSession(created.SessionID) != nil {
		t.Fatal("closed session remained active")
	}
}

// mustResolve creates a session and returns the settings frozen onto it.
func (a *Agent) mustResolve(t *testing.T, request acp.NewSessionRequest) settings.Resolved {
	t.Helper()
	response, err := a.NewSession(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	value := a.findSession(response.SessionID)
	if value == nil {
		t.Fatalf("session %q was not registered", response.SessionID)
		return settings.Resolved{}
	}
	return value.state.configuration.Settings
}

func settingsAgent(t *testing.T, client Model, globalPath, modelOverride string) *Agent {
	t.Helper()
	t.Setenv("OX_KEYRING_DISABLED", "1")
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	instance, err := New(Config{
		Logger:        discardLogger(),
		Credentials:   credentials.NewStore(discardLogger()),
		ModelOverride: modelOverride,
		SettingsPath:  globalPath,
		Client:        client,
	})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func writeSettings(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func floatPointer(value float64) *float64 {
	return &value
}

func durableTestSession(
	t *testing.T,
	instance *Agent,
	configuration requestConfiguration,
	turnID string,
) *session {
	t.Helper()
	id, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	created, err := newRecord(1, recordSessionCreated, sessionCreated{
		SessionID:     id,
		CWD:           t.TempDir(),
		Configuration: configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := foldRecords([]sessionRecord{created})
	if err != nil {
		t.Fatal(err)
	}
	log, err := instance.store.create(id, created)
	if err != nil {
		t.Fatal(err)
	}
	value := &session{id: id, state: state, log: log}
	t.Cleanup(log.close)
	if err := instance.commit(value, recordUserMessage, userMessageRecord{
		TurnID:    turnID,
		MessageID: "message-" + turnID,
		Content:   []acp.ContentBlock{{Type: "text", Text: "prompt"}},
	}); err != nil {
		t.Fatal(err)
	}
	return value
}

func emptyCredentialStore(t *testing.T) *credentials.Store {
	t.Helper()
	keyring.MockInit()
	t.Setenv("OX_KEYRING_DISABLED", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	return credentials.NewStore(discardLogger())
}

// validNewSessionRequest names an empty workspace, so no stray settings file on
// the developer's machine can reach a test.
func validNewSessionRequest(t *testing.T) acp.NewSessionRequest {
	t.Helper()
	return newSessionRequest(t.TempDir(), nil)
}

func newSessionRequest(cwd string, meta acp.Metadata) acp.NewSessionRequest {
	return acp.NewSessionRequest{
		CWD:        cwd,
		MCPServers: []json.RawMessage{},
		Meta:       meta,
	}
}

func assertErrorCode(t *testing.T, err error, code jrpc2.Code) {
	t.Helper()
	var rpcError *jrpc2.Error
	if !errors.As(err, &rpcError) || rpcError.Code != code {
		t.Fatalf("error = %v, want code %d", err, code)
	}
}

func toolCall(id, name string) openrouter.ToolCall {
	return toolCallWithArguments(id, name, `{}`)
}

func toolCallWithArguments(id, name, arguments string) openrouter.ToolCall {
	return openrouter.ToolCall{
		ID:   id,
		Type: "function",
		Function: openrouter.ToolCallFunction{
			Name:      name,
			Arguments: arguments,
		},
	}
}

func TestSuccessfulToolResultSurvivesConcurrentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{{
			Name:         "mutation",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			Approval:     ApprovalNone,
			ParallelSafe: false,
			Execute: func(context.Context, Invocation) (string, error) {
				cancel()
				return "mutation completed", nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	results := instance.executeBatch(
		ctx,
		ephemeralToolSession(t),
		[]openrouter.ToolCall{toolCall("call", "mutation")},
		nil,
		make(chan event, 8),
	)
	if len(results) != 1 || results[0].failed ||
		results[0].content != "mutation completed" {
		t.Fatalf("result = %#v", results)
	}
}

func ephemeralToolSession(t *testing.T) *session {
	t.Helper()
	return &session{
		id: "test-session",
		state: durableState{
			cwd: t.TempDir(),
		},
	}
}

type repeatingToolModel struct {
	requests int
}

type capturingCompletionModel struct {
	completion *openrouter.Completion
	requests   []openrouter.Request
}

func (m *capturingCompletionModel) Stream(
	_ context.Context,
	request openrouter.Request,
	_ func(openrouter.Delta),
) (*openrouter.Completion, error) {
	m.requests = append(m.requests, request)
	return m.completion, nil
}

func (m *capturingCompletionModel) ModelInfo(
	_ context.Context,
	id string,
) (*openrouter.Model, error) {
	return &openrouter.Model{ID: id, ContextLength: 2400}, nil
}

// staticCompletionModel answers every request with one completion. entry and
// entryErr stand in for the catalog, so a test can present a model that declares
// reasoning, one that does not, or an id the catalog does not know.
type staticCompletionModel struct {
	completion *openrouter.Completion
	entry      *openrouter.Model
	entryErr   error
}

func (m *staticCompletionModel) Stream(
	context.Context,
	openrouter.Request,
	func(openrouter.Delta),
) (*openrouter.Completion, error) {
	return m.completion, nil
}

func (m *staticCompletionModel) ModelInfo(
	_ context.Context,
	id string,
) (*openrouter.Model, error) {
	if m.entryErr != nil {
		return nil, m.entryErr
	}
	if m.entry != nil {
		return m.entry, nil
	}
	return &openrouter.Model{ID: id, ContextLength: 100}, nil
}

func (m *repeatingToolModel) Stream(
	context.Context,
	openrouter.Request,
	func(openrouter.Delta),
) (*openrouter.Completion, error) {
	m.requests++
	return &openrouter.Completion{
		ToolCalls: []openrouter.ToolCall{
			toolCall(fmt.Sprintf("call-%d", m.requests), "again"),
		},
		FinishReason: "tool_calls",
	}, nil
}

func (*repeatingToolModel) ModelInfo(
	_ context.Context,
	id string,
) (*openrouter.Model, error) {
	return &openrouter.Model{ID: id, ContextLength: 100}, nil
}
