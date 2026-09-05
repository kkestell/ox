package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zalando/go-keyring"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
	diagnostictrace "github.com/kkestell/ox/internal/trace"
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
	if response.AgentCapabilities.MCPCapabilities == nil ||
		!response.AgentCapabilities.MCPCapabilities.HTTP ||
		response.AgentCapabilities.MCPCapabilities.SSE {
		t.Fatalf("MCP capabilities = %#v", response.AgentCapabilities.MCPCapabilities)
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

func TestMCPActivationBuildsSessionToolsAndKeepsSecretsOutOfState(t *testing.T) {
	var calls atomic.Int32
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{
		Name: "echo", Title: "Echo", Description: "echo input",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		calls.Add(1)
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: "from MCP"}},
		}, nil
	})
	server.AddTool(&sdk.Tool{
		Name: "fail", InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: "server refused"}},
			IsError: true,
		}, nil
	})
	server.AddTool(&sdk.Tool{
		Name: "large", InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: strings.Repeat("x", mcpInlineBytes+1)}},
		}, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(
		func(request *http.Request) *sdk.Server {
			if request.Header.Get("Authorization") != "secret-value" {
				t.Error("MCP request omitted configured header")
			}
			return server
		},
		&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true},
	))
	defer httpServer.Close()

	instance := settingsAgent(t, &staticCompletionModel{}, "", "test/model")
	request := validNewSessionRequest(t)
	request.MCPServers = []acp.MCPServer{{HTTP: &acp.MCPHTTPServer{
		Type: "http", Name: "fixture server", URL: httpServer.URL,
		Headers: []acp.HTTPHeader{{Name: "Authorization", Value: "secret-value"}},
	}}}
	created, err := instance.NewSession(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	value := instance.findSession(created.SessionID)
	const name = "mcp__fixture_server__echo"
	index, ok := value.primaryTools.byName[name]
	if !ok {
		t.Fatalf("primary tools = %#v", value.primaryTools.byName)
	}
	if _, ok := value.subagentTools.byName[name]; !ok {
		t.Fatalf("subagent tools = %#v", value.subagentTools.byName)
	}
	tool := value.primaryTools.tools[index]
	if tool.Approval != ApprovalAsk || tool.ParallelSafe ||
		toolSetTitle(value.primaryTools, name, nil) != "fixture server / echo (Echo)" {
		t.Fatalf("MCP tool = %#v", tool)
	}
	if len(value.state.configuration.MCPTools) != 3 ||
		value.state.configuration.MCPTools[0].Identity == "" {
		t.Fatalf("MCP evidence = %#v", value.state.configuration.MCPTools)
	}
	rule := tool.Suggest(nil)
	if rule == "" || !tool.Covered([]string{rule}, nil) || tool.Covered([]string{"other"}, nil) {
		t.Fatalf("MCP grant rule = %q", rule)
	}
	output, err := tool.Execute(context.Background(), Invocation{
		Arguments: json.RawMessage(`{}`), CallID: "mcp-call",
		SpillDir: instance.store.spillDir(created.SessionID),
	})
	if err != nil || output != "from MCP" || calls.Load() != 1 {
		t.Fatalf("MCP execution = %q, calls %d, error %v", output, calls.Load(), err)
	}
	failed := value.primaryTools.tools[value.primaryTools.byName["mcp__fixture_server__fail"]]
	_, err = failed.Execute(context.Background(), Invocation{
		Arguments: json.RawMessage(`{}`), CallID: "mcp-fail",
		SpillDir: instance.store.spillDir(created.SessionID),
	})
	if content, ok := toolResultContent(err); !ok || content != "server refused" {
		t.Fatalf("MCP error = %q, %v", content, err)
	}
	large := value.primaryTools.tools[value.primaryTools.byName["mcp__fixture_server__large"]]
	output, err = large.Execute(context.Background(), Invocation{
		Arguments: json.RawMessage(`{}`), CallID: "mcp-large",
		SpillDir: instance.store.spillDir(created.SessionID),
	})
	if err != nil || !strings.Contains(output, "full output at") {
		t.Fatalf("large MCP output = %q, %v", output, err)
	}
	spilled, err := os.ReadFile(filepath.Join(instance.store.spillDir(created.SessionID), "mcp-mcp-large.out"))
	if err != nil || string(spilled) != strings.Repeat("x", mcpInlineBytes+1) {
		t.Fatalf("large MCP spill = %d bytes, %v", len(spilled), err)
	}
	plan, err := applySelections(
		value.activationBase, sessionSelections{Mode: modePlan}, nil, value.models,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range plan.Tools {
		if strings.HasPrefix(declaration.Function.Name, "mcp__") {
			t.Fatalf("plan mode exposed MCP tool %q", declaration.Function.Name)
		}
	}
	raw, err := os.ReadFile(filepath.Join(instance.store.root, created.SessionID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-value") {
		t.Fatalf("session log leaked MCP secret: %s", raw)
	}
	if _, err := instance.CloseSession(context.Background(), acp.CloseSessionRequest{
		SessionID: created.SessionID,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveredMCPRequiresTheInterruptedDefinition(t *testing.T) {
	required := requestConfiguration{MCPTools: []mcpToolConfiguration{{
		Name: "mcp__server__tool", ServerName: "server", ToolName: "tool", Identity: "same",
	}}}
	calls := []openrouter.ToolCall{toolCall("call", "mcp__server__tool")}
	if err := validateRecoveredMCP(required, required, calls); err != nil {
		t.Fatalf("matching definition: %v", err)
	}
	changed := cloneConfiguration(required)
	changed.MCPTools[0].Identity = "changed"
	if err := validateRecoveredMCP(required, changed, calls); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed definition error = %v", err)
	}
	if err := validateRecoveredMCP(required, requestConfiguration{}, calls); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("missing definition error = %v", err)
	}
	if err := validateRecoveredMCP(required, requestConfiguration{}, []openrouter.ToolCall{toolCall("builtin", "read")}); err != nil {
		t.Fatalf("unrelated call: %v", err)
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

func TestLogoutRejectsCredentialFile(t *testing.T) {
	store := credentials.NewStore(discardLogger(), "file-key", true)
	instance, err := New(Config{Logger: discardLogger(), Credentials: store})
	if err != nil {
		t.Fatal(err)
	}

	_, err = instance.Logout(context.Background(), acp.LogoutRequest{})
	assertErrorCode(t, err, jrpc2.InternalError)
	if !strings.Contains(err.Error(), "--credential-file") {
		t.Fatalf("logout error = %v", err)
	}
	if store.Key() != "file-key" ||
		store.Source() != credentials.SourceCredentialFile {
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
		{Name: "todo", ParentOnly: true, PlanMode: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(instance.primaryTools.tools) != 3 ||
		len(instance.subagentTools.tools) != 1 ||
		instance.subagentTools.tools[0].Name != "read" {
		t.Fatalf(
			"primary = %#v, subagent = %#v",
			instance.primaryTools.tools,
			instance.subagentTools.tools,
		)
	}
}

func TestFormCapabilityFiltersParentChildAndPlanTools(t *testing.T) {
	instance, err := New(Config{Logger: discardLogger(), Tools: []Tool{
		{Name: "question", Kind: acp.ToolKindOther, RequiresForm: true, PlanMode: true},
		{Name: "read", Kind: acp.ToolKindRead, PlanMode: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, capabilities := range []*acp.ClientCapabilities{
		nil,
		{},
		{Elicitation: &acp.ElicitationCapabilities{}},
		{Elicitation: &acp.ElicitationCapabilities{URL: &acp.ElicitationURLCapabilities{}}},
	} {
		if _, err := instance.Initialize(context.Background(), acp.InitializeRequest{
			ProtocolVersion: acp.ProtocolVersion, ClientCapabilities: capabilities,
		}); err != nil {
			t.Fatal(err)
		}
		primary, child := instance.negotiatedToolSets()
		if _, exists := primary.byName["question"]; exists {
			t.Fatalf("question present for capabilities %#v", capabilities)
		}
		if _, exists := child.byName["question"]; exists {
			t.Fatalf("child question present for capabilities %#v", capabilities)
		}
		if configuredPlanTools(primary)["question"] {
			t.Fatalf("plan question present for capabilities %#v", capabilities)
		}
	}

	if _, err := instance.Initialize(context.Background(), acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion,
		ClientCapabilities: &acp.ClientCapabilities{Elicitation: &acp.ElicitationCapabilities{
			Form: &acp.ElicitationFormCapabilities{},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	primary, child := instance.negotiatedToolSets()
	if _, exists := primary.byName["question"]; !exists {
		t.Fatal("parent question omitted with form support")
	}
	if _, exists := child.byName["question"]; !exists {
		t.Fatal("child question omitted with form support")
	}
	if !configuredPlanTools(primary)["question"] {
		t.Fatal("question omitted from plan tools with form support")
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
	oldCall := openrouter.ToolCall{
		ID: "child-public-old", Type: "function",
		Function: openrouter.ToolCallFunction{Name: "read", Arguments: `{}`},
	}
	if err := instance.startToolExecution(value, "turn", "parent", oldCall, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := instance.completeToolExecution(value, "turn", "parent", oldCall.ID, storedToolResult{
		CallID: oldCall.ID, Content: "old result",
	}); err != nil {
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
	recentCall := openrouter.ToolCall{
		ID: "child-public-recent", Type: "function",
		Function: openrouter.ToolCallFunction{Name: "read", Arguments: `{}`},
	}
	if err := instance.startToolExecution(value, "turn", "parent", recentCall, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := instance.completeToolExecution(value, "turn", "parent", recentCall.ID, storedToolResult{
		CallID: recentCall.ID, Content: "recent result",
	}); err != nil {
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
		nil,
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
	if err := instance.startToolExecution(value, "turn", "", parentCall, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := instance.completeToolExecution(value, "turn", "", parentCall.ID, storedToolResult{
		CallID: parentCall.ID, Content: answer, Delegation: delegation,
	}); err != nil {
		t.Fatal(err)
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
		MCPServers: []acp.MCPServer{},
	})
	assertErrorCode(t, err, acp.ErrCodeAuthRequired)

	store := credentials.NewStore(discardLogger(), "test-key", true)
	instance, err = New(Config{Logger: discardLogger(), Credentials: store})
	if err != nil {
		t.Fatal(err)
	}
	_, err = instance.NewSession(context.Background(), validNewSessionRequest(t))
	assertErrorCode(t, err, jrpc2.InternalError)
	if !strings.Contains(err.Error(), "--model") {
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
		resolved.ModelSource != settings.SourceCLI {
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
			name: "model without context length",
			body: `{"model": "plain/model"}`,
			client: &staticCompletionModel{entry: &openrouter.Model{
				ID: "plain/model",
			}},
			contains: []string{"plain/model", "no positive context length", "choose a model"},
		},
		{
			name: "model with nonpositive context length",
			body: `{"model": "plain/model"}`,
			client: &staticCompletionModel{entry: &openrouter.Model{
				ID: "plain/model", ContextLength: -1,
				TopProvider: openrouter.TopProvider{ContextLength: -2},
			}},
			contains: []string{"plain/model", "no positive context length", "choose a model"},
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

func TestCloseWaitsForClaimedConfigurationChange(t *testing.T) {
	instance := settingsAgent(t, &staticCompletionModel{}, "", "test/model")
	created, err := instance.NewSession(context.Background(), validNewSessionRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	value := instance.findSession(created.SessionID)
	release, err := value.claimConfigChange()
	if err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() { closed <- instance.closeActive(created.SessionID) }()
	deadline := time.Now().Add(time.Second)
	for {
		value.mu.Lock()
		closing := value.closing
		value.mu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("close did not claim the session")
		}
		runtime.Gosched()
	}
	select {
	case err := <-closed:
		t.Fatalf("close returned before configuration change finished: %v", err)
	default:
	}
	release()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err := value.claimConfigChange(); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("claim after close error = %v", err)
	}
}

func TestResumeValidatesDurableModelSelectionInsteadOfBaseModel(t *testing.T) {
	global := filepath.Join(t.TempDir(), "settings.json")
	writeSettings(t, global, `{"model":"base/model"}`)
	model := &catalogCompletionModel{models: []openrouter.Model{
		*testModel("base/model", 100),
		*testModel("selected/model", 200),
	}}
	instance := settingsAgent(t, model, global, "")
	request := validNewSessionRequest(t)
	created, err := instance.NewSession(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	value := instance.findSession(created.SessionID)
	selections := sessionSelections{Model: "selected/model"}
	next, err := applySelections(value.activationBase, selections, nil, value.models)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.commit(value, recordOptionChanged, optionChanged{
		Selections: selections, Configuration: next,
		Options: buildConfigOptions(next, value.models),
	}); err != nil {
		t.Fatal(err)
	}
	if err := instance.closeActive(created.SessionID); err != nil {
		t.Fatal(err)
	}

	writeSettings(t, global, `{"model":"missing/model"}`)
	model.models = []openrouter.Model{*testModel("selected/model", 200)}
	if _, err := instance.ResumeSession(context.Background(), acp.ResumeSessionRequest{
		SessionID: created.SessionID,
		CWD:       request.CWD,
	}); err != nil {
		t.Fatal(err)
	}
	resumed := instance.findSession(created.SessionID)
	if got := resumed.state.configuration.Settings; got.Model != "selected/model" ||
		got.ModelSource != settings.SourceSession {
		t.Fatalf("resumed settings = %#v", got)
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
	instance, err := New(Config{
		Logger:        discardLogger(),
		Credentials:   credentials.NewStore(discardLogger(), "test-key", true),
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
	return credentials.NewStore(discardLogger(), "", false)
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
		MCPServers: []acp.MCPServer{},
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

func TestToolDispatchPersistenceFailureSkipsExecutor(t *testing.T) {
	var executions atomic.Int32
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{{
			Name: "mutation", InputSchema: json.RawMessage(`{"type":"object"}`),
			Approval: ApprovalNone,
			Execute: func(context.Context, Invocation) (string, error) {
				executions.Add(1)
				return "changed", nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		Tools:    cloneTools(instance.primaryTools.modelTools),
	}
	value := durableTestSession(t, instance, configuration, "turn")
	call := toolCall("call", "mutation")
	if err := instance.commit(value, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{call}, RequestCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	value.log.close()
	_, err = instance.dispatchApprovedBatch(
		context.Background(), value, instance.primaryTools, value.primaryFileReads(),
		[]openrouter.ToolCall{call}, nil, nil, ClientFileSystem{}, ClientTerminal{},
		make(chan event, 8), "", []toolResult{{}}, []bool{true}, diagnostictrace.Turn{},
	)
	if err == nil || !strings.Contains(err.Error(), "persist tool dispatch") {
		t.Fatalf("dispatch error = %v", err)
	}
	if executions.Load() != 0 {
		t.Fatalf("executions = %d", executions.Load())
	}
}

func TestTodoPersistenceFailureEmitsNoPlanOrSuccessfulResult(t *testing.T) {
	var value *session
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{
			{
				Name: "todo", InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval: ApprovalNone, ParentOnly: true, PlanMode: true,
				Execute: func(_ context.Context, invocation Invocation) (string, error) {
					value.log.close()
					if err := invocation.ReplaceTodo([]acp.PlanEntry{{
						Content: "must persist", Priority: acp.PlanEntryPriorityMedium,
						Status: acp.PlanEntryStatusPending,
					}}); err != nil {
						return "", err
					}
					return "updated", nil
				},
			},
			{Name: "read", Kind: acp.ToolKindRead, Approval: ApprovalNone},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		Tools:    cloneTools(instance.primaryTools.modelTools),
		ToolKinds: map[string]acp.ToolKind{
			"todo": acp.ToolKindOther, "read": acp.ToolKindRead,
		},
		PlanTools: map[string]bool{"todo": true, "read": true},
	}
	value = durableTestSession(t, instance, configuration, "turn")
	call := toolCall("todo-call", "todo")
	if err := instance.commit(value, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{call}, RequestCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	events := make(chan event, 8)
	_, err = instance.dispatchApprovedBatch(
		context.Background(), value, instance.primaryTools, value.primaryFileReads(),
		[]openrouter.ToolCall{call}, nil, nil, ClientFileSystem{}, ClientTerminal{}, events,
		"", []toolResult{{}}, []bool{true}, diagnostictrace.Turn{},
	)
	if err == nil || !value.poisoned || value.state.todo != nil {
		t.Fatalf("dispatch = error %v, poisoned %v, todo %#v", err, value.poisoned, value.state.todo)
	}
	close(events)
	for current := range events {
		if current.kind == eventPlan {
			t.Fatal("failed todo persistence emitted a plan update")
		}
	}
	execution, ok := value.state.toolExecutions[call.ID]
	if !ok || execution.Result != nil {
		t.Fatalf("durable execution = %#v, present %v", execution, ok)
	}
}

func TestToolCompletionPersistenceFailureStopsLaterSibling(t *testing.T) {
	var first, siblingCancelled, later atomic.Int32
	siblingStarted := make(chan struct{})
	var value *session
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{
			{
				Name: "first", InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval: ApprovalNone, ParallelSafe: true,
				Execute: func(context.Context, Invocation) (string, error) {
					first.Add(1)
					<-siblingStarted
					value.log.close()
					return "effect happened", nil
				},
			},
			{
				Name: "sibling", InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval: ApprovalNone, ParallelSafe: true,
				Execute: func(ctx context.Context, _ Invocation) (string, error) {
					close(siblingStarted)
					<-ctx.Done()
					siblingCancelled.Add(1)
					return "", ctx.Err()
				},
			},
			{
				Name: "later", InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval: ApprovalNone,
				Execute: func(context.Context, Invocation) (string, error) {
					later.Add(1)
					return "should not happen", nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		Tools:    cloneTools(instance.primaryTools.modelTools),
	}
	value = durableTestSession(t, instance, configuration, "turn")
	calls := []openrouter.ToolCall{
		toolCall("first-call", "first"), toolCall("sibling-call", "sibling"),
		toolCall("later-call", "later"),
	}
	if err := instance.commit(value, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: calls, RequestCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = instance.dispatchApprovedBatch(
		context.Background(), value, instance.primaryTools, value.primaryFileReads(),
		calls, nil, nil, ClientFileSystem{}, ClientTerminal{}, make(chan event, 16), "",
		make([]toolResult, len(calls)), []bool{true, true, true}, diagnostictrace.Turn{},
	)
	if err == nil || !strings.Contains(err.Error(), "persist tool completion") {
		t.Fatalf("completion error = %v", err)
	}
	if first.Load() != 1 || siblingCancelled.Load() != 1 || later.Load() != 0 || !value.poisoned {
		t.Fatalf(
			"executions = first %d sibling cancelled %d later %d, poisoned = %v",
			first.Load(), siblingCancelled.Load(), later.Load(), value.poisoned,
		)
	}
	other := durableTestSession(t, instance, configuration, "other-turn")
	otherCall := toolCall("other-call", "later")
	if err := instance.commit(other, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "other-turn", AnswerID: "other-answer", ThoughtID: "other-thought",
		FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{otherCall}, RequestCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.dispatchApprovedBatch(
		context.Background(), other, instance.primaryTools, other.primaryFileReads(),
		[]openrouter.ToolCall{otherCall}, nil, nil, ClientFileSystem{}, ClientTerminal{},
		make(chan event, 8), "", []toolResult{{}}, []bool{true}, diagnostictrace.Turn{},
	); err != nil {
		t.Fatal(err)
	}
	if later.Load() != 1 {
		t.Fatalf("unrelated session executions = %d", later.Load())
	}
}

func TestInterruptedStartedToolBecomesUnknownWithoutExecution(t *testing.T) {
	var executions atomic.Int32
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{{
			Name: "mutation", InputSchema: json.RawMessage(`{"type":"object"}`),
			Approval: ApprovalNone,
			Execute: func(context.Context, Invocation) (string, error) {
				executions.Add(1)
				return "repeated", nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		Tools:    cloneTools(instance.primaryTools.modelTools),
	}
	value := durableTestSession(t, instance, configuration, "turn")
	call := toolCall("call", "mutation")
	if err := instance.commit(value, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{call}, RequestCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := instance.startToolExecution(value, "turn", "", call, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := instance.interruptOpenTurn(value); err != nil {
		t.Fatal(err)
	}
	if executions.Load() != 0 || value.state.openTurn != "" || len(value.state.history) != 3 {
		t.Fatalf("executions = %d, state = %#v", executions.Load(), value.state)
	}
	result := value.state.history[2]
	if result.Role != openrouter.RoleTool || result.Content[0].Text != unknownToolOutcome {
		t.Fatalf("unknown result = %#v", result)
	}
}

func TestInterruptedQuestionIsRecordedWithoutReissue(t *testing.T) {
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{{
			Name: "question", InputSchema: json.RawMessage(`{"type":"object"}`),
			Approval: ApprovalNone, RequiresForm: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		Tools:    cloneTools(instance.primaryTools.modelTools),
	}
	value := durableTestSession(t, instance, configuration, "turn")
	call := toolCall("question-call", "question")
	if err := instance.commit(value, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{call}, RequestCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := instance.startToolExecution(value, "turn", "", call, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := instance.interruptOpenTurn(value); err != nil {
		t.Fatal(err)
	}
	result := value.state.history[len(value.state.history)-1]
	if result.Role != openrouter.RoleTool || result.Content[0].Text != interruptedQuestion {
		t.Fatalf("interrupted result = %#v", result)
	}
	var completion toolCompletedRecord
	for _, record := range value.state.records {
		if record.Type == recordToolCompleted {
			if err := decodeRecord(record.Data, &completion); err != nil {
				t.Fatal(err)
			}
		}
	}
	if completion.CallID != call.ID || completion.Result.Unknown ||
		!completion.Result.Failed || completion.Result.Content != interruptedQuestion {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestRecoveredPermissionDoesNotRedispatchStartedSibling(t *testing.T) {
	var first, second atomic.Int32
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{
			{
				Name: "first", InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval: ApprovalNone, ParallelSafe: true,
				Execute: func(context.Context, Invocation) (string, error) {
					first.Add(1)
					return "repeated", nil
				},
			},
			{
				Name: "second", InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval: ApprovalAsk, ParallelSafe: true,
				Execute: func(context.Context, Invocation) (string, error) {
					second.Add(1)
					return "second done", nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		Tools:    cloneTools(instance.primaryTools.modelTools),
	}
	value := durableTestSession(t, instance, configuration, "turn")
	calls := []openrouter.ToolCall{toolCall("first-call", "first"), toolCall("second-call", "second")}
	if err := instance.commit(value, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: calls, RequestCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := instance.startToolExecution(value, "turn", "", calls[0], "", ""); err != nil {
		t.Fatal(err)
	}
	secondTool := instance.primaryTools.tools[instance.primaryTools.byName["second"]]
	request := instance.permissionRequest(value.id, value.state.cwd, secondTool, calls[1], "", "", "")
	if err := instance.commit(value, recordPermissionOpen, permissionRequestedRecord{
		TurnID:  "turn",
		Pending: pendingPermissionRecord{CallID: calls[1].ID, Generation: 1, Request: request},
	}); err != nil {
		t.Fatal(err)
	}
	results, cancelled, err := instance.executeSuspendedBatch(
		context.Background(), value, calls,
		func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
				Outcome: "selected", OptionID: permissionAllowOnceID,
			}}, nil
		},
		nil,
		ClientFileSystem{}, ClientTerminal{}, make(chan event, 32),
		diagnostictrace.Turn{}, true,
	)
	if err != nil || cancelled {
		t.Fatalf("recovered batch = cancelled %v, error %v", cancelled, err)
	}
	if first.Load() != 0 || second.Load() != 1 || len(results) != 2 ||
		!results[0].unknown || results[0].content != unknownToolOutcome ||
		results[1].failed || results[1].content != "second done" {
		t.Fatalf(
			"executions = first %d second %d, results = %#v",
			first.Load(), second.Load(), results,
		)
	}
}

func TestRecoveredPermissionCommitsUnknownDelegatingSibling(t *testing.T) {
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{
			{
				Name: "task", InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval: ApprovalNone, ParallelSafe: true, Delegates: true,
				Label: func(json.RawMessage) string { return "Task" },
			},
			{
				Name: "second", InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval: ApprovalAsk, ParallelSafe: true,
				Execute: func(context.Context, Invocation) (string, error) {
					return "second done", nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		Tools:    cloneTools(instance.primaryTools.modelTools),
		Subagent: subagentConfiguration{
			SystemPrompt: "child",
			Tools:        cloneTools(instance.subagentTools.modelTools),
		},
	}
	value := durableTestSession(t, instance, configuration, "turn")
	calls := []openrouter.ToolCall{
		toolCall("task-call", "task"),
		toolCall("second-call", "second"),
	}
	if err := instance.commit(value, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: calls, RequestCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := instance.startToolExecution(value, "turn", "", calls[0], "", ""); err != nil {
		t.Fatal(err)
	}
	if err := instance.persistChildContext(value, calls[0].ID, delegationRecord{
		Prompt: "inspect", History: []openrouter.Message{
			textMessage(openrouter.RoleUser, "inspect"),
		},
	}, nil); err != nil {
		t.Fatal(err)
	}
	secondTool := instance.primaryTools.tools[instance.primaryTools.byName["second"]]
	request := instance.permissionRequest(value.id, value.state.cwd, secondTool, calls[1], "", "", "")
	if err := instance.commit(value, recordPermissionOpen, permissionRequestedRecord{
		TurnID:  "turn",
		Pending: pendingPermissionRecord{CallID: calls[1].ID, Generation: 1, Request: request},
	}); err != nil {
		t.Fatal(err)
	}
	results, cancelled, err := instance.executeSuspendedBatch(
		context.Background(), value, calls,
		func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
				Outcome: "selected", OptionID: permissionAllowOnceID,
			}}, nil
		},
		nil,
		ClientFileSystem{}, ClientTerminal{}, make(chan event, 32),
		diagnostictrace.Turn{}, true,
	)
	if err != nil || cancelled {
		t.Fatalf("recovered batch = cancelled %v, error %v", cancelled, err)
	}
	stored := make([]storedToolResult, len(results))
	for index := range results {
		stored[index] = storedResult(calls[index].ID, results[index])
	}
	if stored[0].Delegation == nil || !stored[0].Unknown {
		t.Fatalf("delegating sibling result = %#v", stored[0])
	}
	if err := instance.commit(value, recordModelExchange, modelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: calls, ToolResults: stored,
	}); err != nil {
		t.Fatalf("persist recovered exchange: %v", err)
	}
}

func TestInterruptedBatchPreservesCompletedAndClassifiesRemainingCalls(t *testing.T) {
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{
			{Name: "completed", InputSchema: json.RawMessage(`{"type":"object"}`), Approval: ApprovalNone},
			{Name: "started", InputSchema: json.RawMessage(`{"type":"object"}`), Approval: ApprovalNone},
			{Name: "untouched", InputSchema: json.RawMessage(`{"type":"object"}`), Approval: ApprovalNone},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
		Tools:    cloneTools(instance.primaryTools.modelTools),
	}
	value := durableTestSession(t, instance, configuration, "turn")
	calls := []openrouter.ToolCall{
		toolCall("completed-call", "completed"),
		toolCall("started-call", "started"),
		toolCall("untouched-call", "untouched"),
	}
	if err := instance.commit(value, recordExchangePaused, suspendedModelExchangeRecord{
		TurnID: "turn", AnswerID: "answer", ThoughtID: "thought",
		FinishReason: "tool_calls", ToolCalls: calls, RequestCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls[:2] {
		if err := instance.startToolExecution(value, "turn", "", call, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := instance.completeToolExecution(
		value, "turn", "", calls[0].ID,
		storedToolResult{CallID: calls[0].ID, Content: "durable result"},
	); err != nil {
		t.Fatal(err)
	}
	if err := instance.interruptOpenTurn(value); err != nil {
		t.Fatal(err)
	}
	if len(value.state.history) != 5 {
		t.Fatalf("history = %#v", value.state.history)
	}
	got := []string{
		value.state.history[2].Content[0].Text,
		value.state.history[3].Content[0].Text,
		value.state.history[4].Content[0].Text,
	}
	want := []string{"durable result", unknownToolOutcome, interruptedBeforeStart}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool results = %v, want %v", got, want)
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
	return testModel(id, 2400), nil
}

// staticCompletionModel answers every request with one completion. entry and
// entryErr stand in for the catalog, so a test can present a model that declares
// reasoning, one that does not, or an id the catalog does not know.
type staticCompletionModel struct {
	completion *openrouter.Completion
	entry      *openrouter.Model
	entryErr   error
}

type catalogCompletionModel struct {
	staticCompletionModel
	models []openrouter.Model
}

func (m *catalogCompletionModel) Models(context.Context) ([]openrouter.Model, error) {
	return cloneModels(m.models), nil
}

func (m *catalogCompletionModel) ModelInfo(_ context.Context, id string) (*openrouter.Model, error) {
	for index := range m.models {
		if m.models[index].ID == id {
			entry := m.models[index]
			return &entry, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", openrouter.ErrUnknownModel, id)
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
	return testModel(id, 100), nil
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
	return testModel(id, 100), nil
}

func testModel(id string, contextLength int) *openrouter.Model {
	return &openrouter.Model{
		ID: id, ContextLength: contextLength,
		SupportedParameters: []string{"tools", "temperature", "max_tokens"},
		Architecture:        openrouter.Architecture{InputModalities: []string{"text", "image", "audio"}},
	}
}
