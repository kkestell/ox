package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kkestell/ox/internal/openrouter"
	diagnostictrace "github.com/kkestell/ox/internal/trace"
)

func (a *Agent) delegate(
	ctx context.Context,
	value *session,
	parentCallID string,
	prompt string,
	ask requestPermission,
	fileSystem ClientFileSystem,
	terminal ClientTerminal,
	events chan<- event,
	turn diagnostictrace.Turn,
) (string, *delegationRecord, error) {
	record := &delegationRecord{Prompt: prompt}
	reads, releaseReads := value.childFileReads()
	defer releaseReads()
	configuration := value.state.configuration
	history := []openrouter.Message{{
		Role: openrouter.RoleUser,
		Content: []openrouter.ContentBlock{{
			Type: "text",
			Text: prompt,
		}},
	}}
	providerCallIDs := make(map[string]struct{})
	lastText := ""

	for requestCount := 1; requestCount <= maxTurnRequests; requestCount++ {
		if err := ctx.Err(); err != nil {
			return "", record, err
		}
		a.logger.Info(
			"starting subagent model request",
			"session_id", value.id,
			"parent_tool_call_id", parentCallID,
			"request", requestCount,
			"prefix_fingerprint", subagentPrefixFingerprint(configuration),
		)
		messages := make([]openrouter.Message, 1, len(history)+1)
		messages[0] = openrouter.Message{
			Role: openrouter.RoleSystem,
			Content: []openrouter.ContentBlock{{
				Type: "text",
				Text: configuration.Subagent.SystemPrompt,
			}},
		}
		messages = append(messages, cloneMessages(history)...)
		request := openrouter.Request{
			Model:        configuration.Settings.Model,
			Messages:     messages,
			Tools:        cloneTools(configuration.Subagent.Tools),
			CacheControl: &openrouter.CacheControl{Type: "ephemeral"},
			SessionID:    value.id,
			MaxTokens:    configuration.Settings.MaxTokens,
			Temperature:  configuration.Settings.Temperature,
			Reasoning:    configuration.Settings.Reasoning,
			Provider:     configuration.Settings.Provider,
		}
		provider := turn.Provider(
			diagnostictrace.ProviderSubagent,
			requestCount,
			providerRequestBytes(request),
			parentCallID,
		)
		completion, err := a.client.Stream(ctx, request, func(openrouter.Delta) {})
		provider.Complete(
			providerOutcome(ctx, err, completion),
			providerStopReason(completion),
			providerUsage(completion),
			providerResponseBytes(completion),
		)
		if err != nil {
			return "", record, fmt.Errorf("stream subagent model response: %w", err)
		}
		if completion.Usage != nil {
			record.Usage = append(record.Usage, *completion.Usage)
		}
		if completion.Text != "" {
			lastText = completion.Text
		}
		if completion.FinishReason != "tool_calls" || len(completion.ToolCalls) == 0 {
			if strings.TrimSpace(completion.Text) == "" {
				return "", record, errors.New("subagent returned an empty final answer")
			}
			record.Answer = completion.Text
			return completion.Text, record, nil
		}

		for _, call := range completion.ToolCalls {
			if call.ID == "" {
				return "", record, errors.New("subagent tool call ID is required")
			}
			if _, exists := providerCallIDs[call.ID]; exists {
				return "", record, fmt.Errorf("duplicate subagent provider tool call ID %q", call.ID)
			}
			providerCallIDs[call.ID] = struct{}{}
		}
		publicCalls := make([]openrouter.ToolCall, len(completion.ToolCalls))
		for index, call := range completion.ToolCalls {
			callID, err := allocateToolCallID(value)
			if err != nil {
				return "", record, err
			}
			publicCalls[index] = call
			publicCalls[index].ID = callID
		}
		results := a.executeBatchWith(
			ctx,
			value,
			a.subagentTools,
			reads,
			publicCalls,
			ask,
			fileSystem,
			terminal,
			events,
			parentCallID,
			turn,
		)
		assistant := openrouter.Message{
			Role:             openrouter.RoleAssistant,
			ToolCalls:        append([]openrouter.ToolCall(nil), completion.ToolCalls...),
			ReasoningDetails: cloneRawMessages(completion.ReasoningDetails),
		}
		if completion.Text != "" {
			assistant.Content = []openrouter.ContentBlock{{
				Type: "text",
				Text: completion.Text,
			}}
		}
		history = append(history, assistant)
		for index, call := range completion.ToolCalls {
			result := results[index]
			turn.ToolCompleted(
				publicCalls[index].ID,
				call.Function.Name,
				parentCallID,
				toolOutcome(result),
				len(result.content),
			)
			history = append(history, openrouter.Message{
				Role:       openrouter.RoleTool,
				ToolCallID: call.ID,
				Content: []openrouter.ContentBlock{{
					Type: "text",
					Text: result.content,
				}},
			})
			record.Calls = append(record.Calls, delegatedCall{
				CallID:           publicCalls[index].ID,
				Name:             call.Function.Name,
				Arguments:        json.RawMessage(call.Function.Arguments),
				Content:          result.content,
				Failed:           result.failed,
				ApprovalDecision: result.approval,
				Target:           result.target,
			})
			kind := eventToolCompleted
			if result.failed {
				kind = eventToolFailed
			}
			events <- a.toolEvent(
				a.subagentTools,
				publicCalls[index],
				kind,
				parentCallID,
				result.content,
				result.target,
			)
		}
		if requestCount == maxTurnRequests {
			answer := strings.TrimSpace(lastText)
			if answer != "" {
				answer += "\n\n"
			}
			answer += "Subagent stopped after reaching the request limit."
			record.Answer = answer
			return answer, record, nil
		}
	}
	panic("unreachable")
}
