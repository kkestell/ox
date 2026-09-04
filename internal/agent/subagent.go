package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kkestell/ox/internal/openrouter"
)

func (a *Agent) delegate(
	ctx context.Context,
	value *session,
	parentCallID string,
	prompt string,
	ask requestPermission,
	events chan<- event,
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
		completion, err := a.client.Stream(ctx, openrouter.Request{
			Model:        configuration.Settings.Model,
			Messages:     messages,
			Tools:        cloneTools(configuration.Subagent.Tools),
			CacheControl: &openrouter.CacheControl{Type: "ephemeral"},
			SessionID:    value.id,
			MaxTokens:    configuration.Settings.MaxTokens,
			Temperature:  configuration.Settings.Temperature,
			Reasoning:    configuration.Settings.Reasoning,
			Provider:     configuration.Settings.Provider,
		}, func(openrouter.Delta) {})
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
			events,
			parentCallID,
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
