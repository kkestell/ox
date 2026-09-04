package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/server"
	"github.com/zalando/go-keyring"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
	oxtools "github.com/kkestell/ox/internal/tools"
)

func TestPromptStreamsAndReplaysCompletedHistory(t *testing.T) {
	var systemPrompt string
	model := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			onDelta func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			systemPrompt = request.Messages[0].Content[0].Text
			if len(messages) != 1 || request.SessionID == "" {
				return nil, errors.New("first request did not contain one user message and a session ID")
			}
			if request.CacheControl == nil || request.CacheControl.Type != "ephemeral" {
				return nil, errors.New("first request omitted ephemeral cache control")
			}
			onDelta(openrouter.Delta{Kind: openrouter.DeltaReasoning, Text: "thinking"})
			onDelta(openrouter.Delta{Kind: openrouter.DeltaText, Text: "answer"})
			return completion("answer"), nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if request.Messages[0].Content[0].Text != systemPrompt {
				return nil, errors.New("system prompt changed within the session activation")
			}
			if len(messages) != 3 {
				return nil, errors.New("second request did not replay user, assistant, user")
			}
			if messages[1].Role != openrouter.RoleAssistant ||
				messages[1].Content[0].Text != "answer" {
				return nil, errors.New("completed assistant response was not replayed")
			}
			return completion("second"), nil
		},
	}}
	harness := newAgentHarness(t, model, nil)
	sessionID := harness.newSession(t)

	first := harness.prompt(t, sessionID, "first")
	if first.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", first.StopReason)
	}
	second := harness.prompt(t, sessionID, "second")
	if second.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", second.StopReason)
	}
	if second.Usage == nil || second.Usage.TotalTokens != 24 {
		t.Fatalf("cumulative usage = %#v", second.Usage)
	}

	updates := harness.updates()
	var answerID, thoughtID string
	var sawUsage bool
	for _, update := range updates {
		switch update.discriminator(t) {
		case "agent_message_chunk":
			var chunk acp.AgentMessageChunk
			update.decode(t, &chunk)
			answerID = chunk.MessageID
		case "agent_thought_chunk":
			var chunk acp.AgentThoughtChunk
			update.decode(t, &chunk)
			thoughtID = chunk.MessageID
		case "usage_update":
			var usage acp.UsageUpdate
			update.decode(t, &usage)
			sawUsage = usage.Used == 12 && usage.Size == 128_000 &&
				usage.Cost != nil && usage.Cost.Currency == "USD"
		}
	}
	if answerID == "" || thoughtID == "" || answerID == thoughtID {
		t.Fatalf("answer message ID %q, thought message ID %q", answerID, thoughtID)
	}
	if !sawUsage {
		t.Fatal("usage update was not observed")
	}
	model.assertConsumed(t)
}

func TestPromptFailureReportsAndReplaysTheModelError(t *testing.T) {
	sessionDir := t.TempDir()
	cwd := t.TempDir()
	model := &scriptedModel{scripts: []modelScript{
		func(
			context.Context,
			openrouter.Request,
			func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			return nil, errors.New("provider rejected tool parameters")
		},
	}}
	first := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		SessionDir:    sessionDir,
	})
	sessionID := first.newSessionIn(t, cwd, nil)
	if _, err := first.callPrompt(sessionID, "hello"); err == nil ||
		!strings.Contains(err.Error(), "provider rejected tool parameters") {
		t.Fatalf("prompt error = %v", err)
	}
	assertFailureUpdate(t, first.updates(), "stream model response: provider rejected tool parameters")
	model.assertConsumed(t)

	var closed acp.CloseSessionResponse
	if err := first.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&closed,
	); err != nil {
		t.Fatal(err)
	}

	second := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        &scriptedModel{},
		SessionDir:    sessionDir,
	})
	var loaded acp.LoadSessionResponse
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        cwd,
			MCPServers: []json.RawMessage{},
		},
		&loaded,
	); err != nil {
		t.Fatal(err)
	}
	assertFailureUpdate(t, second.updates(), "stream model response: provider rejected tool parameters")
}

func assertFailureUpdate(t *testing.T, updates []capturedUpdate, want string) {
	t.Helper()
	for _, update := range updates {
		if update.discriminator(t) != "agent_message_chunk" {
			continue
		}
		var chunk acp.AgentMessageChunk
		update.decode(t, &chunk)
		if chunk.Meta[acp.MetaOutcome] == "failed" && chunk.Content.Text == want {
			return
		}
	}
	t.Fatalf("failure update %q was not observed", want)
}

func TestSessionRestartLoadsReplayAndContinuesExactHistory(t *testing.T) {
	sessionDir := t.TempDir()
	cwd := t.TempDir()
	var systemPrompt string
	firstModel := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if len(messages) != 1 {
				return nil, errors.New("first process history was not one user message")
			}
			systemPrompt = request.Messages[0].Content[0].Text
			return completion("persisted answer"), nil
		},
	}}
	first := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        firstModel,
		SessionDir:    sessionDir,
	})
	sessionID := first.newSessionIn(t, cwd, nil)
	first.prompt(t, sessionID, "persist me")
	firstModel.assertConsumed(t)

	locked := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        &scriptedModel{},
		SessionDir:    sessionDir,
	})
	var load acp.LoadSessionResponse
	err := locked.local.Client.CallResult(t.Context(), "session/load", acp.LoadSessionRequest{
		SessionID:  sessionID,
		CWD:        cwd,
		MCPServers: []json.RawMessage{},
	}, &load)
	if err == nil || !strings.Contains(err.Error(), "active in another runtime") {
		t.Fatalf("locked load error = %v", err)
	}

	var closed acp.CloseSessionResponse
	if err := first.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&closed,
	); err != nil {
		t.Fatal(err)
	}

	secondModel := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if request.Messages[0].Content[0].Text != systemPrompt {
				return nil, errors.New("system prompt changed across the runtime restart")
			}
			if len(messages) != 3 ||
				messages[0].Content[0].Text != "persist me" ||
				messages[1].Content[0].Text != "persisted answer" ||
				messages[2].Content[0].Text != "continue" {
				return nil, errors.New("fresh process did not reconstruct exact history")
			}
			return completion("continued"), nil
		},
	}}
	second := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        secondModel,
		SessionDir:    sessionDir,
	})
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        cwd,
			MCPServers: []json.RawMessage{},
		},
		&load,
	); err != nil {
		t.Fatal(err)
	}
	var sawUser, sawAnswer bool
	for _, update := range second.updates() {
		if strings.Contains(string(update.Update), "<environment>") {
			t.Fatalf("system prompt entered ACP replay: %s", update.Update)
		}
		switch update.discriminator(t) {
		case "user_message_chunk":
			sawUser = true
		case "agent_message_chunk":
			var chunk acp.AgentMessageChunk
			update.decode(t, &chunk)
			sawAnswer = chunk.Content.Text == "persisted answer"
		}
	}
	if !sawUser || !sawAnswer {
		t.Fatalf("load replay omitted history: user=%v answer=%v", sawUser, sawAnswer)
	}
	second.prompt(t, sessionID, "continue")
	secondModel.assertConsumed(t)

	var listed acp.ListSessionsResponse
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/list",
		acp.ListSessionsRequest{CWD: cwd},
		&listed,
	); err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 ||
		listed.Sessions[0].SessionID != sessionID ||
		listed.Sessions[0].Title != "persist me" {
		t.Fatalf("listed sessions = %#v", listed.Sessions)
	}
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&closed,
	); err != nil {
		t.Fatal(err)
	}
	var deleted acp.DeleteSessionResponse
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/delete",
		acp.DeleteSessionRequest{SessionID: sessionID},
		&deleted,
	); err != nil {
		t.Fatal(err)
	}
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/list",
		acp.ListSessionsRequest{CWD: cwd},
		&listed,
	); err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 0 {
		t.Fatalf("deleted session remained listed: %#v", listed.Sessions)
	}
}

func TestReadOnlyToolsSpillRefuseEscapeReplayAndDelete(t *testing.T) {
	sessionDir := t.TempDir()
	realCWD := t.TempDir()
	canonicalCWD, err := filepath.EvalSymlinks(realCWD)
	if err != nil {
		t.Fatal(err)
	}
	linkParent := t.TempDir()
	linkCWD := filepath.Join(linkParent, "workspace")
	if err := os.Symlink(realCWD, linkCWD); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for index := 0; index <= 200; index++ {
		body.WriteString("needle\n")
	}
	if err := os.WriteFile(filepath.Join(realCWD, "many.txt"), []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	var spillPath string
	model := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if !strings.Contains(
				request.Messages[0].Content[0].Text,
				"<workspace-root>"+canonicalCWD+"</workspace-root>",
			) {
				return nil, errors.New("system prompt did not use the canonical workspace root")
			}
			if len(request.Tools) != 7 {
				return nil, errors.New("built-in tools were not frozen into the request")
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("grep-call", "grep", `{"pattern":"needle"}`),
					modelToolCall("escape-call", "read_file", `{"path":"../outside"}`),
				},
				FinishReason: "tool_calls",
			}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if len(messages) != 4 {
				return nil, errors.New("parallel tool group did not commit as one complete exchange")
			}
			grepResult := messages[2].Content[0].Text
			const marker = "full output at "
			start := strings.Index(grepResult, marker)
			if start < 0 {
				return nil, errors.New("grep result did not spill")
			}
			spillPath = strings.TrimSuffix(grepResult[start+len(marker):], "]")
			if !strings.Contains(messages[3].Content[0].Text, "outside the workspace") {
				return nil, errors.New("workspace escape was not returned as a failed tool result")
			}
			arguments, err := json.Marshal(map[string]any{
				"path":   spillPath,
				"offset": 1,
				"limit":  200,
			})
			if err != nil {
				return nil, err
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("read-spill-call", "read_file", string(arguments)),
				},
				FinishReason: "tool_calls",
			}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if len(messages) != 6 ||
				!strings.Contains(messages[5].Content[0].Text, "many.txt:1: needle") {
				return nil, errors.New("read_file could not read the session spill")
			}
			return completion("done"), nil
		},
	}}
	first := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		SessionDir:    sessionDir,
		Tools:         oxtools.All(),
	})
	sessionID := first.newSessionIn(t, linkCWD, nil)
	first.prompt(t, sessionID, "search")
	model.assertConsumed(t)

	var sawRead, sawSearch, sawFailure bool
	for _, update := range first.updates() {
		switch update.discriminator(t) {
		case "tool_call":
			var call acp.ToolCall
			update.decode(t, &call)
			sawRead = sawRead || call.Kind == acp.ToolKindRead
			sawSearch = sawSearch || call.Kind == acp.ToolKindSearch
		case "tool_call_update":
			var call acp.ToolCallUpdate
			update.decode(t, &call)
			sawFailure = sawFailure || call.ToolCallID == "escape-call" &&
				call.Status == acp.ToolCallStatusFailed
		}
	}
	if !sawRead || !sawSearch || !sawFailure {
		t.Fatalf(
			"tool updates omitted classification or failure: read=%v search=%v failure=%v",
			sawRead,
			sawSearch,
			sawFailure,
		)
	}
	if _, err := os.Stat(spillPath); err != nil {
		t.Fatalf("spill file is not durable: %v", err)
	}

	var listed acp.ListSessionsResponse
	if err := first.local.Client.CallResult(
		t.Context(),
		"session/list",
		acp.ListSessionsRequest{CWD: linkCWD},
		&listed,
	); err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].CWD != canonicalCWD {
		t.Fatalf("canonical session listing = %#v", listed.Sessions)
	}

	var closed acp.CloseSessionResponse
	if err := first.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&closed,
	); err != nil {
		t.Fatal(err)
	}
	second := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        &scriptedModel{},
		SessionDir:    sessionDir,
		Tools:         oxtools.All(),
	})
	var loaded acp.LoadSessionResponse
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        linkCWD,
			MCPServers: []json.RawMessage{},
		},
		&loaded,
	); err != nil {
		t.Fatal(err)
	}
	replayedCalls := 0
	replayedFailed := false
	replayedRead := false
	replayedSearch := false
	for _, update := range second.updates() {
		switch update.discriminator(t) {
		case "tool_call":
			replayedCalls++
			var call acp.ToolCall
			update.decode(t, &call)
			replayedRead = replayedRead || call.Kind == acp.ToolKindRead
			replayedSearch = replayedSearch || call.Kind == acp.ToolKindSearch
		case "tool_call_update":
			var call acp.ToolCallUpdate
			update.decode(t, &call)
			replayedFailed = replayedFailed ||
				call.ToolCallID == "escape-call" &&
					call.Status == acp.ToolCallStatusFailed
		}
	}
	if replayedCalls != 3 || !replayedFailed || !replayedRead || !replayedSearch {
		t.Fatalf(
			"tool replay = %d calls, failed=%v read=%v search=%v",
			replayedCalls,
			replayedFailed,
			replayedRead,
			replayedSearch,
		)
	}
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&closed,
	); err != nil {
		t.Fatal(err)
	}
	var deleted acp.DeleteSessionResponse
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/delete",
		acp.DeleteSessionRequest{SessionID: sessionID},
		&deleted,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, sessionID+".spill")); !os.IsNotExist(err) {
		t.Fatalf("spill directory survived session deletion: %v", err)
	}
}

func TestNewSessionRejectsANonexistentWorkspace(t *testing.T) {
	harness := newAgentHarness(t, &scriptedModel{}, oxtools.All())
	_, err := harness.callNewSession(filepath.Join(t.TempDir(), "missing"), nil)
	if err == nil || !strings.Contains(err.Error(), "cannot access the working directory") {
		t.Fatalf("nonexistent workspace error = %v", err)
	}
}

func TestSessionActivationRecordsComposedPromptChangeOnce(t *testing.T) {
	sessionDir := t.TempDir()
	cwd := t.TempDir()
	first := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        &scriptedModel{},
		SessionDir:    sessionDir,
	})
	sessionID := first.newSessionIn(t, cwd, nil)
	var closed acp.CloseSessionResponse
	if err := first.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&closed,
	); err != nil {
		t.Fatal(err)
	}

	const oldPrompt = "prompt from an earlier composition"
	path := filepath.Join(sessionDir, sessionID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &created); err != nil {
		t.Fatal(err)
	}
	created["data"].(map[string]any)["configuration"].(map[string]any)["systemPrompt"] = oldPrompt
	data, err = json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	var request openrouter.Request
	model := &scriptedModel{scripts: []modelScript{captureRequest(&request)}}
	second := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		SessionDir:    sessionDir,
	})
	var loaded acp.LoadSessionResponse
	if err := second.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        cwd,
			MCPServers: []json.RawMessage{},
		},
		&loaded,
	); err != nil {
		t.Fatal(err)
	}

	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("records after recomposition = %d, want 2", len(lines))
	}
	var changed struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(lines[1], &changed); err != nil {
		t.Fatal(err)
	}
	if changed.Type != "request_configuration_changed" {
		t.Fatalf("second record type = %q", changed.Type)
	}

	second.prompt(t, sessionID, "use the new prompt")
	if request.Messages[0].Content[0].Text == oldPrompt {
		t.Fatal("request retained the earlier composed prompt")
	}
	model.assertConsumed(t)
}

func TestParallelToolsFinishOutOfOrderAndReplayInCallOrder(t *testing.T) {
	started := make(chan string, 2)
	finished := make(chan string, 2)
	var systemPrompt string
	release := map[string]chan struct{}{
		"one": make(chan struct{}),
		"two": make(chan struct{}),
	}
	tool := agent.Tool{
		Name:         "parallel",
		Approval:     agent.ApprovalNone,
		ParallelSafe: true,
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		Execute: func(
			_ context.Context,
			invocation agent.Invocation,
		) (string, error) {
			var input struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(invocation.Arguments, &input); err != nil {
				return "", err
			}
			started <- input.ID
			invocation.Emit("live-" + input.ID)
			<-release[input.ID]
			finished <- input.ID
			return "result-" + input.ID, nil
		},
	}
	model := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if _, err := conversation(request); err != nil {
				return nil, err
			}
			systemPrompt = request.Messages[0].Content[0].Text
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("call-1", "parallel", `{"id":"one"}`),
					modelToolCall("call-2", "parallel", `{"id":"two"}`),
				},
				FinishReason: "tool_calls",
			}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if request.Messages[0].Content[0].Text != systemPrompt {
				return nil, errors.New("system prompt changed within the tool loop")
			}
			if len(messages) != 4 {
				return nil, errors.New("tool request did not replay four messages")
			}
			first := messages[2]
			second := messages[3]
			if first.ToolCallID != "call-1" || first.Content[0].Text != "result-one" ||
				second.ToolCallID != "call-2" || second.Content[0].Text != "result-two" {
				return nil, errors.New("tool results were not replayed in provider call order")
			}
			return completion("done"), nil
		},
	}}
	harness := newAgentHarness(t, model, []agent.Tool{tool})
	sessionID := harness.newSession(t)

	result := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(sessionID, "use tools")
		result <- promptResult{response: response, err: err}
	}()
	seen := map[string]bool{<-started: true, <-started: true}
	if !seen["one"] || !seen["two"] {
		t.Fatalf("parallel tools did not overlap: %#v", seen)
	}
	close(release["two"])
	if got := <-finished; got != "two" {
		t.Fatalf("first completed tool = %q", got)
	}
	close(release["one"])

	got := <-result
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", got.response.StopReason)
	}
	model.assertConsumed(t)
}

func TestConcurrentTasksNestChildCallsApproveAndReplay(t *testing.T) {
	var approvals atomic.Int32
	model := &routedModel{route: func(
		_ context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		system := request.Messages[0].Content[0].Text
		if strings.HasPrefix(system, "You are a subagent") {
			if len(request.Tools) != 6 {
				return nil, fmt.Errorf("subagent tools = %d, want 6", len(request.Tools))
			}
			for _, tool := range request.Tools {
				if tool.Function.Name == "task" {
					return nil, errors.New("subagent request retained task")
				}
			}
			prompt := request.Messages[1].Content[0].Text
			if request.Messages[len(request.Messages)-1].Role == openrouter.RoleTool {
				return completion("answer for " + prompt), nil
			}
			result := completion("")
			result.FinishReason = "tool_calls"
			switch prompt {
			case "run approved shell":
				result.ToolCalls = []openrouter.ToolCall{modelToolCall(
					"provider-shell",
					"shell",
					`{"command":"printf child-shell"}`,
				)}
			case "read the agent guide":
				result.ToolCalls = []openrouter.ToolCall{modelToolCall(
					"provider-read",
					"read_file",
					`{"path":"AGENTS.md","limit":1}`,
				)}
			default:
				return nil, fmt.Errorf("unexpected subagent prompt %q", prompt)
			}
			return result, nil
		}

		messages, err := conversation(request)
		if err != nil {
			return nil, err
		}
		if len(messages) == 1 {
			result := completion("")
			result.FinishReason = "tool_calls"
			result.ToolCalls = []openrouter.ToolCall{
				modelToolCall(
					"task-shell",
					"task",
					`{"description":"Run child shell","prompt":"run approved shell"}`,
				),
				modelToolCall(
					"task-read",
					"task",
					`{"description":"Read child guide","prompt":"read the agent guide"}`,
				),
			}
			return result, nil
		}
		if len(messages) != 4 ||
			messages[2].Role != openrouter.RoleTool ||
			messages[2].ToolCallID != "task-shell" ||
			messages[2].Content[0].Text != "answer for run approved shell" ||
			messages[3].Role != openrouter.RoleTool ||
			messages[3].ToolCallID != "task-read" ||
			messages[3].Content[0].Text != "answer for read the agent guide" {
			return nil, fmt.Errorf("parent history contains child steps: %#v", messages)
		}
		return completion("parent done"), nil
	}}
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools:         oxtools.All(),
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		var permission acp.RequestPermissionRequest
		if err := request.UnmarshalParams(&permission); err != nil {
			return nil, err
		}
		if permission.ToolCall.Name != "shell" ||
			permission.ToolCall.Meta[acp.MetaParentToolCallID] != "task-shell" {
			return nil, fmt.Errorf("child permission = %#v", permission)
		}
		approvals.Add(1)
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "allow_once",
		}}, nil
	})
	workspace := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(workspace, "AGENTS.md"),
		[]byte("guide\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	sessionID := harness.newSessionIn(t, workspace, nil)
	response := harness.prompt(t, sessionID, "delegate both")
	if response.StopReason != acp.StopReasonEndTurn ||
		response.Usage == nil ||
		response.Usage.TotalTokens != 72 {
		t.Fatalf("response = %#v", response)
	}
	if approvals.Load() != 1 {
		t.Fatalf("approval requests = %d", approvals.Load())
	}

	live := harness.updates()
	assertNestedTaskUpdates(t, live)
	beforeReplay := len(live)
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        workspace,
			MCPServers: []json.RawMessage{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	replayed := harness.updates()[beforeReplay:]
	assertNestedTaskUpdates(t, replayed)
	var lastCost float64
	for _, update := range replayed {
		if update.discriminator(t) != "usage_update" {
			continue
		}
		var usage acp.UsageUpdate
		update.decode(t, &usage)
		if usage.Cost != nil {
			lastCost = usage.Cost.Amount
		}
	}
	if lastCost < 0.0059 || lastCost > 0.0061 {
		t.Fatalf("replayed cost = %f", lastCost)
	}
}

func assertNestedTaskUpdates(t *testing.T, updates []capturedUpdate) {
	t.Helper()
	parents := map[string]string{
		"task-shell": "Run child shell",
		"task-read":  "Read child guide",
	}
	childCalls := 0
	childTerminals := 0
	for _, update := range updates {
		switch update.discriminator(t) {
		case "tool_call":
			var call acp.ToolCall
			update.decode(t, &call)
			if title, parent := parents[call.ToolCallID]; parent {
				if call.Title != title || call.Meta[acp.MetaSubagent] != true {
					t.Fatalf("parent call = %#v", call)
				}
				continue
			}
			parent, ok := call.Meta[acp.MetaParentToolCallID].(string)
			if !ok || parents[parent] == "" {
				t.Fatalf("child call = %#v", call)
			}
			childCalls++
		case "tool_call_update":
			var call acp.ToolCallUpdate
			update.decode(t, &call)
			if _, parent := parents[call.ToolCallID]; parent {
				continue
			}
			parent, ok := call.Meta[acp.MetaParentToolCallID].(string)
			if !ok || parents[parent] == "" {
				t.Fatalf("child update = %#v", call)
			}
			if call.Status == acp.ToolCallStatusCompleted ||
				call.Status == acp.ToolCallStatusFailed {
				childTerminals++
			}
		}
	}
	if childCalls != 2 || childTerminals != 2 {
		t.Fatalf("child calls = %d, terminals = %d", childCalls, childTerminals)
	}
}

func TestEmptySubagentAnswerFailsTaskAndParentContinues(t *testing.T) {
	model := &scriptedModel{scripts: []modelScript{
		toolCompletionWithArguments(
			"empty-task",
			"task",
			`{"description":"Empty child","prompt":"return nothing"}`,
		),
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if !strings.HasPrefix(
				request.Messages[0].Content[0].Text,
				"You are a subagent",
			) {
				return nil, errors.New("second request was not the subagent")
			}
			return &openrouter.Completion{FinishReason: "stop"}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if len(messages) != 3 ||
				!strings.Contains(
					messages[2].Content[0].Text,
					"subagent returned an empty final answer",
				) {
				return nil, fmt.Errorf("parent task result = %#v", messages)
			}
			return completion("parent recovered"), nil
		},
	}}
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	harness := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		SessionDir:    sessionDir,
		Tools:         oxtools.All(),
	})
	sessionID := harness.newSessionIn(t, workspace, nil)
	if response := harness.prompt(t, sessionID, "delegate empty"); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("response = %#v", response)
	}
	beforeReplay := len(harness.updates())
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        workspace,
			MCPServers: []json.RawMessage{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	var sawFailed bool
	for _, update := range harness.updates()[beforeReplay:] {
		switch update.discriminator(t) {
		case "tool_call":
			var call acp.ToolCall
			update.decode(t, &call)
			if call.ToolCallID == "empty-task" &&
				(call.Title != "Empty child" || call.Meta[acp.MetaSubagent] != true) {
				t.Fatalf("replayed empty task = %#v", call)
			}
		case "tool_call_update":
			var call acp.ToolCallUpdate
			update.decode(t, &call)
			sawFailed = sawFailed ||
				call.ToolCallID == "empty-task" &&
					call.Status == acp.ToolCallStatusFailed
		}
	}
	if !sawFailed {
		t.Fatal("failed empty task was not replayed")
	}
	model.assertConsumed(t)
}

func TestRejectedChildToolContinuesSubagentLoop(t *testing.T) {
	model := &scriptedModel{scripts: []modelScript{
		toolCompletionWithArguments(
			"reject-task",
			"task",
			`{"description":"Rejected child","prompt":"try shell"}`,
		),
		toolCompletionWithArguments(
			"provider-shell",
			"shell",
			`{"command":"printf should-not-run"}`,
		),
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			last := request.Messages[len(request.Messages)-1]
			if last.Role != openrouter.RoleTool ||
				!strings.Contains(last.Content[0].Text, "user rejected") {
				return nil, fmt.Errorf("rejected child history = %#v", request.Messages)
			}
			return completion("child continued"), nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if messages[len(messages)-1].Content[0].Text != "child continued" {
				return nil, fmt.Errorf("parent result = %#v", messages)
			}
			return completion("parent continued"), nil
		},
	}}
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools:         oxtools.All(),
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "reject_once",
		}}, nil
	})
	sessionID := harness.newSession(t)
	if response := harness.prompt(t, sessionID, "delegate rejection"); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("response = %#v", response)
	}
	var failedChild bool
	for _, update := range harness.updates() {
		if update.discriminator(t) != "tool_call_update" {
			continue
		}
		var call acp.ToolCallUpdate
		update.decode(t, &call)
		failedChild = failedChild ||
			call.Status == acp.ToolCallStatusFailed &&
				call.Meta[acp.MetaParentToolCallID] == "reject-task"
	}
	if !failedChild {
		t.Fatal("rejected child tool was not shown as failed")
	}
	model.assertConsumed(t)
}

func TestConcurrentSubagentWritesUseIndependentReadEvidence(t *testing.T) {
	var barrierMu sync.Mutex
	barrierCount := 0
	bothRead := make(chan struct{})
	var approvals atomic.Int32
	model := &routedModel{route: func(
		_ context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		if strings.HasPrefix(
			request.Messages[0].Content[0].Text,
			"You are a subagent",
		) {
			prompt := request.Messages[1].Content[0].Text
			last := request.Messages[len(request.Messages)-1]
			if last.Role == openrouter.RoleUser {
				return toolCompletionWithArguments(
					"read-"+prompt,
					"read_file",
					`{"path":"shared.txt"}`,
				)(context.Background(), request, nil)
			}
			if last.ToolCallID == "read-"+prompt {
				barrierMu.Lock()
				barrierCount++
				if barrierCount == 2 {
					close(bothRead)
				}
				barrierMu.Unlock()
				<-bothRead
				return toolCompletionWithArguments(
					"write-"+prompt,
					"write_file",
					`{"path":"shared.txt","content":"`+prompt+`"}`,
				)(context.Background(), request, nil)
			}
			if last.ToolCallID == "write-"+prompt {
				return completion(last.Content[0].Text), nil
			}
			return nil, fmt.Errorf("unexpected child history: %#v", request.Messages)
		}
		messages, err := conversation(request)
		if err != nil {
			return nil, err
		}
		if len(messages) == 1 {
			result := completion("")
			result.FinishReason = "tool_calls"
			result.ToolCalls = []openrouter.ToolCall{
				modelToolCall(
					"task-one",
					"task",
					`{"description":"Writer one","prompt":"one"}`,
				),
				modelToolCall(
					"task-two",
					"task",
					`{"description":"Writer two","prompt":"two"}`,
				),
			}
			return result, nil
		}
		if len(messages) != 4 {
			return nil, fmt.Errorf("parent history = %#v", messages)
		}
		stale := 0
		for _, message := range messages[2:] {
			if strings.Contains(message.Content[0].Text, "file changed since") {
				stale++
			}
		}
		if stale != 1 {
			return nil, fmt.Errorf("stale child results = %d, messages = %#v", stale, messages)
		}
		return completion("writes resolved"), nil
	}}
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools:         oxtools.All(),
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		approvals.Add(1)
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "allow_once",
		}}, nil
	})
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "shared.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionID := harness.newSessionIn(t, workspace, nil)
	if response := harness.prompt(t, sessionID, "run writers"); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("response = %#v", response)
	}
	if approvals.Load() != 2 {
		t.Fatalf("approvals = %d", approvals.Load())
	}
	content, err := os.ReadFile(filepath.Join(workspace, "shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "one\n" && string(content) != "two\n" {
		t.Fatalf("shared content = %q", content)
	}
}

func TestApprovalAllowsAndGrantsOnlyForTheActivation(t *testing.T) {
	var executions atomic.Int32
	var requests atomic.Int32
	tool := agent.Tool{
		Name:        "gated",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Execute: func(context.Context, agent.Invocation) (string, error) {
			executions.Add(1)
			return "approved", nil
		},
	}
	model := &scriptedModel{scripts: []modelScript{
		toolCompletion("call-1", "gated"),
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; got != "approved" {
				return nil, fmt.Errorf("first tool result = %q", got)
			}
			return toolCompletion("call-2", "gated")(context.Background(), request, nil)
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; got != "approved" {
				return nil, fmt.Errorf("second tool result = %q", got)
			}
			return completion("done"), nil
		},
		toolCompletion("call-3", "gated"),
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; got != "approved" {
				return nil, fmt.Errorf("reactivated tool result = %q", got)
			}
			return completion("done again"), nil
		},
	}}
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools:         []agent.Tool{tool},
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		var permission acp.RequestPermissionRequest
		if err := request.UnmarshalParams(&permission); err != nil {
			return nil, err
		}
		if permission.ToolCall.ToolCallID == "" ||
			permission.ToolCall.Title != "gated" ||
			string(permission.ToolCall.RawInput) != "{}" ||
			len(permission.Options) != 3 {
			return nil, fmt.Errorf("permission request = %#v", permission)
		}
		requests.Add(1)
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "allow_always",
		}}, nil
	})
	workspace := t.TempDir()
	sessionID := harness.newSessionIn(t, workspace, nil)
	if response, err := harness.callPrompt(sessionID, "first"); err != nil {
		t.Fatal(err)
	} else if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("first stop reason = %q", response.StopReason)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("permission requests in first activation = %d", got)
	}
	if got := executions.Load(); got != 2 {
		t.Fatalf("executions in first activation = %d", got)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        workspace,
			MCPServers: []json.RawMessage{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if response, err := harness.callPrompt(sessionID, "second"); err != nil {
		t.Fatal(err)
	} else if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("second stop reason = %q", response.StopReason)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("permission requests after reactivation = %d", got)
	}
	if got := executions.Load(); got != 3 {
		t.Fatalf("executions after reactivation = %d", got)
	}
	model.assertConsumed(t)
}

func TestShellApprovalGrantIsRuleScopedAndActivationScoped(t *testing.T) {
	model := &scriptedModel{scripts: []modelScript{
		toolCompletionWithArguments(
			"shell-1",
			"shell",
			`{"command":"printf approved"}`,
		),
		toolCompletionWithArguments(
			"shell-2",
			"shell",
			`{"command":"printf approved again"}`,
		),
		toolCompletionWithArguments(
			"shell-3",
			"shell",
			`{"command":"printf different"}`,
		),
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			for _, want := range []string{"approved", "approved", "different"} {
				found := false
				for _, message := range messages {
					if message.Role == openrouter.RoleTool &&
						strings.Contains(message.Content[0].Text, want) {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("conversation omitted shell output %q", want)
				}
			}
			return completion("done"), nil
		},
		toolCompletionWithArguments(
			"shell-4",
			"shell",
			`{"command":"printf approved"}`,
		),
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "approved") {
				return nil, fmt.Errorf("reactivated shell output = %q", got)
			}
			return completion("done again"), nil
		},
		toolCompletionWithArguments(
			"shell-5",
			"shell",
			`{"command":"printf approved"}`,
		),
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "approved") {
				return nil, fmt.Errorf("resumed shell output = %q", got)
			}
			return completion("done after resume"), nil
		},
	}}
	var requests []acp.RequestPermissionRequest
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools:         oxtools.All(),
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		var permission acp.RequestPermissionRequest
		if err := request.UnmarshalParams(&permission); err != nil {
			return nil, err
		}
		requests = append(requests, permission)
		optionID := "allow_once"
		if len(requests) == 1 {
			optionID = "allow_always"
		}
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: optionID,
		}}, nil
	})
	workspace := t.TempDir()
	sessionID := harness.newSessionIn(t, workspace, nil)
	harness.prompt(t, sessionID, "run shell commands")
	if len(requests) != 2 ||
		requests[0].ToolCall.ToolCallID != "shell-1" ||
		requests[1].ToolCall.ToolCallID != "shell-3" {
		t.Fatalf("first activation requests = %#v", requests)
	}
	if len(requests[0].Options) != 3 ||
		requests[0].Options[1].Name != `Allow "printf approved" for this session` {
		t.Fatalf("rule options = %#v", requests[0].Options)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        workspace,
			MCPServers: []json.RawMessage{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	harness.prompt(t, sessionID, "run it again")
	if len(requests) != 3 || requests[2].ToolCall.ToolCallID != "shell-4" {
		t.Fatalf("requests after load = %#v", requests)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/resume",
		acp.ResumeSessionRequest{
			SessionID:  sessionID,
			CWD:        workspace,
			MCPServers: []json.RawMessage{},
		},
		&acp.ResumeSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	harness.prompt(t, sessionID, "run it after resume")
	if len(requests) != 4 || requests[3].ToolCall.ToolCallID != "shell-5" {
		t.Fatalf("requests after resume = %#v", requests)
	}
	model.assertConsumed(t)
}

func TestFileMutationToolsRequireApprovalAndFreshReadsPerActivation(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "notes.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeArguments := `{"path":"notes.txt","content":"after\n"}`
	model := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if _, err := conversation(request); err != nil {
				return nil, err
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("write-unread", "write_file", writeArguments),
				},
				FinishReason: "tool_calls",
			}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "has not read") {
				return nil, fmt.Errorf("unread overwrite result = %q", got)
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("read-current", "read_file", `{"path":"notes.txt"}`),
				},
				FinishReason: "tool_calls",
			}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; got != "before\n" {
				return nil, fmt.Errorf("read result = %q", got)
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("write-fresh", "write_file", writeArguments),
				},
				FinishReason: "tool_calls",
			}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "Wrote notes.txt") {
				return nil, fmt.Errorf("write result = %q", got)
			}
			return completion("first activation complete"), nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if _, err := conversation(request); err != nil {
				return nil, err
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("write-after-load", "write_file", writeArguments),
				},
				FinishReason: "tool_calls",
			}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "has not read") {
				return nil, fmt.Errorf("reactivated overwrite result = %q", got)
			}
			return completion("second activation refused"), nil
		},
	}}
	var approvals atomic.Int32
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools:         oxtools.All(),
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		approvals.Add(1)
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "allow_once",
		}}, nil
	})
	sessionID := harness.newSessionIn(t, workspace, nil)
	harness.prompt(t, sessionID, "replace the notes")
	if data, err := os.ReadFile(path); err != nil || string(data) != "after\n" {
		t.Fatalf("first activation content = %q, %v", data, err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        workspace,
			MCPServers: []json.RawMessage{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	harness.prompt(t, sessionID, "replace the notes again")
	if data, err := os.ReadFile(path); err != nil || string(data) != "after\n" {
		t.Fatalf("reactivated content = %q, %v", data, err)
	}
	if approvals.Load() != 3 {
		t.Fatalf("approval requests = %d, want 3", approvals.Load())
	}
	model.assertConsumed(t)
}

func TestRejectedFileMutationLeavesTheFileUntouched(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "notes.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if _, err := conversation(request); err != nil {
				return nil, err
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"edit-rejected",
					"edit_file",
					`{"path":"notes.txt","old_string":"before","new_string":"after"}`,
				)},
				FinishReason: "tool_calls",
			}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "user rejected") {
				return nil, fmt.Errorf("rejected edit result = %q", got)
			}
			return completion("continued"), nil
		},
	}}
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools:         oxtools.All(),
	}, func(context.Context, *jrpc2.Request) (any, error) {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "reject_once",
		}}, nil
	})
	harness.prompt(t, harness.newSessionIn(t, workspace, nil), "edit the notes")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before\n" || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("rejected mutation changed file: content=%q mtime=%v -> %v", data, before.ModTime(), after.ModTime())
	}
	model.assertConsumed(t)
}

func TestRejectedToolBecomesAResultWithoutDispatch(t *testing.T) {
	var executions atomic.Int32
	model := &scriptedModel{scripts: []modelScript{
		toolCompletion("call-rejected", "gated"),
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			result := messages[len(messages)-1]
			if result.Role != openrouter.RoleTool ||
				!strings.Contains(result.Content[0].Text, "user rejected") {
				return nil, fmt.Errorf("rejected result = %#v", result)
			}
			return completion("continued"), nil
		},
	}}
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools: []agent.Tool{{
			Name:        "gated",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Execute: func(context.Context, agent.Invocation) (string, error) {
				executions.Add(1)
				return "unexpected", nil
			},
		}},
	}, func(context.Context, *jrpc2.Request) (any, error) {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "reject_once",
		}}, nil
	})
	sessionID := harness.newSession(t)
	if _, err := harness.callPrompt(sessionID, "reject it"); err != nil {
		t.Fatal(err)
	}
	if executions.Load() != 0 {
		t.Fatal("rejected tool dispatched")
	}
	updates := harness.updates()
	var failed bool
	for _, update := range updates {
		if update.discriminator(t) != "tool_call_update" {
			continue
		}
		var call acp.ToolCallUpdate
		update.decode(t, &call)
		failed = failed || call.Status == acp.ToolCallStatusFailed
	}
	if !failed {
		t.Fatal("failed tool update was not emitted")
	}
	model.assertConsumed(t)
}

func TestCancellationWhileApprovalIsOutstandingCancelsTheTurn(t *testing.T) {
	requested := make(chan struct{})
	answer := make(chan struct{})
	var gatedExecutions atomic.Int32
	var safeExecutions atomic.Int32
	model := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if _, err := conversation(request); err != nil {
				return nil, err
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("call-cancelled", "gated", `{}`),
					modelToolCall("call-later", "safe", `{}`),
				},
				FinishReason: "tool_calls",
			}, nil
		},
	}}
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools: []agent.Tool{
			{
				Name:        "gated",
				InputSchema: json.RawMessage(`{"type":"object"}`),
				Execute: func(context.Context, agent.Invocation) (string, error) {
					gatedExecutions.Add(1)
					return "unexpected", nil
				},
			},
			{
				Name:        "safe",
				InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval:    agent.ApprovalNone,
				Execute: func(context.Context, agent.Invocation) (string, error) {
					safeExecutions.Add(1)
					return "unexpected", nil
				},
			},
		},
	}, func(context.Context, *jrpc2.Request) (any, error) {
		close(requested)
		<-answer
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome: "cancelled",
		}}, nil
	})
	workspace := t.TempDir()
	sessionID := harness.newSessionIn(t, workspace, nil)
	result := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(sessionID, "cancel approval")
		result <- promptResult{response: response, err: err}
	}()
	<-requested
	if err := harness.local.Client.Notify(
		t.Context(),
		"session/cancel",
		acp.CancelNotification{SessionID: sessionID},
	); err != nil {
		t.Fatal(err)
	}
	close(answer)
	got := <-result
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stop reason = %q", got.response.StopReason)
	}
	if gatedExecutions.Load() != 0 || safeExecutions.Load() != 0 {
		t.Fatal("tool dispatched after approval cancellation")
	}
	var failed int
	for _, update := range harness.updates() {
		if update.discriminator(t) != "tool_call_update" {
			continue
		}
		var call acp.ToolCallUpdate
		update.decode(t, &call)
		if call.Status == acp.ToolCallStatusFailed {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("failed terminal tool updates = %d, want 2", failed)
	}
	beforeReplay := len(harness.updates())
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        workspace,
			MCPServers: []json.RawMessage{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	var replayedFailed int
	for _, update := range harness.updates()[beforeReplay:] {
		if update.discriminator(t) != "tool_call_update" {
			continue
		}
		var call acp.ToolCallUpdate
		update.decode(t, &call)
		if call.Status == acp.ToolCallStatusFailed {
			replayedFailed++
		}
	}
	if replayedFailed != 2 {
		t.Fatalf("replayed failed tool updates = %d, want 2", replayedFailed)
	}
	model.assertConsumed(t)
}

func toolCompletion(callID, name string) modelScript {
	return toolCompletionWithArguments(callID, name, `{}`)
}

func toolCompletionWithArguments(callID, name, arguments string) modelScript {
	return func(
		_ context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		if _, err := conversation(request); err != nil {
			return nil, err
		}
		return &openrouter.Completion{
			ToolCalls:    []openrouter.ToolCall{modelToolCall(callID, name, arguments)},
			FinishReason: "tool_calls",
		}, nil
	}
}

func TestCancellationStopsActiveToolsAndAccountsForLaterCalls(t *testing.T) {
	started := make(chan string, 2)
	var exclusiveStarts atomic.Int32
	parallel := agent.Tool{
		Name:         "parallel",
		Approval:     agent.ApprovalNone,
		ParallelSafe: true,
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		Execute: func(
			ctx context.Context,
			invocation agent.Invocation,
		) (string, error) {
			started <- string(invocation.Arguments)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	exclusive := agent.Tool{
		Name:        "exclusive",
		Approval:    agent.ApprovalNone,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Execute: func(
			context.Context,
			agent.Invocation,
		) (string, error) {
			exclusiveStarts.Add(1)
			return "should not run", nil
		},
	}
	model := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if _, err := conversation(request); err != nil {
				return nil, err
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("call-1", "parallel", `{"id":1}`),
					modelToolCall("call-2", "parallel", `{"id":2}`),
					modelToolCall("call-3", "exclusive", `{}`),
				},
				FinishReason: "tool_calls",
			}, nil
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if len(messages) != 6 {
				return nil, errors.New("cancelled tool group was not committed completely")
			}
			for index, id := range []string{"call-1", "call-2", "call-3"} {
				message := messages[index+2]
				if message.ToolCallID != id || !strings.Contains(message.Content[0].Text, "cancel") {
					return nil, errors.New("cancelled results were missing or out of order")
				}
			}
			return completion("after cancellation"), nil
		},
	}}
	harness := newAgentHarness(t, model, []agent.Tool{parallel, exclusive})
	sessionID := harness.newSession(t)

	result := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(sessionID, "cancel me")
		result <- promptResult{response: response, err: err}
	}()
	<-started
	<-started
	if err := harness.local.Client.Notify(
		t.Context(),
		"session/cancel",
		acp.CancelNotification{SessionID: sessionID},
	); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stop reason = %q", got.response.StopReason)
	}
	if exclusiveStarts.Load() != 0 {
		t.Fatal("exclusive tool started after cancellation")
	}

	harness.prompt(t, sessionID, "continue")
	model.assertConsumed(t)
}

func TestCancellationStopsConcurrentSubagentsWithoutDurableDelegations(t *testing.T) {
	started := make(chan struct{}, 2)
	model := &routedModel{route: func(
		ctx context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		if strings.HasPrefix(
			request.Messages[0].Content[0].Text,
			"You are a subagent",
		) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		result := completion("")
		result.FinishReason = "tool_calls"
		result.ToolCalls = []openrouter.ToolCall{
			modelToolCall(
				"task-one",
				"task",
				`{"description":"One","prompt":"wait one"}`,
			),
			modelToolCall(
				"task-two",
				"task",
				`{"description":"Two","prompt":"wait two"}`,
			),
		}
		return result, nil
	}}
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	harness := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		SessionDir:    sessionDir,
		Tools:         oxtools.All(),
	})
	sessionID := harness.newSessionIn(t, workspace, nil)
	result := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(sessionID, "delegate then cancel")
		result <- promptResult{response: response, err: err}
	}()
	<-started
	<-started
	if err := harness.local.Client.Notify(
		t.Context(),
		"session/cancel",
		acp.CancelNotification{SessionID: sessionID},
	); err != nil {
		t.Fatal(err)
	}
	outcome := <-result
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stop reason = %q", outcome.response.StopReason)
	}
	beforeReplay := len(harness.updates())
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        workspace,
			MCPServers: []json.RawMessage{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	for _, update := range harness.updates()[beforeReplay:] {
		if update.discriminator(t) != "tool_call" {
			continue
		}
		var call acp.ToolCall
		update.decode(t, &call)
		if call.Meta[acp.MetaParentToolCallID] != nil {
			t.Fatalf("cancelled child call was replayed: %#v", call)
		}
	}
}

func TestCancellationRetainsCompletedChildMutationInReplay(t *testing.T) {
	waiting := make(chan struct{})
	var approval atomic.Int32
	model := &routedModel{route: func(
		ctx context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		if strings.HasPrefix(
			request.Messages[0].Content[0].Text,
			"You are a subagent",
		) {
			last := request.Messages[len(request.Messages)-1]
			if last.Role == openrouter.RoleUser {
				return toolCompletionWithArguments(
					"provider-write",
					"write_file",
					`{"path":"child.txt","content":"mutated\n"}`,
				)(context.Background(), request, nil)
			}
			close(waiting)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return toolCompletionWithArguments(
			"task-write",
			"task",
			`{"description":"Child mutation","prompt":"write then wait"}`,
		)(context.Background(), request, nil)
	}}
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		SessionDir:    sessionDir,
		Tools:         oxtools.All(),
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		approval.Add(1)
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "allow_once",
		}}, nil
	})
	sessionID := harness.newSessionIn(t, workspace, nil)
	result := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(sessionID, "delegate mutation")
		result <- promptResult{response: response, err: err}
	}()
	<-waiting
	if err := harness.local.Client.Notify(
		t.Context(),
		"session/cancel",
		acp.CancelNotification{SessionID: sessionID},
	); err != nil {
		t.Fatal(err)
	}
	outcome := <-result
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.response.StopReason != acp.StopReasonCancelled || approval.Load() != 1 {
		t.Fatalf("outcome = %#v, approvals = %d", outcome, approval.Load())
	}
	if content, err := os.ReadFile(filepath.Join(workspace, "child.txt")); err != nil {
		t.Fatal(err)
	} else if string(content) != "mutated\n" {
		t.Fatalf("child mutation = %q", content)
	}

	beforeReplay := len(harness.updates())
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/close",
		acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        workspace,
			MCPServers: []json.RawMessage{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	var replayedChild bool
	for _, update := range harness.updates()[beforeReplay:] {
		if update.discriminator(t) != "tool_call" {
			continue
		}
		var call acp.ToolCall
		update.decode(t, &call)
		replayedChild = replayedChild ||
			call.Name == "write_file" &&
				call.Meta[acp.MetaParentToolCallID] == "task-write"
	}
	if !replayedChild {
		t.Fatal("completed child mutation was absent from replay")
	}
}

func TestCancellationDiscardsIncompleteAssistantMessage(t *testing.T) {
	started := make(chan struct{})
	model := &scriptedModel{scripts: []modelScript{
		func(
			ctx context.Context,
			request openrouter.Request,
			onDelta func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if _, err := conversation(request); err != nil {
				return nil, err
			}
			onDelta(openrouter.Delta{Kind: openrouter.DeltaText, Text: "partial"})
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if len(messages) != 2 ||
				messages[0].Role != openrouter.RoleUser ||
				messages[1].Role != openrouter.RoleUser {
				return nil, errors.New("incomplete assistant message was replayed")
			}
			return completion("recovered"), nil
		},
	}}
	harness := newAgentHarness(t, model, nil)
	sessionID := harness.newSession(t)
	result := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(sessionID, "cancel model")
		result <- promptResult{response: response, err: err}
	}()
	<-started
	if err := harness.local.Client.Notify(
		t.Context(),
		"session/cancel",
		acp.CancelNotification{SessionID: sessionID},
	); err != nil {
		t.Fatal(err)
	}
	if got := <-result; got.err != nil ||
		got.response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("cancelled prompt = %#v", got)
	}
	harness.prompt(t, sessionID, "next")
	model.assertConsumed(t)
}

func TestOneSessionRejectsOverlapWhileSeparateSessionsRun(t *testing.T) {
	model := newBlockingModel()
	harness := newAgentHarness(t, model, nil)
	firstSession := harness.newSession(t)
	secondSession := harness.newSession(t)

	firstResult := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(firstSession, "first")
		firstResult <- promptResult{response: response, err: err}
	}()
	if call := <-model.started; call != firstSession {
		t.Fatalf("first model session = %q", call)
	}

	overlap := make(chan error, 1)
	go func() {
		_, err := harness.callPrompt(firstSession, "overlap")
		overlap <- err
	}()
	if err := <-overlap; err == nil || !strings.Contains(err.Error(), "active prompt") {
		t.Fatalf("overlapping prompt error = %v", err)
	}

	secondResult := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(secondSession, "second")
		secondResult <- promptResult{response: response, err: err}
	}()
	if call := <-model.started; call != secondSession {
		t.Fatalf("second model session = %q", call)
	}
	close(model.release[firstSession])
	close(model.release[secondSession])
	if result := <-firstResult; result.err != nil {
		t.Fatal(result.err)
	}
	if result := <-secondResult; result.err != nil {
		t.Fatal(result.err)
	}
}

func TestSettingsFilesShapeEveryModelRequest(t *testing.T) {
	workspace := t.TempDir()
	global := writeSettingsFile(t, filepath.Join(t.TempDir(), "settings.json"), `{
		"model": "global/model",
		"max_tokens": 512,
		"temperature": 0.2,
		"reasoning": {"effort": "high"},
		"provider": {"order": ["alpha"], "allow_fallbacks": false}
	}`)
	var globalOnly, overridden openrouter.Request
	model := &scriptedModel{
		entry: &openrouter.Model{
			ID:            "global/model",
			ContextLength: 128_000,
			Reasoning: &openrouter.ModelReasoning{
				SupportedEfforts: []string{"high", "low"},
			},
		},
		scripts: []modelScript{
			captureRequest(&globalOnly),
			captureRequest(&overridden),
		},
	}
	harness := newSettingsHarness(t, model, global)

	harness.prompt(t, harness.newSessionIn(t, workspace, nil), "global only")
	if globalOnly.Model != "global/model" || *globalOnly.MaxTokens != 512 ||
		*globalOnly.Temperature != 0.2 {
		t.Fatalf("global request = %#v", globalOnly)
	}
	if globalOnly.Reasoning == nil || globalOnly.Reasoning.Effort != "high" {
		t.Fatalf("global reasoning = %#v", globalOnly.Reasoning)
	}
	if globalOnly.Provider == nil ||
		strings.Join(globalOnly.Provider.Order, ",") != "alpha" ||
		globalOnly.Provider.AllowFallbacks == nil || *globalOnly.Provider.AllowFallbacks {
		t.Fatalf("global provider = %#v", globalOnly.Provider)
	}

	writeSettingsFile(t, settings.WorkspacePath(workspace), `{
		"temperature": 0.9,
		"provider": {"order": ["beta"]}
	}`)
	harness.prompt(t, harness.newSessionIn(t, workspace, nil), "workspace override")
	if overridden.Model != "global/model" || *overridden.MaxTokens != 512 ||
		*overridden.Temperature != 0.9 {
		t.Fatalf("overridden request = %#v", overridden)
	}
	if overridden.Reasoning == nil || overridden.Reasoning.Effort != "high" {
		t.Fatalf("overridden reasoning = %#v", overridden.Reasoning)
	}
	if strings.Join(overridden.Provider.Order, ",") != "beta" ||
		overridden.Provider.AllowFallbacks == nil || *overridden.Provider.AllowFallbacks {
		t.Fatalf("overridden provider = %#v", overridden.Provider)
	}
	model.assertConsumed(t)
}

func TestMalformedWorkspaceSettingsFailSessionCreationBeforeAnyRequest(t *testing.T) {
	workspace := t.TempDir()
	path := writeSettingsFile(t, settings.WorkspacePath(workspace), `{"models": "typo/model"}`)
	model := &scriptedModel{}
	harness := newSettingsHarness(t, model, "")

	_, err := harness.callNewSession(workspace, nil)
	assertRPCErrorCode(t, err, jrpc2.InternalError)
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want mention of %q", err, path)
	}
	model.assertConsumed(t)
}

func TestSessionSettingsAreFrozenAtCreation(t *testing.T) {
	workspace := t.TempDir()
	global := filepath.Join(t.TempDir(), "settings.json")
	writeSettingsFile(t, global, `{"model": "first/model", "temperature": 0.1}`)
	writeSettingsFile(t, settings.WorkspacePath(workspace), `{"max_tokens": 100}`)

	var before, after, fresh openrouter.Request
	model := &scriptedModel{scripts: []modelScript{
		captureRequest(&before),
		captureRequest(&after),
		captureRequest(&fresh),
	}}
	harness := newSettingsHarness(t, model, global)
	frozen := harness.newSessionIn(t, workspace, nil)
	harness.prompt(t, frozen, "before the edit")

	writeSettingsFile(t, global, `{"model": "second/model", "temperature": 0.7}`)
	writeSettingsFile(t, settings.WorkspacePath(workspace), `{"max_tokens": 200}`)
	harness.prompt(t, frozen, "after the edit")
	if after.Model != before.Model || *after.Temperature != *before.Temperature ||
		*after.MaxTokens != *before.MaxTokens {
		t.Fatalf("running session changed mid-conversation: %#v then %#v", before, after)
	}
	if before.Model != "first/model" || *before.Temperature != 0.1 ||
		*before.MaxTokens != 100 {
		t.Fatalf("frozen request = %#v", before)
	}

	harness.prompt(t, harness.newSessionIn(t, workspace, nil), "fresh session")
	if fresh.Model != "second/model" || *fresh.Temperature != 0.7 ||
		*fresh.MaxTokens != 200 {
		t.Fatalf("fresh request = %#v", fresh)
	}
	model.assertConsumed(t)
}

func captureRequest(into *openrouter.Request) modelScript {
	return func(
		_ context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		if _, err := conversation(request); err != nil {
			return nil, err
		}
		*into = request
		return completion("done"), nil
	}
}

func conversation(request openrouter.Request) ([]openrouter.Message, error) {
	if len(request.Messages) == 0 {
		return nil, errors.New("model request omitted the system message")
	}
	message := request.Messages[0]
	if message.Role != openrouter.RoleSystem ||
		len(message.Content) != 1 ||
		message.Content[0].Type != "text" ||
		strings.TrimSpace(message.Content[0].Text) == "" {
		return nil, fmt.Errorf(
			"first model message is not a non-empty system message: %#v",
			message,
		)
	}
	return request.Messages[1:], nil
}

func writeSettingsFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProcessSessionCreationReportsMissingConfiguration(t *testing.T) {
	process := startRuntime(t, "")
	defer process.stop(t)
	workspace := t.TempDir()

	process.write(t, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":%q,"mcpServers":[]}}`,
		workspace,
	))
	response := process.read(t)
	errorObject, ok := response["error"].(map[string]any)
	if !ok || errorObject["code"] != float64(acp.ErrCodeAuthRequired) {
		t.Fatalf("authentication error = %#v", response)
	}
}

func TestAuthenticateMakesRunningRPCServerUsableWithoutRestart(t *testing.T) {
	keyring.MockInit()
	t.Setenv("OX_KEYRING_DISABLED", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := credentials.NewStore(logger)
	instance, err := agent.New(agent.Config{
		Logger:        logger,
		Credentials:   store,
		ModelOverride: "test/model",
		Client:        &scriptedModel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	local := server.NewLocal(instance.Methods(), nil)
	t.Cleanup(func() {
		if err := local.Close(); err != nil {
			t.Errorf("close local server: %v", err)
		}
	})
	workspace := t.TempDir()

	var session acp.NewSessionResponse
	err = local.Client.CallResult(t.Context(), "session/new", acp.NewSessionRequest{
		CWD:        workspace,
		MCPServers: []json.RawMessage{},
	}, &session)
	assertRPCErrorCode(t, err, acp.ErrCodeAuthRequired)

	const key = "rpc-key"
	if err := store.Set(key); err != nil {
		t.Fatal(err)
	}
	var authenticated acp.AuthenticateResponse
	err = local.Client.CallResult(t.Context(), "authenticate", acp.AuthenticateRequest{
		MethodID: "openrouter",
	}, &authenticated)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(authenticated)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatalf("authenticate response leaked key: %s", raw)
	}
	if store.Key() != key || store.Source() != credentials.SourceKeyring {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}

	if err := local.Client.CallResult(t.Context(), "session/new", acp.NewSessionRequest{
		CWD:        workspace,
		MCPServers: []json.RawMessage{},
	}, &session); err != nil {
		t.Fatalf("new session after authentication: %v", err)
	}

	var logout acp.LogoutResponse
	if err := local.Client.CallResult(
		t.Context(),
		"logout",
		acp.LogoutRequest{},
		&logout,
	); err != nil {
		t.Fatal(err)
	}
	if store.Key() != "" || store.Source() != credentials.SourceNone {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	err = local.Client.CallResult(t.Context(), "session/new", acp.NewSessionRequest{
		CWD:        workspace,
		MCPServers: []json.RawMessage{},
	}, &session)
	assertRPCErrorCode(t, err, acp.ErrCodeAuthRequired)
}

func assertRPCErrorCode(t *testing.T, err error, code jrpc2.Code) {
	t.Helper()
	var rpcError *jrpc2.Error
	if !errors.As(err, &rpcError) || rpcError.Code != code {
		t.Fatalf("error = %v, want code %d", err, code)
	}
}

type modelScript func(
	context.Context,
	openrouter.Request,
	func(openrouter.Delta),
) (*openrouter.Completion, error)

type scriptedModel struct {
	mu      sync.Mutex
	scripts []modelScript
	next    int
	// entry stands in for the catalog entry session creation validates against.
	entry *openrouter.Model
}

type routedModel struct {
	route modelScript
}

func (m *routedModel) Stream(
	ctx context.Context,
	request openrouter.Request,
	onDelta func(openrouter.Delta),
) (*openrouter.Completion, error) {
	return m.route(ctx, request, onDelta)
}

func (m *routedModel) ModelInfo(
	_ context.Context,
	id string,
) (*openrouter.Model, error) {
	return &openrouter.Model{ID: id, ContextLength: 128_000}, nil
}

type blockingModel struct {
	started chan string
	release map[string]chan struct{}
	mu      sync.Mutex
}

func newBlockingModel() *blockingModel {
	return &blockingModel{
		started: make(chan string, 2),
		release: make(map[string]chan struct{}),
	}
}

func (m *blockingModel) Stream(
	ctx context.Context,
	request openrouter.Request,
	_ func(openrouter.Delta),
) (*openrouter.Completion, error) {
	m.mu.Lock()
	release := make(chan struct{})
	m.release[request.SessionID] = release
	m.mu.Unlock()
	m.started <- request.SessionID
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-release:
		return completion("done"), nil
	}
}

func (m *blockingModel) ModelInfo(
	_ context.Context,
	id string,
) (*openrouter.Model, error) {
	return &openrouter.Model{ID: id, ContextLength: 128_000}, nil
}

func (m *scriptedModel) Stream(
	ctx context.Context,
	request openrouter.Request,
	onDelta func(openrouter.Delta),
) (*openrouter.Completion, error) {
	m.mu.Lock()
	if m.next >= len(m.scripts) {
		m.mu.Unlock()
		return nil, errors.New("unexpected model request")
	}
	script := m.scripts[m.next]
	m.next++
	m.mu.Unlock()
	return script(ctx, request, onDelta)
}

func (m *scriptedModel) ModelInfo(
	_ context.Context,
	id string,
) (*openrouter.Model, error) {
	if m.entry != nil {
		return m.entry, nil
	}
	return &openrouter.Model{ID: id, ContextLength: 128_000}, nil
}

func (m *scriptedModel) assertConsumed(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.next != len(m.scripts) {
		t.Fatalf("model requests = %d, want %d", m.next, len(m.scripts))
	}
}

type capturedUpdate struct {
	Update json.RawMessage `json:"update"`
}

func (u capturedUpdate) discriminator(t *testing.T) string {
	t.Helper()
	var header struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	u.decode(t, &header)
	return header.SessionUpdate
}

func (u capturedUpdate) decode(t *testing.T, target any) {
	t.Helper()
	if err := json.Unmarshal(u.Update, target); err != nil {
		t.Fatal(err)
	}
}

type agentHarness struct {
	local       server.Local
	mu          sync.Mutex
	updatesList []capturedUpdate
}

type callbackHandler func(context.Context, *jrpc2.Request) (any, error)

// newAgentHarness drives the real runtime with OX_MODEL set, so the settings
// files play no part. Use newSettingsHarness to exercise them.
func newAgentHarness(t *testing.T, model agent.Model, tools []agent.Tool) *agentHarness {
	t.Helper()
	return newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools:         tools,
	})
}

// newSettingsHarness drives the runtime with no model override and one global
// settings layer, leaving the model and every request field to the files.
func newSettingsHarness(t *testing.T, model agent.Model, globalPath string) *agentHarness {
	t.Helper()
	return newHarness(t, agent.Config{SettingsPath: globalPath, Client: model})
}

func newHarness(t *testing.T, config agent.Config) *agentHarness {
	return newHarnessWithCallback(t, config, nil)
}

func newHarnessWithCallback(
	t *testing.T,
	config agent.Config,
	onCallback callbackHandler,
) *agentHarness {
	t.Helper()
	t.Setenv("OX_KEYRING_DISABLED", "1")
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config.Name = "test-agent"
	config.Version = "test"
	config.Logger = logger
	config.Credentials = credentials.NewStore(logger)
	instance, err := agent.New(config)
	if err != nil {
		t.Fatal(err)
	}
	harness := &agentHarness{}
	harness.local = server.NewLocal(instance.Methods(), &server.LocalOptions{
		Client: &jrpc2.ClientOptions{
			OnCallback: onCallback,
			OnNotify: func(request *jrpc2.Request) {
				if request.Method() != "session/update" {
					return
				}
				var update capturedUpdate
				if err := request.UnmarshalParams(&update); err != nil {
					return
				}
				harness.mu.Lock()
				harness.updatesList = append(harness.updatesList, update)
				harness.mu.Unlock()
			},
		},
		Server: &jrpc2.ServerOptions{AllowPush: true, Concurrency: 16},
	})
	t.Cleanup(func() {
		if err := harness.local.Close(); err != nil {
			t.Errorf("close local server: %v", err)
		}
	})
	return harness
}

// newSession names an empty workspace, so no stray settings file on the
// developer's machine can reach a test.
func (h *agentHarness) newSession(t *testing.T) string {
	t.Helper()
	return h.newSessionIn(t, t.TempDir(), nil)
}

func (h *agentHarness) newSessionIn(t *testing.T, cwd string, meta acp.Metadata) string {
	t.Helper()
	sessionID, err := h.callNewSession(cwd, meta)
	if err != nil {
		t.Fatal(err)
	}
	return sessionID
}

func (h *agentHarness) callNewSession(cwd string, meta acp.Metadata) (string, error) {
	var response acp.NewSessionResponse
	err := h.local.Client.CallResult(context.Background(), "session/new", acp.NewSessionRequest{
		CWD:        cwd,
		MCPServers: []json.RawMessage{},
		Meta:       meta,
	}, &response)
	return response.SessionID, err
}

func (h *agentHarness) prompt(t *testing.T, sessionID, text string) acp.PromptResponse {
	t.Helper()
	response, err := h.callPrompt(sessionID, text)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func (h *agentHarness) callPrompt(sessionID, text string) (acp.PromptResponse, error) {
	var response acp.PromptResponse
	err := h.local.Client.CallResult(context.Background(), "session/prompt", acp.PromptRequest{
		SessionID: sessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: text}},
	}, &response)
	return response, err
}

func (h *agentHarness) updates() []capturedUpdate {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]capturedUpdate(nil), h.updatesList...)
}

type promptResult struct {
	response acp.PromptResponse
	err      error
}

func completion(text string) *openrouter.Completion {
	return &openrouter.Completion{
		Text:         text,
		FinishReason: "stop",
		Usage: &openrouter.Usage{
			PromptTokens:     7,
			CompletionTokens: 5,
			TotalTokens:      12,
			Cost:             0.001,
		},
	}
}

func modelToolCall(id, name, arguments string) openrouter.ToolCall {
	return openrouter.ToolCall{
		ID:   id,
		Type: "function",
		Function: openrouter.ToolCallFunction{
			Name:      name,
			Arguments: arguments,
		},
	}
}
