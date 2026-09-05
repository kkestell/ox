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

// requestAdmission is the pure result of sizing the next provider request. A
// non-nil plan means the caller must summarize the plan's middle and pass that
// summary to compactRequest before sending the request.
type requestAdmission struct {
	request   openrouter.Request
	occupancy int
	plan      *compactionPlan
}

func (a *Agent) admitPrimaryRequest(
	ctx context.Context,
	value *session,
	turn diagnostictrace.Turn,
	requestCount int,
) (openrouter.Request, *event, error) {
	request := a.modelRequest(value)
	value.stateMu.Lock()
	configuration := cloneConfiguration(value.state.configuration)
	value.stateMu.Unlock()
	planned, err := planRequestAdmission(request, configuration.ContextWindow)
	if err != nil {
		return openrouter.Request{}, nil, err
	}
	if planned.plan == nil {
		return planned.request, nil, nil
	}
	completion, err := a.summarizeContext(
		ctx,
		value.id,
		configuration,
		request.Messages[planned.plan.headEnd:planned.plan.tailStart],
		turn,
		requestCount,
		"",
	)
	if err != nil {
		return openrouter.Request{}, nil, err
	}
	admitted, err := compactRequest(
		request, *planned.plan, completion.Text, configuration.ContextWindow,
	)
	if err != nil {
		return openrouter.Request{}, nil, err
	}
	value.stateMu.Lock()
	turnID := value.state.openTurn
	value.stateMu.Unlock()
	record := compactionRecord{
		TurnID:  turnID,
		HeadEnd: planned.plan.headEnd - 1, TailStart: planned.plan.tailStart - 1,
		Summary: admitted.request.Messages[planned.plan.headEnd],
		Usage:   completion.Usage, Occupancy: admitted.occupancy,
	}
	if err := a.commit(value, recordCompaction, record); err != nil {
		return openrouter.Request{}, nil, fmt.Errorf("persist model context compaction: %w", err)
	}
	return admitted.request, a.compactionUsageEvent(value, admitted.occupancy), nil
}

func (a *Agent) admitChildRequest(
	ctx context.Context,
	value *session,
	parentCallID string,
	record *delegationRecord,
	request openrouter.Request,
	turn diagnostictrace.Turn,
	requestCount int,
) (openrouter.Request, error) {
	value.stateMu.Lock()
	configuration := cloneConfiguration(value.state.configuration)
	value.stateMu.Unlock()
	planned, err := planRequestAdmission(request, configuration.ContextWindow)
	if err != nil {
		return openrouter.Request{}, err
	}
	if planned.plan == nil {
		return planned.request, nil
	}
	completion, err := a.summarizeContext(
		ctx,
		value.id,
		configuration,
		request.Messages[planned.plan.headEnd:planned.plan.tailStart],
		turn,
		requestCount,
		parentCallID,
	)
	if err != nil {
		return openrouter.Request{}, err
	}
	admitted, err := compactRequest(
		request, *planned.plan, completion.Text, configuration.ContextWindow,
	)
	if err != nil {
		return openrouter.Request{}, err
	}
	record.History = cloneMessages(admitted.request.Messages[1:])
	record.Occupancy = admitted.occupancy
	if completion.Usage != nil {
		record.Usage = append(record.Usage, *completion.Usage)
	}
	value.stateMu.Lock()
	turnID := value.state.openTurn
	value.stateMu.Unlock()
	compaction := &compactionRecord{
		TurnID: turnID, ParentCallID: parentCallID,
		HeadEnd: planned.plan.headEnd - 1, TailStart: planned.plan.tailStart - 1,
		Summary: admitted.request.Messages[planned.plan.headEnd],
		Usage:   completion.Usage, Occupancy: admitted.occupancy,
	}
	if err := a.persistChildContext(value, parentCallID, *record, compaction); err != nil {
		return openrouter.Request{}, fmt.Errorf("persist child context compaction: %w", err)
	}
	return admitted.request, nil
}

func (a *Agent) summarizeContext(
	ctx context.Context,
	sessionID string,
	configuration requestConfiguration,
	messages []openrouter.Message,
	turn diagnostictrace.Turn,
	requestCount int,
	parentCallID string,
) (*openrouter.Completion, error) {
	transcript := renderCompactionTranscript(messages)
	request := summarizerRequest(sessionID, configuration, transcript)
	_, budget, err := estimateProviderRequest(request)
	if err != nil {
		return nil, err
	}
	if configuration.ContextWindow > 0 && budget > configuration.ContextWindow {
		return nil, fmt.Errorf(
			"model context summary request needs %d tokens but the model context window is %d; the conversation cannot be compacted safely",
			budget, configuration.ContextWindow,
		)
	}
	provider := turn.Provider(
		diagnostictrace.ProviderCompaction,
		requestCount,
		providerRequestBytes(request),
		parentCallID,
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
	if completion.FinishReason == "refusal" || completion.FinishReason == "content_filter" {
		return nil, errors.New("summarize model context: provider refused the summary")
	}
	if strings.TrimSpace(completion.Text) == "" {
		return nil, errors.New("summarize model context: provider returned an empty summary")
	}
	return completion, nil
}

func (a *Agent) compactionUsageEvent(value *session, occupancy int) *event {
	value.stateMu.Lock()
	configuration := cloneConfiguration(value.state.configuration)
	totalCost := value.state.cost
	value.stateMu.Unlock()
	return &event{
		kind: eventUsage, contextOccupancy: occupancy,
		contextWindow: configuration.ContextWindow, totalCost: totalCost,
	}
}

// planRequestAdmission sizes the request about to be sent, including the
// requested output reserve. It returns at most one compaction plan and never
// changes request.
func planRequestAdmission(
	request openrouter.Request,
	contextWindow int,
) (requestAdmission, error) {
	occupancy, budget, err := estimateProviderRequest(request)
	if err != nil {
		return requestAdmission{}, err
	}
	result := requestAdmission{request: request, occupancy: occupancy}
	if contextWindow <= 0 || !shouldCompact(budget, contextWindow) {
		return result, nil
	}

	headEnd := protectedMessageEnd(request.Messages)
	if headEnd < 0 {
		if budget > contextWindow {
			return requestAdmission{}, fmt.Errorf(
				"provider request needs %d tokens but the model context window is %d; the request has no user message that can be compacted",
				budget,
				contextWindow,
			)
		}
		return result, nil
	}
	protected := requestWithMessages(request, request.Messages[:headEnd])
	_, protectedBudget, err := estimateProviderRequest(protected)
	if err != nil {
		return requestAdmission{}, err
	}
	if protectedBudget > contextWindow {
		return requestAdmission{}, fmt.Errorf(
			"provider request protected prefix needs %d tokens but the model context window is %d; shorten the instructions, tool schemas, first user request, or requested output",
			protectedBudget,
			contextWindow,
		)
	}

	plan := planCompaction(request.Messages, contextWindow)
	latestStart := previousMessageGroup(request.Messages, headEnd, len(request.Messages))
	if plan == nil && budget > contextWindow && latestStart > headEnd {
		plan = &compactionPlan{headEnd: headEnd, tailStart: latestStart}
	}
	if plan == nil {
		if budget > contextWindow {
			return requestAdmission{}, fmt.Errorf(
				"provider request needs %d tokens but the model context window is %d; the newest complete message group cannot be compacted",
				budget,
				contextWindow,
			)
		}
		return result, nil
	}

	// A large protected prefix can leave less than the normal retention budget.
	// In that case retain only the newest complete group rather than rejecting a
	// request that one useful summary could still admit.
	if !compactionShapeFits(request, *plan, contextWindow) && latestStart > headEnd {
		plan = &compactionPlan{headEnd: headEnd, tailStart: latestStart}
	}
	if !compactionShapeFits(request, *plan, contextWindow) {
		if budget <= contextWindow {
			return result, nil
		}
		return requestAdmission{}, fmt.Errorf(
			"provider request cannot fit the model context window of %d tokens while preserving the newest complete message group; shorten the recent input or requested output",
			contextWindow,
		)
	}
	result.plan = plan
	return result, nil
}

// compactRequest applies a completed summary to a planned request and verifies
// that the result fits. It constructs a fresh message slice, so an empty or
// oversized summary leaves request unchanged.
func compactRequest(
	request openrouter.Request,
	plan compactionPlan,
	summary string,
	contextWindow int,
) (requestAdmission, error) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return requestAdmission{}, errors.New("summarize model context: provider returned an empty summary")
	}

	messages, err := compactedMessages(request.Messages, plan, summary)
	if err != nil {
		return requestAdmission{}, err
	}
	compacted := requestWithMessages(request, messages)
	occupancy, budget, err := estimateProviderRequest(compacted)
	if err != nil {
		return requestAdmission{}, err
	}
	if contextWindow > 0 && budget > contextWindow {
		return requestAdmission{}, fmt.Errorf(
			"compacted provider request needs %d tokens but the model context window is %d; the generated summary is too large",
			budget,
			contextWindow,
		)
	}
	return requestAdmission{request: compacted, occupancy: occupancy}, nil
}

func compactionShapeFits(
	request openrouter.Request,
	plan compactionPlan,
	contextWindow int,
) bool {
	messages, err := compactedMessages(request.Messages, plan, "x")
	if err != nil {
		return false
	}
	_, budget, err := estimateProviderRequest(requestWithMessages(request, messages))
	return err == nil && budget <= contextWindow
}

func compactedMessages(
	messages []openrouter.Message,
	plan compactionPlan,
	summary string,
) ([]openrouter.Message, error) {
	if err := validateCompactionPlan(messages, plan); err != nil {
		return nil, err
	}
	messageCount, ok := checkedAdd(plan.headEnd, 1)
	if ok {
		messageCount, ok = checkedAdd(messageCount, len(messages)-plan.tailStart)
	}
	if !ok {
		return nil, errors.New("size compacted provider request: integer overflow")
	}
	compacted := make([]openrouter.Message, 0, messageCount)
	compacted = append(compacted, cloneMessages(messages[:plan.headEnd])...)
	compacted = append(compacted, newSummaryMessage(summary))
	compacted = append(compacted, cloneMessages(messages[plan.tailStart:])...)
	return compacted, nil
}

func protectedMessageEnd(messages []openrouter.Message) int {
	for index := range messages {
		if messages[index].Role == openrouter.RoleUser {
			return index + 1
		}
	}
	return -1
}

func validateCompactionPlan(messages []openrouter.Message, plan compactionPlan) error {
	if plan.headEnd != protectedMessageEnd(messages) || plan.tailStart <= plan.headEnd ||
		plan.tailStart >= len(messages) {
		return errors.New("model context compaction splice boundaries are invalid")
	}
	if !messageGroupBoundary(messages, plan.headEnd) ||
		!messageGroupBoundary(messages, plan.tailStart) {
		return errors.New("model context compaction would separate a tool call from its result")
	}
	return nil
}

func requestWithMessages(
	request openrouter.Request,
	messages []openrouter.Message,
) openrouter.Request {
	request.Messages = messages
	return request
}

// estimateProviderRequest returns the conservative prompt occupancy followed
// by the total budget after reserving max_tokens.
func estimateProviderRequest(request openrouter.Request) (int, int, error) {
	for _, message := range request.Messages {
		for _, block := range message.Content {
			switch block.Type {
			case "image_url":
				return 0, 0, errors.New("cannot size provider request containing image content; remove the image or choose a model with a larger known context window")
			case "input_audio":
				return 0, 0, errors.New("cannot size provider request containing audio content; remove the audio or choose a model with a larger known context window")
			}
		}
	}
	prompt := struct {
		Messages []openrouter.Message `json:"messages"`
		Tools    []openrouter.Tool    `json:"tools,omitempty"`
	}{Messages: request.Messages, Tools: request.Tools}
	data, err := json.Marshal(prompt)
	if err != nil {
		return 0, 0, fmt.Errorf("size provider request: %w", err)
	}
	occupancy := bytesToTokens(len(data))
	reserve := 0
	if request.MaxTokens != nil {
		reserve = *request.MaxTokens
		if reserve < 0 {
			return 0, 0, errors.New("size provider request: max_tokens cannot be negative")
		}
	}
	budget, ok := checkedAdd(occupancy, reserve)
	if !ok {
		return 0, 0, errors.New("size provider request: token budget integer overflow")
	}
	return occupancy, budget, nil
}

func checkedAdd(left, right int) (int, bool) {
	if left < 0 || right < 0 || left > int(^uint(0)>>1)-right {
		return 0, false
	}
	return left + right, true
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
	if occupancy <= 0 || contextWindow <= 0 {
		return false
	}
	threshold := contextWindow / 100 * compactionThresholdPercent
	remainder := contextWindow % 100 * compactionThresholdPercent
	if remainder > 0 {
		threshold += (remainder + 99) / 100
	}
	return occupancy >= threshold
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

	tailBudget := contextWindow/100*compactionRetainPercent +
		contextWindow%100*compactionRetainPercent/100
	tailStart := len(messages)
	tailTokens := 0
	for tailStart > headEnd {
		start := previousMessageGroup(messages, headEnd, tailStart)
		groupTokens := estimateMessages(messages[start:tailStart])
		if tailTokens > 0 &&
			(groupTokens > tailBudget || tailTokens > tailBudget-groupTokens) {
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
	if bytes < 0 {
		panic("token estimate byte count cannot be negative")
	}
	tokens := bytes / 4
	if bytes%4 != 0 {
		tokens++
	}
	return tokens
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
