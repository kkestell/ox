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

const (
	compactionThresholdPercent = 80
	compactionRetainPercent    = 20
	summaryMessagePrefix       = "Summary of earlier conversation:\n\n"
	summarizerSystemPrompt     = "You are compacting the transcript of an ongoing tool-using agent session so it can continue with less context. Produce a faithful, detailed summary of the conversation below. Preserve every fact, decision, and result the agent will need to keep working: file paths and their relevant contents, commands run and their outputs, errors encountered, conclusions reached, and any task still in progress. Write prose, not a transcript; add no commentary, and invent nothing that is not present."
)

type compactionPlan struct {
	headEnd   int
	tailStart int
}

func (a *Agent) maybeCompact(
	ctx context.Context,
	value *session,
	turn diagnostictrace.Turn,
) (*event, error) {
	value.stateMu.Lock()
	configuration := cloneConfiguration(value.state.configuration)
	history := cloneMessages(value.state.history)
	occupancy := value.state.occupancy
	value.stateMu.Unlock()
	if !shouldCompact(occupancy, configuration.ContextWindow) {
		return nil, nil
	}
	plan := planCompaction(history, configuration.ContextWindow)
	if plan == nil {
		return nil, nil
	}

	transcript := renderCompactionTranscript(history[plan.headEnd:plan.tailStart])
	request := summarizerRequest(value.id, configuration, transcript)
	provider := turn.Provider(
		diagnostictrace.ProviderCompaction, 1, providerRequestBytes(request), "",
	)
	completion, err := a.client.Stream(ctx, request, nil)
	provider.Complete(
		providerOutcome(ctx, err, completion),
		providerStopReason(completion),
		providerUsage(completion),
		providerResponseBytes(completion),
	)
	if err != nil {
		return nil, fmt.Errorf("summarize model context: %w", err)
	}
	if completion == nil {
		return nil, errors.New("summarize model context: provider returned no completion")
	}
	summary := strings.TrimSpace(completion.Text)
	if summary == "" {
		return nil, errors.New("summarize model context: provider returned an empty summary")
	}
	if completion.FinishReason == "refusal" || completion.FinishReason == "content_filter" {
		return nil, errors.New("summarize model context: provider refused the summary")
	}

	summaryMessage := newSummaryMessage(summary)
	compacted := make([]openrouter.Message, 0, plan.headEnd+1+len(history)-plan.tailStart)
	compacted = append(compacted, history[:plan.headEnd]...)
	compacted = append(compacted, summaryMessage)
	compacted = append(compacted, history[plan.tailStart:]...)
	compactedOccupancy := estimateRequestTokens(
		configuration.SystemPrompt,
		configuration.Tools,
		compacted,
	)
	if err := a.commit(value, recordCompaction, compactionRecord{
		HeadEnd:   plan.headEnd,
		TailStart: plan.tailStart,
		Summary:   summaryMessage,
		Usage:     completion.Usage,
		Occupancy: compactedOccupancy,
	}); err != nil {
		return nil, fmt.Errorf("persist model context compaction: %w", err)
	}

	value.stateMu.Lock()
	totalCost := value.state.cost
	value.stateMu.Unlock()
	a.logger.Info(
		"model context compacted",
		"session_id", value.id,
		"messages_before", len(history),
		"messages_after", len(compacted),
		"context_occupancy", compactedOccupancy,
	)
	return &event{
		kind:             eventUsage,
		contextOccupancy: compactedOccupancy,
		contextWindow:    configuration.ContextWindow,
		totalCost:        totalCost,
	}, nil
}

func summarizerRequest(
	sessionID string,
	configuration requestConfiguration,
	transcript string,
) openrouter.Request {
	return openrouter.Request{
		Model: configuration.Settings.Model,
		Messages: []openrouter.Message{
			{
				Role: openrouter.RoleSystem,
				Content: []openrouter.ContentBlock{{
					Type: "text",
					Text: summarizerSystemPrompt,
				}},
			},
			{
				Role: openrouter.RoleUser,
				Content: []openrouter.ContentBlock{{
					Type: "text",
					Text: transcript,
				}},
			},
		},
		SessionID:   sessionID,
		MaxTokens:   configuration.Settings.MaxTokens,
		Temperature: configuration.Settings.Temperature,
		Reasoning:   configuration.Settings.Reasoning,
		Provider:    configuration.Settings.Provider,
	}
}

func shouldCompact(occupancy, contextWindow int) bool {
	return occupancy > 0 && contextWindow > 0 &&
		int64(occupancy)*100 >= int64(contextWindow)*compactionThresholdPercent
}

func planCompaction(messages []openrouter.Message, contextWindow int) *compactionPlan {
	if contextWindow <= 0 {
		return nil
	}
	firstUser := -1
	for index := range messages {
		if messages[index].Role == openrouter.RoleUser {
			firstUser = index
			break
		}
	}
	if firstUser < 0 {
		return nil
	}
	headEnd := firstUser + 1
	if headEnd >= len(messages) {
		return nil
	}

	tailBudget := contextWindow * compactionRetainPercent / 100
	tailStart := len(messages)
	tailTokens := 0
	for tailStart > headEnd {
		start := previousMessageGroup(messages, headEnd, tailStart)
		groupTokens := estimateMessages(messages[start:tailStart])
		if tailTokens > 0 && tailTokens+groupTokens > tailBudget {
			break
		}
		tailStart = start
		tailTokens += groupTokens
	}
	if tailStart <= headEnd {
		return nil
	}
	middleTokens := estimateMessages(messages[headEnd:tailStart])
	if middleTokens <= tailTokens {
		return nil
	}
	return &compactionPlan{headEnd: headEnd, tailStart: tailStart}
}

func previousMessageGroup(messages []openrouter.Message, headEnd, end int) int {
	start := end - 1
	for start > headEnd && messages[start].Role == openrouter.RoleTool {
		start--
	}
	if messages[start].Role == openrouter.RoleAssistant && len(messages[start].ToolCalls) > 0 {
		return start
	}
	return end - 1
}

func estimateRequestTokens(
	systemPrompt string,
	tools []openrouter.Tool,
	history []openrouter.Message,
) int {
	messages := make([]openrouter.Message, 1, len(history)+1)
	messages[0] = openrouter.Message{
		Role: openrouter.RoleSystem,
		Content: []openrouter.ContentBlock{{
			Type: "text",
			Text: systemPrompt,
		}},
	}
	messages = append(messages, history...)
	request := struct {
		Messages []openrouter.Message `json:"messages"`
		Tools    []openrouter.Tool    `json:"tools,omitempty"`
	}{Messages: messages, Tools: tools}
	data, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return bytesToTokens(len(data))
}

func estimateMessages(messages []openrouter.Message) int {
	data, err := json.Marshal(messages)
	if err != nil {
		panic(err)
	}
	return bytesToTokens(len(data))
}

func bytesToTokens(bytes int) int {
	return (bytes + 3) / 4
}

func renderCompactionTranscript(messages []openrouter.Message) string {
	var output strings.Builder
	for _, message := range messages {
		fmt.Fprintf(&output, "[%s]", message.Role)
		for _, content := range message.Content {
			if content.Type != "text" || content.Text == "" {
				continue
			}
			output.WriteByte(' ')
			output.WriteString(content.Text)
		}
		for _, call := range message.ToolCalls {
			fmt.Fprintf(
				&output,
				"\n  -> calls %s(%s)",
				call.Function.Name,
				call.Function.Arguments,
			)
		}
		output.WriteString("\n\n")
	}
	return output.String()
}

func newSummaryMessage(summary string) openrouter.Message {
	return openrouter.Message{
		Role: openrouter.RoleUser,
		Content: []openrouter.ContentBlock{{
			Type: "text",
			Text: summaryMessagePrefix + strings.TrimSpace(summary),
		}},
	}
}
