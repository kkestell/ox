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
	elicit requestElicitation,
	fileSystem ClientFileSystem,
	terminal ClientTerminal,
	events chan<- event,
	turn diagnostictrace.Turn,
) (string, *delegationRecord, error) {
	reads, releaseReads := value.childFileReads()
	defer releaseReads()
	value.stateMu.Lock()
	configuration := value.state.turnConfiguration()
	child, resuming := value.state.children[parentCallID]
	child = cloneChildContext(child)
	value.stateMu.Unlock()
	record := &delegationRecord{Prompt: prompt}
	history := []openrouter.Message{{
		Role: openrouter.RoleUser,
		Content: []openrouter.ContentBlock{{
			Type: "text",
			Text: prompt,
		}},
	}}
	if resuming {
		if child.Prompt != prompt {
			return "", record, errors.New("durable child context has a different prompt")
		}
		record = delegationFromChild(child)
		history = cloneMessages(child.History)
		if child.Answer != "" {
			return child.Answer, record, nil
		}
	} else {
		record.History = cloneMessages(history)
		if err := a.persistChildContext(value, parentCallID, *record, nil); err != nil {
			return "", record, fmt.Errorf("persist child context: %w", err)
		}
	}
	providerCallIDs := make(map[string]struct{})
	lastText := record.Answer
	for _, message := range history {
		for _, call := range message.ToolCalls {
			providerCallIDs[call.ID] = struct{}{}
		}
		if message.Role == openrouter.RoleAssistant && len(message.Content) > 0 {
			lastText = message.Content[0].Text
		}
	}
	if record.RequestCount == maxTurnRequests {
		answer := childRequestLimitAnswer(lastText)
		record.Answer = answer
		if err := a.persistChildContext(value, parentCallID, *record, nil); err != nil {
			return "", record, fmt.Errorf("persist child context: %w", err)
		}
		return answer, record, nil
	}

	for requestCount := record.RequestCount + 1; requestCount <= maxTurnRequests; requestCount++ {
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
		request, err := a.admitChildRequest(
			ctx, value, parentCallID, record, request, turn, requestCount,
		)
		if err != nil {
			return "", record, err
		}
		history = cloneMessages(request.Messages[1:])
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
		if completion.Text != "" {
			lastText = completion.Text
		}
		if completion.FinishReason != "tool_calls" || len(completion.ToolCalls) == 0 {
			if completion.Usage != nil {
				record.Usage = append(record.Usage, *completion.Usage)
				record.Occupancy = completion.Usage.PromptTokens
			}
			assistant := openrouter.Message{
				Role:             openrouter.RoleAssistant,
				ReasoningDetails: cloneRawMessages(completion.ReasoningDetails),
			}
			if completion.Text != "" {
				assistant.Content = []openrouter.ContentBlock{{Type: "text", Text: completion.Text}}
			}
			history = append(history, assistant)
			record.History = cloneMessages(history)
			record.RequestCount = requestCount
			if strings.TrimSpace(completion.Text) == "" {
				if persistErr := a.persistChildContext(value, parentCallID, *record, nil); persistErr != nil {
					return "", record, fmt.Errorf("persist child context: %w", persistErr)
				}
				return "", record, errors.New("subagent returned an empty final answer")
			}
			record.Answer = completion.Text
			if err := a.persistChildContext(value, parentCallID, *record, nil); err != nil {
				return "", record, fmt.Errorf("persist child context: %w", err)
			}
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
		results, err := a.executeBatchWith(
			ctx,
			value,
			a.sessionSubagentTools(value),
			reads,
			publicCalls,
			ask,
			elicit,
			fileSystem,
			terminal,
			events,
			parentCallID,
			turn,
		)
		if err != nil {
			return "", record, err
		}
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
				Unknown:          result.unknown,
			})
			kind := eventToolCompleted
			if result.failed {
				kind = eventToolFailed
			}
			events <- a.toolEvent(
				a.sessionSubagentTools(value),
				publicCalls[index],
				kind,
				parentCallID,
				result.content,
				result.target,
			)
		}
		if completion.Usage != nil {
			record.Usage = append(record.Usage, *completion.Usage)
			record.Occupancy = completion.Usage.PromptTokens
		}
		record.History = cloneMessages(history)
		record.RequestCount = requestCount
		if requestCount == maxTurnRequests {
			answer := childRequestLimitAnswer(lastText)
			record.Answer = answer
		}
		if err := a.persistChildContext(value, parentCallID, *record, nil); err != nil {
			return "", record, fmt.Errorf("persist child context: %w", err)
		}
		if record.Answer != "" {
			return record.Answer, record, nil
		}
	}
	panic("unreachable")
}

func childRequestLimitAnswer(lastText string) string {
	answer := strings.TrimSpace(lastText)
	if answer != "" {
		answer += "\n\n"
	}
	return answer + "Subagent stopped after reaching the request limit."
}

func delegationFromChild(child childContext) *delegationRecord {
	return &delegationRecord{
		Prompt:       child.Prompt,
		Answer:       child.Answer,
		Calls:        append([]delegatedCall(nil), child.Calls...),
		Usage:        append([]openrouter.Usage(nil), child.Usage...),
		History:      cloneMessages(child.History),
		RequestCount: child.RequestCount,
		Occupancy:    child.Occupancy,
	}
}

func (a *Agent) persistChildContext(
	value *session,
	parentCallID string,
	record delegationRecord,
	compaction *compactionRecord,
) error {
	value.stateMu.Lock()
	defer value.stateMu.Unlock()
	child := childContext{
		ParentCallID: parentCallID,
		Prompt:       record.Prompt,
		History:      cloneMessages(record.History),
		Calls:        append([]delegatedCall(nil), record.Calls...),
		Usage:        append([]openrouter.Usage(nil), record.Usage...),
		RequestCount: record.RequestCount,
		Occupancy:    record.Occupancy,
		Answer:       record.Answer,
	}
	return a.commitLocked(value, recordChildContext, childContextRecord{
		TurnID: value.state.openTurn, Child: child, Compaction: compaction,
	})
}
