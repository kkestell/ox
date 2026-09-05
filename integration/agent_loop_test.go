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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
			sawUsage = usage.Used == 7 && usage.Size == 128_000 &&
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

func TestWorkspaceMemoryPersistsAcrossSessionsAndReplaysRetrievedFacts(t *testing.T) {
	sessionDir := t.TempDir()
	memoryDir := t.TempDir()
	workspace := t.TempDir()
	var memoryID string
	model := &scriptedModel{scripts: []modelScript{
		toolCompletionWithArguments(
			"memory-write", "memory_write",
			`{"type":"decision","content":"Use the stable workspace memory contract"}`,
		),
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			var fact agent.MemoryFact
			if err := json.Unmarshal([]byte(messages[len(messages)-1].Content[0].Text), &fact); err != nil {
				return nil, fmt.Errorf("decode memory write result: %w", err)
			}
			memoryID = fact.ID
			return completion("stored"), nil
		},
		toolCompletionWithArguments("memory-search", "memory_search", `{"query":"stable contract"}`),
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(
				messages[len(messages)-1].Content[0].Text,
				"Use the stable workspace memory contract",
			) {
				return nil, errors.New("cross-session memory search missed stored fact")
			}
			return completion("found"), nil
		},
		func(_ context.Context, _ openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"memory-delete", "memory_delete", `{"id":"`+memoryID+`"}`,
				)},
				FinishReason: "tool_calls",
			}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(messages[len(messages)-1].Content[0].Text, memoryID) {
				return nil, errors.New("memory deletion result omitted its ID")
			}
			return completion("deleted"), nil
		},
	}}
	var approvals atomic.Int32
	first := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model", Client: model, Tools: oxtools.All(),
		SessionDir: sessionDir, MemoryDir: memoryDir,
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		var permission acp.RequestPermissionRequest
		if err := request.UnmarshalParams(&permission); err != nil {
			return nil, err
		}
		if permission.ToolCall.Name != "memory_write" && permission.ToolCall.Name != "memory_delete" {
			return nil, fmt.Errorf("unexpected memory permission: %#v", permission.ToolCall)
		}
		approvals.Add(1)
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome: "selected", OptionID: "allow_once",
		}}, nil
	})
	writer := first.newSessionIn(t, workspace, nil)
	reader := first.newSessionIn(t, workspace, nil)
	first.prompt(t, writer, "store")
	first.prompt(t, reader, "search")
	first.prompt(t, writer, "delete")
	if approvals.Load() != 2 {
		t.Fatalf("memory approvals = %d", approvals.Load())
	}
	model.assertConsumed(t)
	for _, sessionID := range []string{writer, reader} {
		if err := first.local.Client.CallResult(t.Context(), "session/close",
			acp.CloseSessionRequest{SessionID: sessionID}, &acp.CloseSessionResponse{}); err != nil {
			t.Fatal(err)
		}
	}

	replayModel := &scriptedModel{scripts: []modelScript{
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			for _, message := range messages {
				if message.Role == openrouter.RoleTool && strings.Contains(
					message.Content[0].Text, "Use the stable workspace memory contract",
				) {
					return completion("replayed"), nil
				}
			}
			return nil, errors.New("deleted live memory changed durable tool history")
		},
	}}
	second := newHarness(t, agent.Config{
		ModelOverride: "test/model", Client: replayModel, Tools: oxtools.All(),
		SessionDir: sessionDir, MemoryDir: memoryDir,
	})
	if err := second.local.Client.CallResult(t.Context(), "session/load", acp.LoadSessionRequest{
		SessionID: reader, CWD: workspace, MCPServers: []acp.MCPServer{},
	}, &acp.LoadSessionResponse{}); err != nil {
		t.Fatal(err)
	}
	second.prompt(t, reader, "recall prior search")
	replayModel.assertConsumed(t)
}

func TestPlanModeExposesMemorySearchButRejectsMemoryMutationDispatch(t *testing.T) {
	memoryDir := t.TempDir()
	model := &scriptedModel{scripts: []modelScript{
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if !requestHasTool(request, "memory_search") ||
				requestHasTool(request, "memory_write") || requestHasTool(request, "memory_delete") {
				return nil, errors.New("plan mode memory tools were not constrained")
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"hidden-memory", "memory_write", `{"type":"finding","content":"must not run"}`,
				)},
				FinishReason: "tool_calls",
			}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(messages[len(messages)-1].Content[0].Text, "unknown tool") {
				return nil, errors.New("excluded memory mutation reached dispatch")
			}
			return completion("blocked"), nil
		},
	}}
	harness := newHarness(t, agent.Config{
		ModelOverride: "test/model", Client: model, Tools: oxtools.All(), MemoryDir: memoryDir,
	})
	sessionID := harness.newSession(t)
	harness.setConfig(t, sessionID, "mode", "plan")
	harness.prompt(t, sessionID, "try hidden memory write")
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			t.Fatalf("plan-mode mutation created %s", entry.Name())
		}
	}
	model.assertConsumed(t)
}

func TestFormQuestionUsesNegotiatedElicitationAndDurableToolHistory(t *testing.T) {
	model := &scriptedModel{scripts: []modelScript{
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if !requestHasTool(request, "question") {
				return nil, errors.New("question tool was not declared")
			}
			result := completion("")
			result.FinishReason = "tool_calls"
			result.ToolCalls = []openrouter.ToolCall{modelToolCall(
				"provider-question", "question",
				`{"question":"Choose.","options":[{"label":"Safe","description":"Small changes."},{"label":"Fast"}],"default":"Safe"}`,
			)}
			return result, nil
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
			if len(messages) != 3 || messages[2].Role != openrouter.RoleTool ||
				messages[2].Content[0].Text != `{"outcome":"accepted","answer":"Safe"}` {
				return nil, fmt.Errorf("question result history = %#v", messages)
			}
			return completion("done"), nil
		},
	}}
	var callbackCount atomic.Int32
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model", Client: model, Tools: oxtools.All(),
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodElicitationCreate {
			return nil, fmt.Errorf("unexpected callback %q", request.Method())
		}
		callbackCount.Add(1)
		var form acp.CreateElicitationRequest
		if err := request.UnmarshalParams(&form); err != nil {
			return nil, err
		}
		if err := form.Validate(); err != nil {
			return nil, err
		}
		property := form.RequestedSchema.Properties["answer"]
		if form.Message != "Choose." || form.SessionID == "" || form.ToolCallID == "" ||
			len(property.OneOf) != 2 || property.OneOf[0].Title != "Safe" ||
			property.OneOf[0].Description != "Small changes." ||
			property.Default == nil || *property.Default != "Safe" {
			return nil, fmt.Errorf("elicitation request = %#v", form)
		}
		return acp.CreateElicitationResponse{
			Action:  acp.ElicitationActionAccept,
			Content: map[string]json.RawMessage{"answer": json.RawMessage(`"Safe"`)},
		}, nil
	})
	harness.initialize(t, &acp.ClientCapabilities{Elicitation: &acp.ElicitationCapabilities{
		Form: &acp.ElicitationFormCapabilities{},
	}})
	sessionID := harness.newSession(t)
	response := harness.prompt(t, sessionID, "ask")
	if response.StopReason != acp.StopReasonEndTurn || callbackCount.Load() != 1 {
		t.Fatalf("response = %#v, callbacks = %d", response, callbackCount.Load())
	}
	model.assertConsumed(t)
}

func TestFormQuestionCancellationDoesNotBlockAnotherSession(t *testing.T) {
	questionStarted := make(chan struct{})
	model := &routedModel{route: func(
		_ context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		messages, err := conversation(request)
		if err != nil {
			return nil, err
		}
		if messages[0].Content[0].Text == "other" {
			return completion("other done"), nil
		}
		result := completion("")
		result.FinishReason = "tool_calls"
		result.ToolCalls = []openrouter.ToolCall{modelToolCall(
			"held-question", "question", `{"question":"Wait?"}`,
		)}
		return result, nil
	}}
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model", Client: model, Tools: oxtools.All(),
	}, func(ctx context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodElicitationCreate {
			return nil, fmt.Errorf("unexpected callback %q", request.Method())
		}
		close(questionStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	harness.initialize(t, &acp.ClientCapabilities{Elicitation: &acp.ElicitationCapabilities{
		Form: &acp.ElicitationFormCapabilities{},
	}})
	heldSession := harness.newSession(t)
	otherSession := harness.newSession(t)
	heldResult := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(heldSession, "held")
		heldResult <- promptResult{response: response, err: err}
	}()
	<-questionStarted
	other := harness.prompt(t, otherSession, "other")
	if other.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("other response = %#v", other)
	}
	if err := harness.local.Client.Notify(t.Context(), "session/cancel", acp.CancelNotification{
		SessionID: heldSession,
	}); err != nil {
		t.Fatal(err)
	}
	result := <-heldResult
	if result.err != nil || result.response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("held response = %#v, error = %v", result.response, result.err)
	}
}

func TestPromptCompactsModelHistoryBeforeTheNextTurn(t *testing.T) {
	const oldAnswer = "old detail "
	recentAnswer := strings.Repeat("recent answer ", 100)
	var ordinarySystemPrompt string
	model := &scriptedModel{
		entry: &openrouter.Model{ID: "test/model", ContextLength: 7600, SupportedParameters: []string{"tools"}},
		scripts: []modelScript{
			func(
				_ context.Context,
				request openrouter.Request,
				_ func(openrouter.Delta),
			) (*openrouter.Completion, error) {
				ordinarySystemPrompt = request.Messages[0].Content[0].Text
				return &openrouter.Completion{
					Text: strings.Repeat(oldAnswer, 240), FinishReason: "stop",
					Usage: &openrouter.Usage{
						PromptTokens: 100, CompletionTokens: 600, TotalTokens: 700, Cost: 0.001,
					},
				}, nil
			},
			func(
				_ context.Context,
				_ openrouter.Request,
				_ func(openrouter.Delta),
			) (*openrouter.Completion, error) {
				return &openrouter.Completion{
					Text: recentAnswer, FinishReason: "stop",
					Usage: &openrouter.Usage{
						PromptTokens: 800, CompletionTokens: 5, TotalTokens: 805, Cost: 0.002,
					},
				}, nil
			},
			func(
				_ context.Context,
				request openrouter.Request,
				_ func(openrouter.Delta),
			) (*openrouter.Completion, error) {
				if len(request.Tools) != 0 || len(request.Messages) != 2 {
					return nil, fmt.Errorf("summary request = %#v", request)
				}
				if request.Messages[0].Content[0].Text == ordinarySystemPrompt ||
					request.Messages[0].Content[0].Text == "" {
					return nil, errors.New("summary request reused or omitted the ordinary system prompt")
				}
				transcript := request.Messages[1].Content[0].Text
				if !strings.Contains(transcript, strings.Repeat(oldAnswer, 20)) ||
					strings.Contains(transcript, "recent answer") {
					return nil, fmt.Errorf("summary transcript = %q", transcript)
				}
				return &openrouter.Completion{
					Text: "the old answer contained the required detail", FinishReason: "stop",
					Usage: &openrouter.Usage{
						PromptTokens: 650, CompletionTokens: 20, TotalTokens: 670, Cost: 0.004,
					},
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
				if len(request.Tools) != 1 || len(messages) != 4 {
					return nil, fmt.Errorf("compacted request = %#v", request)
				}
				if messages[0].Content[0].Text != "first" ||
					!strings.Contains(messages[1].Content[0].Text, "required detail") ||
					messages[2].Content[0].Text != recentAnswer ||
					messages[3].Content[0].Text != "third" {
					return nil, fmt.Errorf("compacted history = %#v", messages)
				}
				return completion("done"), nil
			},
		},
	}
	tools := []agent.Tool{{
		Name: "read", Description: "read a fixture", Kind: acp.ToolKindRead,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Execute: func(context.Context, agent.Invocation) (string, error) {
			return "", nil
		},
	}}
	harness := newAgentHarness(t, model, tools)
	sessionID := harness.newSession(t)
	harness.prompt(t, sessionID, "first")
	harness.prompt(t, sessionID, "second")
	beforeThird := len(harness.updates())
	harness.prompt(t, sessionID, "third")

	var occupancies []uint64
	for _, update := range harness.updates()[beforeThird:] {
		if update.discriminator(t) != "usage_update" {
			continue
		}
		var usage acp.UsageUpdate
		update.decode(t, &usage)
		occupancies = append(occupancies, usage.Used)
	}
	if len(occupancies) != 2 || occupancies[0] == 0 || occupancies[0] >= 7600 ||
		occupancies[1] != 7 {
		t.Fatalf("third-turn occupancies = %#v", occupancies)
	}
	model.assertConsumed(t)
}

func TestPromptCompactsWithinASingleToolLoop(t *testing.T) {
	oldResult := strings.Repeat("old tool result ", 300)
	recentResult := strings.Repeat("recent tool result ", 40)
	model := &scriptedModel{
		entry: &openrouter.Model{ID: "test/model", ContextLength: 9600, SupportedParameters: []string{"tools"}},
		scripts: []modelScript{
			func(context.Context, openrouter.Request, func(openrouter.Delta)) (*openrouter.Completion, error) {
				result := completion("")
				result.FinishReason = "tool_calls"
				result.ToolCalls = []openrouter.ToolCall{modelToolCall("old", "read", `{"which":"old"}`)}
				return result, nil
			},
			func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
				messages, err := conversation(request)
				if err != nil {
					return nil, err
				}
				if len(messages) != 3 || messages[2].Role != openrouter.RoleTool ||
					messages[2].Content[0].Text != oldResult {
					return nil, fmt.Errorf("first continuation = %#v", messages)
				}
				result := completion("")
				result.FinishReason = "tool_calls"
				result.ToolCalls = []openrouter.ToolCall{modelToolCall("recent", "read", `{"which":"recent"}`)}
				return result, nil
			},
			func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
				if len(request.Tools) != 0 || len(request.Messages) != 2 {
					return nil, fmt.Errorf("tool-loop summary request = %#v", request)
				}
				transcript := request.Messages[1].Content[0].Text
				if !strings.Contains(transcript, oldResult[:80]) || strings.Contains(transcript, recentResult[:80]) {
					return nil, fmt.Errorf("tool-loop summary transcript = %q", transcript)
				}
				return completion("old tool facts"), nil
			},
			func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
				messages, err := conversation(request)
				if err != nil {
					return nil, err
				}
				if len(messages) != 4 ||
					!strings.Contains(messages[1].Content[0].Text, "old tool facts") ||
					messages[2].Role != openrouter.RoleAssistant ||
					messages[3].Role != openrouter.RoleTool ||
					messages[3].ToolCallID != "recent" ||
					messages[3].Content[0].Text != recentResult {
					return nil, fmt.Errorf("compacted tool continuation = %#v", messages)
				}
				return completion("done"), nil
			},
		},
	}
	tool := agent.Tool{
		Name: "read", Description: "read a named fixture",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"which":{"type":"string"}},"required":["which"]}`),
		Kind:        acp.ToolKindRead, Approval: agent.ApprovalNone, ParallelSafe: true,
		Execute: func(_ context.Context, invocation agent.Invocation) (string, error) {
			var input struct {
				Which string `json:"which"`
			}
			if err := json.Unmarshal(invocation.Arguments, &input); err != nil {
				return "", err
			}
			if input.Which == "old" {
				return oldResult, nil
			}
			return recentResult, nil
		},
	}
	harness := newAgentHarness(t, model, []agent.Tool{tool})
	sessionID := harness.newSession(t)
	harness.prompt(t, sessionID, "work through both fixtures")
	model.assertConsumed(t)
}

func TestPromptCompactsWithinAChildToolLoop(t *testing.T) {
	oldResult := strings.Repeat("old child result ", 360)
	recentResult := strings.Repeat("recent child result ", 40)
	model := &routedModel{entry: &openrouter.Model{ID: "test/model", ContextLength: 9600, SupportedParameters: []string{"tools"}}}
	childRequests := 0
	model.route = func(
		_ context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		if len(request.Tools) == 0 {
			if len(request.Messages) != 2 {
				return nil, fmt.Errorf("child summary request = %#v", request)
			}
			transcript := request.Messages[1].Content[0].Text
			if !strings.Contains(transcript, oldResult[:80]) || strings.Contains(transcript, recentResult[:80]) {
				return nil, fmt.Errorf("child summary transcript = %q", transcript)
			}
			return completion("old child facts"), nil
		}
		if len(request.Tools) == 1 && request.Tools[0].Function.Name == "read" {
			childRequests++
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			switch childRequests {
			case 1:
				result := completion("")
				result.FinishReason = "tool_calls"
				result.ToolCalls = []openrouter.ToolCall{modelToolCall("child-old", "read", `{"which":"old"}`)}
				return result, nil
			case 2:
				if messages[len(messages)-1].Content[0].Text != oldResult {
					return nil, fmt.Errorf("first child continuation = %#v", messages)
				}
				result := completion("")
				result.FinishReason = "tool_calls"
				result.ToolCalls = []openrouter.ToolCall{modelToolCall("child-recent", "read", `{"which":"recent"}`)}
				return result, nil
			case 3:
				if len(messages) != 4 ||
					!strings.Contains(messages[1].Content[0].Text, "old child facts") ||
					messages[3].Role != openrouter.RoleTool ||
					messages[3].Content[0].Text != recentResult {
					return nil, fmt.Errorf("compacted child continuation = %#v", messages)
				}
				return completion("child done"), nil
			default:
				return nil, errors.New("unexpected child request")
			}
		}
		messages, err := conversation(request)
		if err != nil {
			return nil, err
		}
		if len(messages) == 1 {
			result := completion("")
			result.FinishReason = "tool_calls"
			result.ToolCalls = []openrouter.ToolCall{modelToolCall(
				"parent-task", "task", `{"description":"Inspect fixtures","prompt":"inspect both fixtures"}`,
			)}
			return result, nil
		}
		if len(messages) != 3 || messages[2].Role != openrouter.RoleTool ||
			messages[2].Content[0].Text != "child done" {
			return nil, fmt.Errorf("parent received child history = %#v", messages)
		}
		return completion("parent done"), nil
	}
	read := agent.Tool{
		Name: "read", Description: "read a named fixture",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"which":{"type":"string"}},"required":["which"]}`),
		Kind:        acp.ToolKindRead, Approval: agent.ApprovalNone, ParallelSafe: true,
		Execute: func(_ context.Context, invocation agent.Invocation) (string, error) {
			if strings.Contains(string(invocation.Arguments), `"old"`) {
				return oldResult, nil
			}
			return recentResult, nil
		},
	}
	task := agent.Tool{
		Name: "task", Description: "delegate a task",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"]}`),
		Kind:        acp.ToolKindOther, Approval: agent.ApprovalNone, ParallelSafe: true,
		Delegates: true, Label: func(json.RawMessage) string { return "Task" },
		Execute: func(ctx context.Context, invocation agent.Invocation) (string, error) {
			var input struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(invocation.Arguments, &input); err != nil {
				return "", err
			}
			return invocation.Delegate(ctx, input.Prompt)
		},
	}
	harness := newAgentHarness(t, model, []agent.Tool{task, read})
	sessionID := harness.newSession(t)
	harness.prompt(t, sessionID, "delegate the fixture inspection")
	if childRequests != 3 {
		t.Fatalf("child requests = %d, want 3", childRequests)
	}
}

func TestPromptCompactionFailureLeavesHistoryUntouched(t *testing.T) {
	tests := []struct {
		name    string
		summary modelScript
		problem string
	}{
		{
			name: "provider failure",
			summary: func(
				context.Context,
				openrouter.Request,
				func(openrouter.Delta),
			) (*openrouter.Completion, error) {
				return nil, errors.New("summary failed")
			},
			problem: "summary failed",
		},
		{
			name: "empty summary",
			summary: func(
				context.Context,
				openrouter.Request,
				func(openrouter.Delta),
			) (*openrouter.Completion, error) {
				return &openrouter.Completion{Text: "  ", FinishReason: "stop"}, nil
			},
			problem: "empty summary",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := &scriptedModel{
				entry:   &openrouter.Model{ID: "test/model", ContextLength: 7600},
				scripts: compactionAtomicityScripts(test.summary, ""),
			}
			harness := newAgentHarness(t, model, nil)
			sessionID := harness.newSession(t)
			harness.prompt(t, sessionID, "first")
			harness.prompt(t, sessionID, "second")
			if _, err := harness.callPrompt(sessionID, "failed prompt"); err == nil ||
				!strings.Contains(err.Error(), test.problem) {
				t.Fatalf("compaction failure = %v", err)
			}
			harness.prompt(t, sessionID, "retry")
			model.assertConsumed(t)
		})
	}
}

func TestPromptCompactionCancellationLeavesHistoryUntouched(t *testing.T) {
	started := make(chan struct{})
	model := &scriptedModel{
		entry: &openrouter.Model{ID: "test/model", ContextLength: 7600},
		scripts: compactionAtomicityScripts(func(
			ctx context.Context,
			_ openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}, "cancelled prompt"),
	}
	harness := newAgentHarness(t, model, nil)
	sessionID := harness.newSession(t)
	harness.prompt(t, sessionID, "first")
	harness.prompt(t, sessionID, "second")
	result := make(chan promptResult, 1)
	go func() {
		response, err := harness.callPrompt(sessionID, "cancelled prompt")
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
	got := <-result
	if got.err != nil || got.response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("cancelled prompt = %#v, %v", got.response, got.err)
	}
	harness.prompt(t, sessionID, "retry")
	model.assertConsumed(t)
}

func compactionAtomicityScripts(failedSummary modelScript, retainedPrompt string) []modelScript {
	oldAnswer := strings.Repeat("old detail ", 240)
	recentAnswer := strings.Repeat("recent answer ", 100)
	return []modelScript{
		func(
			_ context.Context,
			_ openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			return &openrouter.Completion{
				Text: oldAnswer, FinishReason: "stop",
				Usage: &openrouter.Usage{
					PromptTokens: 100, CompletionTokens: 600, TotalTokens: 700,
				},
			}, nil
		},
		func(
			_ context.Context,
			_ openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			return &openrouter.Completion{
				Text: recentAnswer, FinishReason: "stop",
				Usage: &openrouter.Usage{
					PromptTokens: 800, CompletionTokens: 5, TotalTokens: 805,
				},
			}, nil
		},
		failedSummary,
		func(
			_ context.Context,
			request openrouter.Request,
			_ func(openrouter.Delta),
		) (*openrouter.Completion, error) {
			if len(request.Messages) != 2 ||
				!strings.Contains(request.Messages[1].Content[0].Text, oldAnswer) {
				return nil, fmt.Errorf("retry summary request = %#v", request)
			}
			return &openrouter.Completion{
				Text: "preserved detail", FinishReason: "stop",
				Usage: &openrouter.Usage{
					PromptTokens: 650, CompletionTokens: 20, TotalTokens: 670,
				},
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
			if len(messages) < 3 || messages[len(messages)-1].Content[0].Text != "retry" {
				return nil, fmt.Errorf("history after failed compaction = %#v", messages)
			}
			var sawRetained bool
			for _, message := range messages {
				if len(message.Content) > 0 && message.Content[0].Text == retainedPrompt {
					sawRetained = true
				}
				if retainedPrompt == "" && len(message.Content) > 0 &&
					(message.Content[0].Text == "failed prompt" || message.Content[0].Text == "cancelled prompt") {
					return nil, fmt.Errorf("rejected prompt entered history: %#v", messages)
				}
			}
			if retainedPrompt != "" && !sawRetained {
				return nil, fmt.Errorf("retained prompt missing from history: %#v", messages)
			}
			return completion("done"), nil
		},
	}
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
			MCPServers: []acp.MCPServer{},
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
				messages[0].Content[0].Text != "persist me" ||
				messages[1].Content[0].Text != "persisted answer" ||
				messages[2].Content[0].Text != "remember this" {
				return nil, errors.New("second turn did not receive checkpointed history")
			}
			return completion("second persisted answer"), nil
		},
	}}
	first := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        firstModel,
		SessionDir:    sessionDir,
	})
	sessionID := first.newSessionIn(t, cwd, nil)
	first.prompt(t, sessionID, "persist me")
	first.prompt(t, sessionID, "remember this")
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
		MCPServers: []acp.MCPServer{},
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
	logData, err := os.ReadFile(filepath.Join(sessionDir, sessionID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var previousType string
	checkpointCount := 0
	for _, line := range bytes.Split(bytes.TrimSpace(logData), []byte{'\n'}) {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Type == "checkpoint" {
			checkpointCount++
			if previousType != "turn_finished" {
				t.Fatalf("checkpoint follows %q", previousType)
			}
		}
		previousType = envelope.Type
	}
	if checkpointCount != 2 {
		t.Fatalf("checkpoint records = %d, want 2", checkpointCount)
	}
	baseline := newHarness(t, agent.Config{
		ModelOverride: "test/model",
		Client:        &scriptedModel{},
		SessionDir:    sessionDir,
	})
	if err := baseline.local.Client.CallResult(
		t.Context(),
		"session/load",
		acp.LoadSessionRequest{
			SessionID:  sessionID,
			CWD:        cwd,
			MCPServers: []acp.MCPServer{},
		},
		&load,
	); err != nil {
		t.Fatal(err)
	}
	beforeReplay := baseline.updates()
	if err := baseline.local.Client.CallResult(
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
			if len(messages) != 5 ||
				messages[0].Content[0].Text != "persist me" ||
				messages[1].Content[0].Text != "persisted answer" ||
				messages[2].Content[0].Text != "remember this" ||
				messages[3].Content[0].Text != "second persisted answer" ||
				messages[4].Content[0].Text != "continue" {
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
			MCPServers: []acp.MCPServer{},
		},
		&load,
	); err != nil {
		t.Fatal(err)
	}
	afterReplay := second.updates()
	if len(afterReplay) != len(beforeReplay) {
		t.Fatalf("replay updates = %d, want %d", len(afterReplay), len(beforeReplay))
	}
	beforeUpdates := make([]string, len(beforeReplay))
	afterUpdates := make([]string, len(afterReplay))
	for index := range beforeReplay {
		beforeUpdates[index] = string(beforeReplay[index].Update)
		afterUpdates[index] = string(afterReplay[index].Update)
	}
	sort.Strings(beforeUpdates)
	sort.Strings(afterUpdates)
	for index := range beforeUpdates {
		if afterUpdates[index] != beforeUpdates[index] {
			t.Fatalf(
				"replay update %d changed:\nafter = %s\nbefore = %s",
				index,
				afterUpdates[index],
				beforeUpdates[index],
			)
		}
	}
	var sawUser, sawAnswer bool
	for _, update := range afterReplay {
		if strings.Contains(string(update.Update), "<environment>") {
			t.Fatalf("system prompt entered ACP replay: %s", update.Update)
		}
		switch update.discriminator(t) {
		case "user_message_chunk":
			sawUser = true
		case "agent_message_chunk":
			var chunk acp.AgentMessageChunk
			update.decode(t, &chunk)
			sawAnswer = sawAnswer || chunk.Content.Text == "persisted answer"
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
			if len(request.Tools) != 13 {
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
			MCPServers: []acp.MCPServer{},
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
			MCPServers: []acp.MCPServer{},
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
	var terminalMu sync.Mutex
	var terminalMethods []string
	var sessionID string
	model := &routedModel{route: func(
		_ context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		system := request.Messages[0].Content[0].Text
		if strings.HasPrefix(system, "You are a subagent") {
			if len(request.Tools) != 11 {
				return nil, fmt.Errorf("subagent tools = %d, want 11", len(request.Tools))
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
		terminalMu.Lock()
		defer terminalMu.Unlock()
		switch request.Method() {
		case acp.MethodSessionRequestPermission:
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
		case acp.MethodTerminalCreate:
			var create acp.CreateTerminalRequest
			if err := request.UnmarshalParams(&create); err != nil {
				return nil, err
			}
			if create.SessionID != sessionID || create.Command != "/bin/sh" ||
				len(create.Args) != 2 || create.Args[0] != "-c" ||
				create.Args[1] != "printf child-shell" {
				return nil, fmt.Errorf("child terminal create = %#v", create)
			}
			terminalMethods = append(terminalMethods, request.Method())
			return acp.CreateTerminalResponse{TerminalID: "child-terminal"}, nil
		case acp.MethodTerminalWaitForExit:
			terminalMethods = append(terminalMethods, request.Method())
			zero := 0
			return acp.WaitForTerminalExitResponse{ExitCode: &zero}, nil
		case acp.MethodTerminalOutput:
			terminalMethods = append(terminalMethods, request.Method())
			return acp.TerminalOutputResponse{Output: "child-shell"}, nil
		case acp.MethodTerminalRelease:
			terminalMethods = append(terminalMethods, request.Method())
			return acp.ReleaseTerminalResponse{}, nil
		default:
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
	})
	harness.initialize(t, &acp.ClientCapabilities{Terminal: true})
	workspace := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(workspace, "AGENTS.md"),
		[]byte("guide\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	sessionID = harness.newSessionIn(t, workspace, nil)
	response := harness.prompt(t, sessionID, "delegate both")
	if response.StopReason != acp.StopReasonEndTurn ||
		response.Usage == nil ||
		response.Usage.TotalTokens != 72 {
		t.Fatalf("response = %#v", response)
	}
	if approvals.Load() != 1 {
		t.Fatalf("approval requests = %d", approvals.Load())
	}
	terminalMu.Lock()
	gotTerminalMethods := append([]string(nil), terminalMethods...)
	terminalMu.Unlock()
	wantTerminalMethods := []string{
		acp.MethodTerminalCreate,
		acp.MethodTerminalWaitForExit,
		acp.MethodTerminalOutput,
		acp.MethodTerminalRelease,
	}
	if fmt.Sprint(gotTerminalMethods) != fmt.Sprint(wantTerminalMethods) {
		t.Fatalf("child terminal methods = %v, want %v", gotTerminalMethods, wantTerminalMethods)
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
			MCPServers: []acp.MCPServer{},
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
			MCPServers: []acp.MCPServer{},
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
			MCPServers: []acp.MCPServer{},
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
			MCPServers: []acp.MCPServer{},
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
			MCPServers: []acp.MCPServer{},
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
			MCPServers: []acp.MCPServer{},
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

func TestDelegatedFilesystemPreservesToolSemanticsAndContinuesAfterClientError(t *testing.T) {
	root := t.TempDir()
	localPath := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(localPath, []byte("local contents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	absolutePath, err := filepath.EvalSymlinks(localPath)
	if err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(filepath.Dir(absolutePath), "missing.txt")
	const initial = "\ufeffone\r\ntwo\r\nthree"
	clientFiles := map[string]string{absolutePath: initial}

	model := &scriptedModel{scripts: []modelScript{
		func(context.Context, openrouter.Request, func(openrouter.Delta)) (*openrouter.Completion, error) {
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"read-delegated",
					"read_file",
					`{"path":"notes.txt","offset":2,"limit":1}`,
				)},
				FinishReason: "tool_calls",
			}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			want := "two\n[lines 2-2 of 3; pass offset/limit to read more]"
			if got := messages[len(messages)-1].Content[0].Text; got != want {
				return nil, fmt.Errorf("delegated read result = %q, want %q", got, want)
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"write-delegated",
					"write_file",
					`{"path":"notes.txt","content":"alpha\nbeta\n"}`,
				)},
				FinishReason: "tool_calls",
			}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "Wrote notes.txt") {
				return nil, fmt.Errorf("delegated write result = %q", got)
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"edit-delegated",
					"edit_file",
					`{"path":"notes.txt","old_string":"beta","new_string":"BETA"}`,
				)},
				FinishReason: "tool_calls",
			}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "Edited notes.txt") {
				return nil, fmt.Errorf("delegated edit result = %q", got)
			}
			return &openrouter.Completion{
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"read-error",
					"read_file",
					`{"path":"missing.txt"}`,
				)},
				FinishReason: "tool_calls",
			}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			messages, err := conversation(request)
			if err != nil {
				return nil, err
			}
			if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "missing delegated file") {
				return nil, fmt.Errorf("delegated read error result = %q", got)
			}
			return completion("continued after delegated error"), nil
		},
	}}

	var sessionID string
	var callbackMu sync.Mutex
	var filesystemMethods []string
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		Tools:         oxtools.All(),
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		switch request.Method() {
		case acp.MethodSessionRequestPermission:
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
				Outcome:  "selected",
				OptionID: "allow_once",
			}}, nil
		case acp.MethodFSReadTextFile:
			var read acp.ReadTextFileRequest
			if err := request.UnmarshalParams(&read); err != nil {
				return nil, err
			}
			if read.SessionID != sessionID || !filepath.IsAbs(read.Path) ||
				read.Line != nil || read.Limit != nil {
				return nil, fmt.Errorf("delegated read request = %#v", read)
			}
			callbackMu.Lock()
			filesystemMethods = append(filesystemMethods, request.Method())
			content, ok := clientFiles[read.Path]
			callbackMu.Unlock()
			if !ok {
				return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing delegated file")
			}
			return acp.ReadTextFileResponse{Content: content}, nil
		case acp.MethodFSWriteTextFile:
			var write acp.WriteTextFileRequest
			if err := request.UnmarshalParams(&write); err != nil {
				return nil, err
			}
			if write.SessionID != sessionID || write.Path != absolutePath {
				return nil, fmt.Errorf("delegated write request = %#v", write)
			}
			callbackMu.Lock()
			filesystemMethods = append(filesystemMethods, request.Method())
			clientFiles[write.Path] = write.Content
			callbackMu.Unlock()
			return acp.WriteTextFileResponse{}, nil
		default:
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
	})
	harness.initialize(t, &acp.ClientCapabilities{FS: &acp.FileSystemCapabilities{
		ReadTextFile:  true,
		WriteTextFile: true,
	}})
	sessionID = harness.newSessionIn(t, root, nil)
	harness.prompt(t, sessionID, "use delegated files")

	if data, err := os.ReadFile(localPath); err != nil || string(data) != "local contents\n" {
		t.Fatalf("local file = %q, %v", data, err)
	}
	callbackMu.Lock()
	gotClient := clientFiles[absolutePath]
	gotMethods := append([]string(nil), filesystemMethods...)
	callbackMu.Unlock()
	if want := "\ufeffalpha\r\nBETA"; gotClient != want {
		t.Fatalf("delegated file = %q, want %q", gotClient, want)
	}
	wantMethods := []string{
		acp.MethodFSReadTextFile,
		acp.MethodFSReadTextFile,
		acp.MethodFSWriteTextFile,
		acp.MethodFSReadTextFile,
		acp.MethodFSWriteTextFile,
		acp.MethodFSReadTextFile,
	}
	if fmt.Sprint(gotMethods) != fmt.Sprint(wantMethods) {
		t.Fatalf("filesystem methods = %v, want %v", gotMethods, wantMethods)
	}
	if missingPath == absolutePath {
		t.Fatal("invalid test paths")
	}
	model.assertConsumed(t)
}

func TestFilesystemCapabilitiesSelectEachMethodIndependently(t *testing.T) {
	for _, test := range []struct {
		name       string
		capability acp.FileSystemCapabilities
		wantLocal  string
		wantClient string
		wantReads  int
		wantWrites int
	}{
		{
			name:       "delegated read and local write",
			capability: acp.FileSystemCapabilities{ReadTextFile: true},
			wantLocal:  "after\n",
			wantClient: "before\n",
			wantReads:  1,
		},
		{
			name:       "local read and delegated write",
			capability: acp.FileSystemCapabilities{WriteTextFile: true},
			wantLocal:  "before\n",
			wantClient: "after\n",
			wantWrites: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "notes.txt")
			if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			absolute, err := filepath.EvalSymlinks(path)
			if err != nil {
				t.Fatal(err)
			}
			clientContent := "before\n"
			model := &scriptedModel{scripts: []modelScript{
				toolCompletionWithArguments("read", "read_file", `{"path":"notes.txt"}`),
				toolCompletionWithArguments(
					"write",
					"write_file",
					`{"path":"notes.txt","content":"after\n"}`,
				),
				func(context.Context, openrouter.Request, func(openrouter.Delta)) (*openrouter.Completion, error) {
					return completion("done"), nil
				},
			}}
			reads, writes := 0, 0
			var sessionID string
			harness := newHarnessWithCallback(t, agent.Config{
				ModelOverride: "test/model",
				Client:        model,
				Tools:         oxtools.All(),
			}, func(_ context.Context, request *jrpc2.Request) (any, error) {
				switch request.Method() {
				case acp.MethodSessionRequestPermission:
					return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
						Outcome:  "selected",
						OptionID: "allow_once",
					}}, nil
				case acp.MethodFSReadTextFile:
					var read acp.ReadTextFileRequest
					if err := request.UnmarshalParams(&read); err != nil {
						return nil, err
					}
					if read.SessionID != sessionID || read.Path != absolute {
						return nil, fmt.Errorf("read request = %#v", read)
					}
					reads++
					return acp.ReadTextFileResponse{Content: clientContent}, nil
				case acp.MethodFSWriteTextFile:
					var write acp.WriteTextFileRequest
					if err := request.UnmarshalParams(&write); err != nil {
						return nil, err
					}
					if write.SessionID != sessionID || write.Path != absolute {
						return nil, fmt.Errorf("write request = %#v", write)
					}
					writes++
					clientContent = write.Content
					return acp.WriteTextFileResponse{}, nil
				default:
					return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
				}
			})
			harness.initialize(t, &acp.ClientCapabilities{FS: &test.capability})
			sessionID = harness.newSessionIn(t, root, nil)
			harness.prompt(t, sessionID, "read then write")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != test.wantLocal || clientContent != test.wantClient ||
				reads != test.wantReads || writes != test.wantWrites {
				t.Fatalf(
					"local=%q client=%q reads=%d writes=%d",
					data,
					clientContent,
					reads,
					writes,
				)
			}
			model.assertConsumed(t)
		})
	}
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
	canonicalPath, err := filepath.EvalSymlinks(path)
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
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		var permission acp.RequestPermissionRequest
		if err := request.UnmarshalParams(&permission); err != nil {
			return nil, err
		}
		if len(permission.ToolCall.Locations) != 1 ||
			permission.ToolCall.Locations[0].Path != canonicalPath {
			return nil, fmt.Errorf("permission locations = %#v", permission.ToolCall.Locations)
		}
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  "selected",
			OptionID: "reject_once",
		}}, nil
	})
	sessionID := harness.newSessionIn(t, workspace, nil)
	harness.prompt(t, sessionID, "edit the notes")
	live := harness.updates()
	assertToolLocation(t, live, "edit-rejected", canonicalPath)
	replayStart := len(live)
	if err := harness.local.Client.CallResult(
		t.Context(), "session/close", acp.CloseSessionRequest{SessionID: sessionID},
		&acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(
		t.Context(), "session/load", acp.LoadSessionRequest{
			SessionID: sessionID, CWD: workspace, MCPServers: []acp.MCPServer{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	assertToolLocation(t, harness.updates()[replayStart:], "edit-rejected", canonicalPath)
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

func assertToolLocation(t *testing.T, updates []capturedUpdate, callID, want string) {
	t.Helper()
	for _, update := range updates {
		if update.discriminator(t) != "tool_call" {
			continue
		}
		var call acp.ToolCall
		update.decode(t, &call)
		if call.ToolCallID != callID {
			continue
		}
		if len(call.Locations) != 1 || call.Locations[0].Path != want {
			t.Fatalf("tool %q locations = %#v", callID, call.Locations)
		}
		return
	}
	t.Fatalf("tool %q was not published", callID)
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
			MCPServers: []acp.MCPServer{},
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
			MCPServers: []acp.MCPServer{},
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
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	harness := newHarnessWithCallback(t, agent.Config{
		ModelOverride: "test/model",
		Client:        model,
		SessionDir:    sessionDir,
		Tools:         oxtools.All(),
	}, func(_ context.Context, request *jrpc2.Request) (any, error) {
		if request.Method() != acp.MethodSessionRequestPermission {
			return nil, jrpc2.Errorf(jrpc2.MethodNotFound, "unknown callback")
		}
		var permission acp.RequestPermissionRequest
		if err := request.UnmarshalParams(&permission); err != nil {
			return nil, err
		}
		want := filepath.Join(canonicalWorkspace, "child.txt")
		if len(permission.ToolCall.Locations) != 1 ||
			permission.ToolCall.Locations[0].Path != want {
			return nil, fmt.Errorf("child permission locations = %#v", permission.ToolCall.Locations)
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

	live := harness.updates()
	wantLocation := filepath.Join(canonicalWorkspace, "child.txt")
	var childCallID string
	for _, update := range live {
		if update.discriminator(t) != "tool_call" {
			continue
		}
		var call acp.ToolCall
		update.decode(t, &call)
		if call.Name == "write_file" && call.Meta[acp.MetaParentToolCallID] == "task-write" {
			childCallID = call.ToolCallID
			assertToolLocation(t, live, childCallID, wantLocation)
			break
		}
	}
	if childCallID == "" {
		t.Fatal("completed child mutation was absent from live updates")
	}
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
			MCPServers: []acp.MCPServer{},
		},
		&acp.LoadSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	replay := harness.updates()[beforeReplay:]
	var replayedChild bool
	for _, update := range replay {
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
	assertToolLocation(t, replay, childCallID, wantLocation)
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
			ID:                  "global/model",
			ContextLength:       128_000,
			SupportedParameters: []string{"tools", "temperature", "max_tokens"},
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

func TestWorkspaceInstructionsFreezeAcrossParentAndChildUntilReactivation(t *testing.T) {
	workspace := t.TempDir()
	parent := filepath.Dir(workspace)
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("parent rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "nested", "AGENTS.md"), []byte("nested rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(workspace, "AGENTS.md")
	if err := os.WriteFile(rootPath, []byte("original rules\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	assertPrompt := func(request openrouter.Request, want string) error {
		system := request.Messages[0].Content[0].Text
		block := "<workspace-instructions>\n" + want + "\n</workspace-instructions>"
		if strings.Count(system, block) != 1 {
			return fmt.Errorf("system prompt instruction block = %q", system)
		}
		if strings.Contains(system, "parent rules") || strings.Contains(system, "nested rules") {
			return fmt.Errorf("system prompt loaded instructions outside the root: %q", system)
		}
		messages, err := conversation(request)
		if err != nil {
			return err
		}
		for _, message := range messages {
			for _, content := range message.Content {
				if strings.Contains(content.Text, "<workspace-instructions>") {
					return errors.New("workspace instructions entered conversation history")
				}
			}
		}
		return nil
	}

	model := &scriptedModel{scripts: []modelScript{
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if err := assertPrompt(request, "original rules\r\n"); err != nil {
				return nil, err
			}
			return &openrouter.Completion{FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{
				modelToolCall("task-original", "task", `{"description":"check","prompt":"check rules"}`),
			}}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if !strings.HasPrefix(request.Messages[0].Content[0].Text, "You are a subagent") {
				return nil, errors.New("expected subagent request")
			}
			if err := assertPrompt(request, "original rules\r\n"); err != nil {
				return nil, err
			}
			return completion("child original"), nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if err := assertPrompt(request, "original rules\r\n"); err != nil {
				return nil, err
			}
			return completion("parent original"), nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if err := assertPrompt(request, "reactivated rules\n"); err != nil {
				return nil, err
			}
			return &openrouter.Completion{FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{
				modelToolCall("task-reactivated", "task", `{"description":"check","prompt":"check rules again"}`),
			}}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if !strings.HasPrefix(request.Messages[0].Content[0].Text, "You are a subagent") {
				return nil, errors.New("expected reactivated subagent request")
			}
			if err := assertPrompt(request, "reactivated rules\n"); err != nil {
				return nil, err
			}
			return completion("child reactivated"), nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if err := assertPrompt(request, "reactivated rules\n"); err != nil {
				return nil, err
			}
			return completion("parent reactivated"), nil
		},
	}}
	harness := newAgentHarness(t, model, oxtools.All())
	sessionID := harness.newSessionIn(t, workspace, nil)
	if err := os.WriteFile(rootPath, []byte("reactivated rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	harness.prompt(t, sessionID, "first")
	if err := harness.local.Client.CallResult(t.Context(), "session/close",
		acp.CloseSessionRequest{SessionID: sessionID}, &acp.CloseSessionResponse{},
	); err != nil {
		t.Fatal(err)
	}
	if err := harness.local.Client.CallResult(t.Context(), "session/load", acp.LoadSessionRequest{
		SessionID: sessionID, CWD: workspace, MCPServers: []acp.MCPServer{},
	}, &acp.LoadSessionResponse{}); err != nil {
		t.Fatal(err)
	}
	harness.prompt(t, sessionID, "second")
	model.assertConsumed(t)
}

func TestWorkspaceSkillsShareCatalogAcrossParentAndChild(t *testing.T) {
	workspace := t.TempDir()
	skillPath := filepath.Join(workspace, ".agents", "skills", "inspect", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte(`---
name: inspect
description: Inspect workspace fixtures.
---
Read the fixture carefully.
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var parentCatalog, childCatalog string
	model := &routedModel{route: func(
		_ context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		system := request.Messages[0].Content[0].Text
		start := strings.Index(system, "<skills>")
		end := strings.Index(system, "</skills>")
		if start < 0 || end < start {
			return nil, errors.New("request omitted workspace skill catalog")
		}
		catalog := system[start : end+len("</skills>")]
		messages, err := conversation(request)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(system, "You are a subagent") {
			childCatalog = catalog
			if !requestHasTool(request, "skill") {
				return nil, errors.New("child omitted skill tool")
			}
			if messages[len(messages)-1].Role == openrouter.RoleUser {
				return &openrouter.Completion{FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{
					modelToolCall("child-skill", "skill", `{"name":"inspect"}`),
				}}, nil
			}
			if !strings.Contains(messages[len(messages)-1].Content[0].Text, "Read the fixture carefully") {
				return nil, errors.New("child did not receive loaded skill body")
			}
			return completion("child done"), nil
		}
		parentCatalog = catalog
		if len(messages) == 1 {
			return &openrouter.Completion{FinishReason: "tool_calls", ToolCalls: []openrouter.ToolCall{
				modelToolCall("task-skill", "task", `{"description":"inspect","prompt":"use the inspect skill"}`),
			}}, nil
		}
		return completion("parent done"), nil
	}}
	harness := newAgentHarness(t, model, oxtools.All())
	harness.prompt(t, harness.newSessionIn(t, workspace, nil), "delegate skill use")
	if parentCatalog == "" || childCatalog != parentCatalog {
		t.Fatalf("catalogs differ:\nparent = %q\nchild = %q", parentCatalog, childCatalog)
	}
}

func TestOversizedRenderedWorkspaceSkillCatalogRejectsActivation(t *testing.T) {
	workspace := t.TempDir()
	for index := 0; index < 128; index++ {
		name := fmt.Sprintf("skill-%03d", index)
		path := filepath.Join(workspace, ".agents", "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(
			"---\nname: %s\ndescription: %s\n---\nbody\n",
			name,
			strings.Repeat("x", 600),
		)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	harness := newAgentHarness(t, &scriptedModel{}, oxtools.All())
	_, err := harness.callNewSession(workspace, nil)
	assertRPCErrorCode(t, err, jrpc2.InternalError)
	if !strings.Contains(err.Error(), "skill catalog renders") ||
		!strings.Contains(err.Error(), "maximum") {
		t.Fatalf("activation error = %v", err)
	}
}

func TestPlanModeHidesAndRejectsEffectfulTools(t *testing.T) {
	var executed atomic.Int32
	mutate := agent.Tool{
		Name: "mutate", Kind: acp.ToolKindEdit, Approval: agent.ApprovalNone,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Execute: func(context.Context, agent.Invocation) (string, error) {
			executed.Add(1)
			return "changed", nil
		},
	}
	read := agent.Tool{
		Name: "read", Kind: acp.ToolKindRead, Approval: agent.ApprovalNone,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Execute:     func(context.Context, agent.Invocation) (string, error) { return "read", nil },
	}
	model := &scriptedModel{
		entry: &openrouter.Model{
			ID: "test/model", ContextLength: 128_000,
			SupportedParameters: []string{"tools"},
		},
		scripts: []modelScript{
			func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
				if len(request.Tools) != 1 || request.Tools[0].Function.Name != "read" {
					return nil, fmt.Errorf("plan tools = %#v", request.Tools)
				}
				return &openrouter.Completion{
					FinishReason: "tool_calls",
					ToolCalls:    []openrouter.ToolCall{modelToolCall("hidden", "mutate", `{}`)},
				}, nil
			},
			func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
				messages, err := conversation(request)
				if err != nil {
					return nil, err
				}
				if got := messages[len(messages)-1].Content[0].Text; !strings.Contains(got, "unknown tool") {
					return nil, fmt.Errorf("hidden tool result = %q", got)
				}
				return completion("done"), nil
			},
		},
	}
	harness := newAgentHarness(t, model, []agent.Tool{read, mutate})
	sessionID := harness.newSession(t)
	response := harness.setConfig(t, sessionID, "mode", "plan")
	if response.ConfigOptions[0].CurrentValue != "plan" {
		t.Fatalf("mode = %q", response.ConfigOptions[0].CurrentValue)
	}
	harness.prompt(t, sessionID, "inspect")
	if executed.Load() != 0 {
		t.Fatal("effectful tool executed in plan mode")
	}
	model.assertConsumed(t)
}

func TestTodoProjectsDurablePlanAndCurrentParentContext(t *testing.T) {
	model := &scriptedModel{scripts: []modelScript{
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if !requestHasTool(request, "todo") {
				return nil, fmt.Errorf("code tools omit todo: %#v", request.Tools)
			}
			return &openrouter.Completion{
				FinishReason: "tool_calls",
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"todo-set", "todo",
					`{"todos":[{"content":"Ship it","priority":"high","status":"in_progress"}]}`,
				)},
			}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if !requestHasTodoContext(request, `"content":"Ship it"`) {
				return nil, fmt.Errorf("request omits current todo context: %#v", request.Messages)
			}
			return &openrouter.Completion{
				FinishReason: "tool_calls",
				ToolCalls: []openrouter.ToolCall{
					modelToolCall("todo-clear", "todo", `{"todos":[]}`),
				},
			}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if requestHasTodoContext(request, "") {
				return nil, fmt.Errorf("cleared todo remained in context: %#v", request.Messages)
			}
			return completion("done"), nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if !requestHasTool(request, "todo") || !requestHasTool(request, "skill") ||
				requestHasTool(request, "write_file") {
				return nil, fmt.Errorf("plan tools = %#v", request.Tools)
			}
			return completion("planned"), nil
		},
	}}
	harness := newAgentHarness(t, model, oxtools.All())
	sessionID := harness.newSession(t)
	harness.prompt(t, sessionID, "track the work")
	var plans []acp.Plan
	for _, update := range harness.updates() {
		if update.discriminator(t) != acp.SessionUpdatePlan {
			continue
		}
		var plan acp.Plan
		update.decode(t, &plan)
		plans = append(plans, plan)
	}
	if len(plans) != 2 || len(plans[0].Entries) != 1 ||
		plans[0].Entries[0].Content != "Ship it" || plans[1].Entries == nil ||
		len(plans[1].Entries) != 0 {
		t.Fatalf("plan updates = %#v", plans)
	}

	planSession := harness.newSession(t)
	harness.setConfig(t, planSession, "mode", "plan")
	harness.prompt(t, planSession, "make a plan")
	model.assertConsumed(t)
}

func TestChildCannotSeeOrReplaceParentTodo(t *testing.T) {
	var childRequests int
	model := &routedModel{route: func(
		_ context.Context,
		request openrouter.Request,
		_ func(openrouter.Delta),
	) (*openrouter.Completion, error) {
		system := request.Messages[0].Content[0].Text
		messages, err := conversation(request)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(system, "You are a subagent") {
			childRequests++
			if requestHasTool(request, "todo") {
				return nil, errors.New("child received todo declaration")
			}
			if childRequests == 1 {
				return &openrouter.Completion{
					FinishReason: "tool_calls",
					ToolCalls: []openrouter.ToolCall{
						modelToolCall("fabricated-todo", "todo", `{"todos":[]}`),
					},
				}, nil
			}
			if !strings.Contains(messages[len(messages)-1].Content[0].Text, "unknown tool") {
				return nil, fmt.Errorf("fabricated child result = %#v", messages[len(messages)-1])
			}
			return completion("child done"), nil
		}
		if len(messages) == 1 {
			return &openrouter.Completion{
				FinishReason: "tool_calls",
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"task-call", "task", `{"description":"check","prompt":"check it"}`,
				)},
			}, nil
		}
		return completion("parent done"), nil
	}}
	harness := newAgentHarness(t, model, oxtools.All())
	harness.prompt(t, harness.newSession(t), "delegate")
	for _, update := range harness.updates() {
		if update.discriminator(t) == acp.SessionUpdatePlan {
			t.Fatal("fabricated child todo changed the parent plan")
		}
	}
	if childRequests != 1 {
		t.Fatalf("child requests = %d, want 1 rejected dispatch", childRequests)
	}
}

func TestTodoSurvivesCloseLoadAndReplay(t *testing.T) {
	sessionDir := t.TempDir()
	workspace := t.TempDir()
	firstModel := &scriptedModel{scripts: []modelScript{
		func(_ context.Context, _ openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			return &openrouter.Completion{
				FinishReason: "tool_calls",
				ToolCalls: []openrouter.ToolCall{modelToolCall(
					"todo-set", "todo", `{"todos":[{"content":"Resume me","status":"pending"}]}`,
				)},
			}, nil
		},
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if !requestHasTodoContext(request, `"content":"Resume me"`) {
				return nil, errors.New("first activation lost todo context")
			}
			return completion("paused"), nil
		},
	}}
	first := newHarness(t, agent.Config{
		ModelOverride: "test/model", Client: firstModel,
		SessionDir: sessionDir, Tools: oxtools.All(),
	})
	sessionID := first.newSessionIn(t, workspace, nil)
	first.prompt(t, sessionID, "start")
	var closed acp.CloseSessionResponse
	if err := first.local.Client.CallResult(t.Context(), "session/close",
		acp.CloseSessionRequest{SessionID: sessionID}, &closed,
	); err != nil {
		t.Fatal(err)
	}
	firstModel.assertConsumed(t)

	secondModel := &scriptedModel{scripts: []modelScript{
		func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
			if !requestHasTodoContext(request, `"content":"Resume me"`) {
				return nil, fmt.Errorf("reactivated request lost todo: %#v", request.Messages)
			}
			return completion("resumed"), nil
		},
	}}
	second := newHarness(t, agent.Config{
		ModelOverride: "test/model", Client: secondModel,
		SessionDir: sessionDir, Tools: oxtools.All(),
	})
	var loaded acp.LoadSessionResponse
	if err := second.local.Client.CallResult(t.Context(), "session/load", acp.LoadSessionRequest{
		SessionID: sessionID, CWD: workspace, MCPServers: []acp.MCPServer{},
	}, &loaded); err != nil {
		t.Fatal(err)
	}
	var replayed bool
	for _, update := range second.updates() {
		if update.discriminator(t) != acp.SessionUpdatePlan {
			continue
		}
		var plan acp.Plan
		update.decode(t, &plan)
		replayed = len(plan.Entries) == 1 && plan.Entries[0].Content == "Resume me"
	}
	if !replayed {
		t.Fatal("session load did not replay the durable plan")
	}
	second.prompt(t, sessionID, "continue")
	secondModel.assertConsumed(t)
}

func requestHasTool(request openrouter.Request, name string) bool {
	for _, tool := range request.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func requestHasTodoContext(request openrouter.Request, contains string) bool {
	for index, message := range request.Messages {
		if index == 0 || message.Role != openrouter.RoleSystem || len(message.Content) != 1 ||
			!strings.HasPrefix(message.Content[0].Text, "Current todo progress state") {
			continue
		}
		return contains == "" || strings.Contains(message.Content[0].Text, contains)
	}
	return false
}

func TestConfigurationChangeDuringTurnAppliesToNextTurn(t *testing.T) {
	started := make(chan openrouter.Request, 1)
	release := make(chan struct{})
	var second openrouter.Request
	initial := openrouter.Model{
		ID: "test/model", ContextLength: 128_000,
		Reasoning: &openrouter.ModelReasoning{SupportedEfforts: []string{"high"}},
	}
	next := openrouter.Model{
		ID: "next/model", ContextLength: 64_000,
		Reasoning: &openrouter.ModelReasoning{SupportedEfforts: []string{"low"}},
	}
	model := &catalogScriptedModel{
		scriptedModel: &scriptedModel{
			entry: &initial,
			scripts: []modelScript{
				func(_ context.Context, request openrouter.Request, _ func(openrouter.Delta)) (*openrouter.Completion, error) {
					started <- request
					<-release
					return completion("first"), nil
				},
				captureRequest(&second),
			}},
		models: []openrouter.Model{next, initial},
	}
	harness := newAgentHarness(t, model, nil)
	sessionID := harness.newSession(t)
	firstDone := make(chan error, 1)
	go func() {
		_, err := harness.callPrompt(sessionID, "first")
		firstDone <- err
	}()
	first := <-started
	response := harness.setConfig(t, sessionID, "model", "next/model")
	if currentOptionValue(response.ConfigOptions, "model") != "next/model" ||
		currentOptionValue(response.ConfigOptions, "reasoning") != "default" {
		t.Fatalf("updated options = %#v", response.ConfigOptions)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	harness.prompt(t, sessionID, "second")
	if first.Model != "test/model" || second.Model != "next/model" || second.Reasoning != nil {
		t.Fatalf("request models/reasoning = %q then %q / %#v", first.Model, second.Model, second.Reasoning)
	}
	model.scriptedModel.assertConsumed(t)
}

func currentOptionValue(options []acp.SessionConfigOption, id string) string {
	for _, option := range options {
		if option.ID == id {
			return option.CurrentValue
		}
	}
	return ""
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := credentials.NewStore(logger, "", false)
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
		MCPServers: []acp.MCPServer{},
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
		MCPServers: []acp.MCPServer{},
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
		MCPServers: []acp.MCPServer{},
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
	entry *openrouter.Model
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
	if m.entry != nil {
		return m.entry, nil
	}
	return defaultTestModel(id), nil
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
	return defaultTestModel(id), nil
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
	return defaultTestModel(id), nil
}

type catalogScriptedModel struct {
	*scriptedModel
	models []openrouter.Model
}

func (m *catalogScriptedModel) Models(_ context.Context) ([]openrouter.Model, error) {
	return append([]openrouter.Model(nil), m.models...), nil
}

func defaultTestModel(id string) *openrouter.Model {
	return &openrouter.Model{
		ID: id, ContextLength: 128_000,
		SupportedParameters: []string{"tools", "temperature", "max_tokens"},
		Architecture:        openrouter.Architecture{InputModalities: []string{"text", "image", "audio"}},
	}
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

// newAgentHarness drives the runtime with a model override, so the settings
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config.Name = "test-agent"
	config.Version = "test"
	config.Logger = logger
	config.Credentials = credentials.NewStore(logger, "test-key", true)
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
		MCPServers: []acp.MCPServer{},
		Meta:       meta,
	}, &response)
	return response.SessionID, err
}

func (h *agentHarness) initialize(t *testing.T, capabilities *acp.ClientCapabilities) {
	t.Helper()
	var response acp.InitializeResponse
	if err := h.local.Client.CallResult(t.Context(), "initialize", acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion,
		ClientCapabilities: capabilities,
	}, &response); err != nil {
		t.Fatal(err)
	}
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

func (h *agentHarness) setConfig(
	t *testing.T,
	sessionID, id, value string,
) acp.SetSessionConfigOptionResponse {
	t.Helper()
	var response acp.SetSessionConfigOptionResponse
	if err := h.local.Client.CallResult(t.Context(), acp.MethodSessionSetConfigOption,
		acp.SetSessionConfigOptionRequest{SessionID: sessionID, ConfigID: id, Value: value},
		&response,
	); err != nil {
		t.Fatal(err)
	}
	return response
}

func (h *agentHarness) updates() []capturedUpdate {
	const quietPeriod = 25 * time.Millisecond
	for {
		h.mu.Lock()
		count := len(h.updatesList)
		h.mu.Unlock()
		time.Sleep(quietPeriod)
		h.mu.Lock()
		if len(h.updatesList) == count {
			updates := append([]capturedUpdate(nil), h.updatesList...)
			h.mu.Unlock()
			return updates
		}
		h.mu.Unlock()
	}
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
