package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/skills"
	diagnostictrace "github.com/kkestell/ox/internal/trace"
	"github.com/kkestell/ox/internal/workspace"
)

const maxTurnRequests = 16

type loopOutcome struct {
	response acp.PromptResponse
	err      error
}

type turnUsage struct {
	seen        bool
	input       uint64
	output      uint64
	thought     uint64
	cachedRead  uint64
	cachedWrite uint64
}

type requestPermission func(
	context.Context,
	acp.RequestPermissionRequest,
) (acp.RequestPermissionResponse, error)

func (a *Agent) run(
	ctx context.Context,
	value *session,
	active *activeTurn,
	ask requestPermission,
	fileSystem ClientFileSystem,
	terminal ClientTerminal,
	events chan<- event,
) loopOutcome {
	return a.runFrom(
		ctx, value, active, ask, fileSystem, terminal, events, 1, nil, false,
	)
}

func (a *Agent) resume(
	ctx context.Context,
	value *session,
	active *activeTurn,
	ask requestPermission,
	fileSystem ClientFileSystem,
	terminal ClientTerminal,
	events chan<- event,
) loopOutcome {
	value.stateMu.Lock()
	suspended := cloneSuspendedExchange(value.state.suspended)
	value.stateMu.Unlock()
	if suspended == nil {
		panic("resume called without a suspended model exchange")
	}
	return a.runFrom(
		ctx, value, active, ask, fileSystem, terminal, events,
		suspended.RequestCount, suspended, true,
	)
}

func (a *Agent) runFrom(
	ctx context.Context,
	value *session,
	active *activeTurn,
	ask requestPermission,
	fileSystem ClientFileSystem,
	terminal ClientTerminal,
	events chan<- event,
	startRequest int,
	suspended *suspendedModelExchangeRecord,
	reissue bool,
) loopOutcome {
	for requestCount := startRequest; requestCount <= maxTurnRequests; requestCount++ {
		if ctx.Err() != nil {
			return a.finishCancelled(value, active, events)
		}
		var answerID, thoughtID string
		var completion *openrouter.Completion
		if suspended != nil {
			answerID = suspended.AnswerID
			thoughtID = suspended.ThoughtID
			completion = suspended.completion()
		} else {
			a.logger.Info(
				"starting model request",
				"session_id", value.id,
				"request", requestCount,
				"prefix_fingerprint", a.prefixFingerprint(value),
			)
			var err error
			answerID, err = randomID()
			if err != nil {
				return a.finishFailed(value, active, events, err)
			}
			thoughtID, err = randomID()
			if err != nil {
				return a.finishFailed(value, active, events, err)
			}
			events <- event{
				kind: eventResponseStart, messageID: answerID, thoughtID: thoughtID,
			}
			request, compactionUpdate, err := a.admitPrimaryRequest(
				ctx, value, active.trace, requestCount,
			)
			if err != nil {
				if active.cancelledByClient.Load() || errors.Is(err, context.Canceled) {
					return a.finishCancelled(value, active, events)
				}
				if requestCount == 1 {
					if finishErr := a.finishTurn(
						value, active, "refusal", "", err.Error(), events,
					); finishErr != nil {
						return loopOutcome{err: finishErr}
					}
					return loopOutcome{err: err}
				}
				return a.finishFailed(value, active, events, err)
			}
			if compactionUpdate != nil {
				events <- *compactionUpdate
			}
			provider := active.trace.Provider(
				diagnostictrace.ProviderPrimary,
				requestCount,
				providerRequestBytes(request),
				"",
			)
			completion, err = a.client.Stream(ctx, request, func(delta openrouter.Delta) {
				switch delta.Kind {
				case openrouter.DeltaText:
					events <- event{kind: eventText, text: delta.Text}
				case openrouter.DeltaReasoning:
					events <- event{kind: eventReasoning, text: delta.Text}
				}
			})
			provider.Complete(
				providerOutcome(ctx, err, completion),
				providerStopReason(completion),
				providerUsage(completion),
				providerResponseBytes(completion),
			)
			if err != nil {
				if (active.cancelledByClient.Load() || errors.Is(err, context.Canceled)) &&
					completion != nil && len(completion.ToolCalls) == 0 &&
					(completion.Text != "" || completion.Reasoning != "" || completion.Usage != nil) {
					if commitErr := a.commit(value, recordModelExchange, modelExchangeRecord{
						TurnID: active.turnID, AnswerID: answerID, ThoughtID: thoughtID,
						Text: completion.Text, Reasoning: completion.Reasoning,
						ReasoningDetails: opaqueMessages(completion.ReasoningDetails),
						Usage:            completion.Usage,
					}); commitErr != nil {
						return loopOutcome{err: fmt.Errorf("persist partial model exchange: %w", commitErr)}
					}
					a.publishUsage(value, completion.Usage, events)
				}
				if active.cancelledByClient.Load() {
					a.logger.Info("model request cancelled", "session_id", value.id)
					return a.finishCancelled(value, active, events)
				}
				return a.finishFailed(
					value, active, events, fmt.Errorf("stream model response: %w", err),
				)
			}
			if active.cancelledByClient.Load() {
				return a.finishCancelled(value, active, events)
			}
		}

		if completion.FinishReason != "tool_calls" || len(completion.ToolCalls) == 0 {
			if err := a.commit(value, recordModelExchange, modelExchangeRecord{
				TurnID:           active.turnID,
				AnswerID:         answerID,
				ThoughtID:        thoughtID,
				Text:             completion.Text,
				Reasoning:        completion.Reasoning,
				ReasoningDetails: opaqueMessages(completion.ReasoningDetails),
				FinishReason:     completion.FinishReason,
				Usage:            completion.Usage,
			}); err != nil {
				return loopOutcome{err: fmt.Errorf("persist model exchange: %w", err)}
			}
			a.publishUsage(value, completion.Usage, events)
			stopReason := stopReason(completion.FinishReason)
			kind := "completed"
			if stopReason == acp.StopReasonRefusal {
				kind = "refusal"
			}
			if err := a.finishTurn(
				value,
				active,
				kind,
				stopReason,
				"",
				events,
			); err != nil {
				return loopOutcome{err: err}
			}
			return loopOutcome{response: acp.PromptResponse{
				StopReason: stopReason,
				Usage:      value.state.usage.acp(),
			}}
		}

		if suspended == nil {
			if err := validateToolCallIDs(value, completion.ToolCalls); err != nil {
				return loopOutcome{err: fmt.Errorf("validate tool calls: %w", err)}
			}
			suspended = &suspendedModelExchangeRecord{
				TurnID: active.turnID, AnswerID: answerID, ThoughtID: thoughtID,
				Text: completion.Text, Reasoning: completion.Reasoning,
				ReasoningDetails: opaqueMessages(completion.ReasoningDetails),
				FinishReason:     completion.FinishReason, Usage: completion.Usage,
				ToolCalls:    append([]openrouter.ToolCall(nil), completion.ToolCalls...),
				ToolTargets:  normalizedToolTargets(value.state.cwd, a.primaryTools, completion.ToolCalls),
				RequestCount: requestCount,
			}
			if err := a.commit(value, recordExchangePaused, *suspended); err != nil {
				return loopOutcome{err: fmt.Errorf("persist suspended model exchange: %w", err)}
			}
			a.publishPendingTools(
				completion.ToolCalls, suspended.ToolTargets, events, active.trace,
			)
		}
		results, batchCancelled, err := a.executeSuspendedBatch(
			ctx, value, completion.ToolCalls, ask, fileSystem, terminal, events,
			active.trace, reissue,
		)
		if err != nil {
			return loopOutcome{err: err}
		}
		storedResults := make([]storedToolResult, len(results))
		for index, result := range results {
			storedResults[index] = storedResult(completion.ToolCalls[index].ID, result)
		}
		if err := a.commit(value, recordModelExchange, modelExchangeRecord{
			TurnID:           active.turnID,
			AnswerID:         answerID,
			ThoughtID:        thoughtID,
			Text:             completion.Text,
			Reasoning:        completion.Reasoning,
			ReasoningDetails: opaqueMessages(completion.ReasoningDetails),
			FinishReason:     completion.FinishReason,
			Usage:            completion.Usage,
			ToolCalls:        append([]openrouter.ToolCall(nil), completion.ToolCalls...),
			ToolResults:      storedResults,
		}); err != nil {
			return loopOutcome{err: fmt.Errorf("persist tool exchange: %w", err)}
		}
		for index, call := range completion.ToolCalls {
			if results[index].unknown && results[index].delegation != nil {
				for _, child := range results[index].delegation.Calls {
					childCall := openrouter.ToolCall{
						ID: child.CallID, Type: "function",
						Function: openrouter.ToolCallFunction{
							Name: child.Name, Arguments: string(child.Arguments),
						},
					}
					events <- a.toolEvent(
						a.subagentTools, childCall, eventToolPending, call.ID, "", child.Target,
					)
					kind := eventToolCompleted
					if child.Failed {
						kind = eventToolFailed
					}
					events <- a.toolEvent(
						a.subagentTools, childCall, kind, call.ID, child.Content, child.Target,
					)
				}
			}
			active.trace.ToolCompleted(
				call.ID,
				call.Function.Name,
				"",
				toolOutcome(results[index]),
				len(results[index].content),
			)
			kind := eventToolCompleted
			if results[index].failed {
				kind = eventToolFailed
			}
			events <- a.toolEvent(
				a.primaryTools,
				call,
				kind,
				"",
				results[index].content,
				results[index].target,
			)
		}
		usages := []*openrouter.Usage{completion.Usage}
		for _, result := range results {
			if result.delegation != nil {
				for index := range result.delegation.Usage {
					usages = append(usages, &result.delegation.Usage[index])
				}
			}
		}
		a.publishUsage(value, combinedUsage(usages...), events)
		suspended = nil
		reissue = false
		if batchCancelled || active.cancelledByClient.Load() {
			return a.finishCancelled(value, active, events)
		}
		if ctx.Err() != nil {
			return a.finishFailed(value, active, events, ctx.Err())
		}
		if requestCount == maxTurnRequests {
			if err := a.finishTurn(
				value,
				active,
				"request_limit",
				acp.StopReasonMaxTurnRequests,
				"",
				events,
			); err != nil {
				return loopOutcome{err: err}
			}
			return loopOutcome{response: acp.PromptResponse{
				StopReason: acp.StopReasonMaxTurnRequests,
				Usage:      value.state.usage.acp(),
			}}
		}
	}
	panic("unreachable")
}

func (s *suspendedModelExchangeRecord) completion() *openrouter.Completion {
	return &openrouter.Completion{
		Text: s.Text, Reasoning: s.Reasoning,
		ReasoningDetails: rawMessages(s.ReasoningDetails),
		FinishReason:     s.FinishReason, Usage: s.Usage,
		ToolCalls: append([]openrouter.ToolCall(nil), s.ToolCalls...),
	}
}

func (a *Agent) publishPendingTools(
	calls []openrouter.ToolCall,
	targets map[string]string,
	events chan<- event,
	turn diagnostictrace.Turn,
) {
	for _, call := range calls {
		turn.ToolPending(call.ID, call.Function.Name, "")
		var kind acp.ToolKind
		var delegates bool
		if index, ok := a.primaryTools.byName[call.Function.Name]; ok {
			kind = a.primaryTools.tools[index].Kind
			delegates = a.primaryTools.tools[index].Delegates
		}
		events <- event{
			kind: eventToolPending, call: call, toolKind: kind,
			title:     a.toolTitle(call.Function.Name, json.RawMessage(call.Function.Arguments)),
			target:    targets[call.ID],
			delegates: delegates,
		}
	}
}

func validateToolCallIDs(value *session, calls []openrouter.ToolCall) error {
	value.callIDsMu.Lock()
	defer value.callIDsMu.Unlock()
	value.stateMu.Lock()
	defer value.stateMu.Unlock()
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		if call.ID == "" {
			return errors.New("tool call ID is required")
		}
		if _, exists := value.state.toolCallIDs[call.ID]; exists {
			return fmt.Errorf("duplicate tool call ID %q", call.ID)
		}
		if _, exists := value.callIDs[call.ID]; exists {
			return fmt.Errorf("duplicate tool call ID %q", call.ID)
		}
		if _, exists := seen[call.ID]; exists {
			return fmt.Errorf("duplicate tool call ID %q", call.ID)
		}
		seen[call.ID] = struct{}{}
	}
	if value.callIDs == nil {
		value.callIDs = make(map[string]struct{})
	}
	for id := range seen {
		value.callIDs[id] = struct{}{}
	}
	return nil
}

func allocateToolCallID(value *session) (string, error) {
	for {
		id, err := randomID()
		if err != nil {
			return "", err
		}
		value.callIDsMu.Lock()
		value.stateMu.Lock()
		_, durable := value.state.toolCallIDs[id]
		value.stateMu.Unlock()
		if _, live := value.callIDs[id]; !durable && !live {
			if value.callIDs == nil {
				value.callIDs = make(map[string]struct{})
			}
			value.callIDs[id] = struct{}{}
			value.callIDsMu.Unlock()
			return id, nil
		}
		value.callIDsMu.Unlock()
	}
}

func (a *Agent) finishCancelled(
	value *session,
	active *activeTurn,
	events chan<- event,
) loopOutcome {
	if err := a.finishTurn(
		value,
		active,
		"cancelled",
		acp.StopReasonCancelled,
		"",
		events,
	); err != nil {
		return loopOutcome{err: err}
	}
	if active.cancelledByClient.Load() {
		return loopOutcome{response: acp.PromptResponse{
			StopReason: acp.StopReasonCancelled,
			Usage:      value.state.usage.acp(),
		}}
	}
	return loopOutcome{err: context.Canceled}
}

func (a *Agent) finishFailed(
	value *session,
	active *activeTurn,
	events chan<- event,
	cause error,
) loopOutcome {
	kind := "failed"
	message := cause.Error()
	if errors.Is(cause, context.Canceled) {
		kind = "interrupted"
		message = ""
	}
	if err := a.finishTurn(value, active, kind, "", message, events); err != nil {
		return loopOutcome{err: err}
	}
	return loopOutcome{err: cause}
}

func (a *Agent) finishTurn(
	value *session,
	active *activeTurn,
	kind string,
	stopReason acp.StopReason,
	message string,
	events chan<- event,
) error {
	messageID, err := randomID()
	if err != nil {
		return err
	}
	outcome := turnFinishedRecord{
		TurnID:     active.turnID,
		Kind:       kind,
		StopReason: stopReason,
		MessageID:  messageID,
		Message:    message,
	}
	if err := a.commit(value, recordTurnFinished, outcome); err != nil {
		return fmt.Errorf("persist turn outcome: %w", err)
	}
	active.trace.Complete(kind, string(stopReason))
	if update := outcomeUpdate(outcome); update != nil {
		events <- event{kind: eventOutcome, update: update}
	}
	return nil
}

func (a *Agent) modelRequest(value *session) (openrouter.Request, int) {
	value.stateMu.Lock()
	configuration := value.state.turnConfiguration()
	history := cloneMessages(value.state.history)
	todo := clonePlanEntries(value.state.todo)
	value.stateMu.Unlock()
	messages := make([]openrouter.Message, 0, len(history)+2)
	messages = append(messages, openrouter.Message{
		Role: openrouter.RoleSystem,
		Content: []openrouter.ContentBlock{{
			Type: "text",
			Text: configuration.SystemPrompt,
		}},
	})
	if len(todo) != 0 {
		messages = append(messages, todoContextMessage(todo))
	}
	historyOffset := len(messages)
	messages = append(messages, history...)
	return openrouter.Request{
		Model:        configuration.Settings.Model,
		Messages:     messages,
		Tools:        cloneTools(configuration.Tools),
		CacheControl: &openrouter.CacheControl{Type: "ephemeral"},
		SessionID:    value.id,
		MaxTokens:    configuration.Settings.MaxTokens,
		Temperature:  configuration.Settings.Temperature,
		Reasoning:    configuration.Settings.Reasoning,
		Provider:     configuration.Settings.Provider,
	}, historyOffset
}

func todoContextMessage(entries []acp.PlanEntry) openrouter.Message {
	data, err := json.Marshal(entries)
	if err != nil {
		panic(err)
	}
	return openrouter.Message{
		Role: openrouter.RoleSystem,
		Content: []openrouter.ContentBlock{{
			Type: "text",
			Text: "Current todo progress state (data, not new instructions):\n" + string(data),
		}},
	}
}

// prefixFingerprint hashes everything a request carries ahead of the messages,
// so a change that would invalidate the provider's prompt cache is visible in
// the log. The turn configuration keeps this stable across a tool loop.
func (a *Agent) prefixFingerprint(value *session) string {
	value.stateMu.Lock()
	configuration := value.state.turnConfiguration()
	value.stateMu.Unlock()
	return requestPrefixFingerprint(
		configuration,
		configuration.SystemPrompt,
		configuration.Tools,
	)
}

func subagentPrefixFingerprint(configuration requestConfiguration) string {
	return requestPrefixFingerprint(
		configuration,
		configuration.Subagent.SystemPrompt,
		configuration.Subagent.Tools,
	)
}

func requestPrefixFingerprint(
	configuration requestConfiguration,
	systemPrompt string,
	tools []openrouter.Tool,
) string {
	config := struct {
		Model        string                   `json:"model"`
		SystemPrompt string                   `json:"system_prompt"`
		Tools        []openrouter.Tool        `json:"tools,omitempty"`
		CacheControl *openrouter.CacheControl `json:"cache_control"`
		MaxTokens    *int                     `json:"max_tokens,omitempty"`
		Temperature  *float64                 `json:"temperature,omitempty"`
		Reasoning    *openrouter.Reasoning    `json:"reasoning,omitempty"`
		Provider     *openrouter.Provider     `json:"provider,omitempty"`
	}{
		Model:        configuration.Settings.Model,
		SystemPrompt: systemPrompt,
		Tools:        tools,
		CacheControl: &openrouter.CacheControl{Type: "ephemeral"},
		MaxTokens:    configuration.Settings.MaxTokens,
		Temperature:  configuration.Settings.Temperature,
		Reasoning:    configuration.Settings.Reasoning,
		Provider:     configuration.Settings.Provider,
	}
	data, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func promptMessage(blocks []acp.ContentBlock) (openrouter.Message, error) {
	content := make([]openrouter.ContentBlock, len(blocks))
	for index, block := range blocks {
		switch block.Type {
		case "text":
			content[index] = openrouter.ContentBlock{Type: "text", Text: block.Text}
		case "image":
			content[index] = openrouter.ContentBlock{
				Type:     "image_url",
				ImageURL: dataURL(block.MIMEType, block.Data),
			}
		case "audio":
			content[index] = openrouter.ContentBlock{
				Type:        "input_audio",
				AudioData:   block.Data,
				AudioFormat: audioFormat(block.MIMEType),
			}
		case "resource_link":
			if block.Name == "" || block.URI == "" {
				return openrouter.Message{}, errors.New("resource link requires name and uri")
			}
			content[index] = openrouter.ContentBlock{
				Type: "text",
				Text: "[" + markdownText(block.Name) + "](" + markdownTarget(block.URI) + ")",
			}
		case "resource":
			if block.Resource == nil {
				return openrouter.Message{}, fmt.Errorf("prompt content block %d resource is required", index+1)
			}
			resource := block.Resource
			if resource.Text != nil {
				marker := fence(*resource.Text)
				content[index] = openrouter.ContentBlock{
					Type: "text",
					Text: "[" + resourceLabel(resource.URI) + "]\n" +
						marker + "\n" + *resource.Text + "\n" + marker,
				}
				continue
			}
			if resource.Blob == nil {
				return openrouter.Message{}, fmt.Errorf(
					"prompt content block %d resource requires text or blob", index+1,
				)
			}
			switch media := mediaType(resource.MIMEType); {
			case strings.HasPrefix(media, "image/"):
				content[index] = openrouter.ContentBlock{
					Type:     "image_url",
					ImageURL: dataURL(resource.MIMEType, *resource.Blob),
				}
			case strings.HasPrefix(media, "audio/"):
				content[index] = openrouter.ContentBlock{
					Type:        "input_audio",
					AudioData:   *resource.Blob,
					AudioFormat: audioFormat(resource.MIMEType),
				}
			case media == "":
				return openrouter.Message{}, fmt.Errorf(
					"prompt content block %d resource blob has no MIME type", index+1,
				)
			default:
				return openrouter.Message{}, fmt.Errorf(
					"prompt content block %d resource blob with MIME type %q cannot be sent to the model",
					index+1, resource.MIMEType,
				)
			}
		default:
			return openrouter.Message{}, fmt.Errorf(
				"prompt content block %d has unsupported type %q", index+1, block.Type,
			)
		}
	}
	return openrouter.Message{Role: openrouter.RoleUser, Content: content}, nil
}

func mediaType(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

func dataURL(mimeType, data string) string {
	return "data:" + mediaType(mimeType) + ";base64," + data
}

func audioFormat(mimeType string) string {
	_, subtype, _ := strings.Cut(mediaType(mimeType), "/")
	subtype = strings.TrimPrefix(subtype, "x-")
	switch subtype {
	case "mpeg":
		return "mp3"
	case "wave":
		return "wav"
	default:
		return subtype
	}
}

func resourceLabel(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" || parsed.Path == "" {
		return uri
	}
	label := parsed.Path
	lineRange := strings.Split(strings.TrimPrefix(parsed.Fragment, "L"), "-L")
	if strings.HasPrefix(parsed.Fragment, "L") {
		start, startErr := strconv.Atoi(lineRange[0])
		if startErr == nil && start > 0 {
			label += ":" + strconv.Itoa(start)
			if len(lineRange) == 2 {
				end, endErr := strconv.Atoi(lineRange[1])
				if endErr == nil && end > 0 {
					label += "-" + strconv.Itoa(end)
				}
			}
		}
	}
	return label
}

func fence(text string) string {
	longest, run := 0, 0
	for _, character := range text {
		if character == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func markdownText(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)
	return replacer.Replace(value)
}

func markdownTarget(value string) string {
	return strings.ReplaceAll(value, ")", `\)`)
}

type toolGroup struct {
	start int
	end   int
}

type toolResult struct {
	content    string
	failed     bool
	approval   approvalDecision
	delegation *delegationRecord
	target     string
	unknown    bool
}

func (a *Agent) executeSuspendedBatch(
	ctx context.Context,
	value *session,
	calls []openrouter.ToolCall,
	ask requestPermission,
	fileSystem ClientFileSystem,
	terminal ClientTerminal,
	events chan<- event,
	turn diagnostictrace.Turn,
	reissue bool,
) ([]toolResult, bool, error) {
	a.logger.Info("suspended tool batch started", "session_id", value.id, "calls", len(calls))
	value.stateMu.Lock()
	configuration := value.state.turnConfiguration()
	value.stateMu.Unlock()
	tools := a.primaryTools
	if configuration.Mode != "" {
		tools = constrainedToolSet(tools, configuration.Tools)
	}
	results := make([]toolResult, len(calls))
	ready := make([]bool, len(calls))

	value.stateMu.Lock()
	progress := cloneSuspendedExchange(value.state.suspended)
	root := value.state.cwd
	value.stateMu.Unlock()
	if progress == nil {
		return nil, false, errors.New("suspended tool batch has no durable state")
	}
	for index, call := range calls {
		results[index].target = progress.ToolTargets[call.ID]
	}
	for _, recorded := range progress.Decisions {
		if recorded.Decision == decisionAllowAlways {
			call := calls[progress.callIndex(recorded.CallID)]
			value.grant(call.Function.Name, recorded.Rule)
		}
	}

	batchCancelled := false
	for index := 0; index < len(calls); index++ {
		call := calls[index]
		value.stateMu.Lock()
		progress = cloneSuspendedExchange(value.state.suspended)
		value.stateMu.Unlock()
		if progress == nil {
			return nil, false, errors.New("suspended tool batch lost its durable state")
		}
		if recorded := progress.decision(call.ID); recorded != nil {
			results[index].approval = recorded.Decision
			switch recorded.Decision {
			case decisionAllowOnce, decisionAllowAlways:
				ready[index] = true
			case decisionCancelled:
				results[index].content = "tool call cancelled before start"
				results[index].failed = true
				batchCancelled = true
			default:
				results[index].content = "the user rejected this tool call"
				results[index].failed = true
			}
			if batchCancelled {
				break
			}
			continue
		}

		toolIndex, known := tools.byName[call.Function.Name]
		if !known || tools.tools[toolIndex].Approval == ApprovalNone {
			ready[index] = true
			continue
		}
		tool := tools.tools[toolIndex]
		arguments := json.RawMessage(call.Function.Arguments)
		if value.granted(tool, arguments) {
			ready[index] = true
			continue
		}
		rule := ""
		if tool.Suggest != nil {
			rule = tool.Suggest(arguments)
		}

		value.approvalMu.Lock()
		if value.granted(tool, arguments) {
			value.approvalMu.Unlock()
			ready[index] = true
			continue
		}
		request := a.permissionRequest(
			value.id, root, tool, call, rule, "", results[index].target,
		)
		value.stateMu.Lock()
		progress = cloneSuspendedExchange(value.state.suspended)
		value.stateMu.Unlock()
		if progress.Pending == nil {
			if err := a.commit(value, recordPermissionOpen, permissionRequestedRecord{
				TurnID: progress.TurnID,
				Pending: pendingPermissionRecord{
					CallID: call.ID, Generation: 1, Request: request,
				},
			}); err != nil {
				value.approvalMu.Unlock()
				return nil, false, fmt.Errorf("persist permission request: %w", err)
			}
			progress.Pending = &pendingPermissionRecord{
				CallID: call.ID, Generation: 1, Request: request,
			}
			reissue = false
		} else if progress.Pending.CallID != call.ID {
			value.approvalMu.Unlock()
			return nil, false, errors.New("pending permission is out of tool-call order")
		}
		if reissue {
			generation := progress.Pending.Generation + 1
			if err := a.commit(value, recordPermissionRetry, permissionReissuedRecord{
				TurnID: progress.TurnID, CallID: call.ID, Generation: generation,
			}); err != nil {
				value.approvalMu.Unlock()
				return nil, false, fmt.Errorf("persist permission reissue: %w", err)
			}
			progress.Pending.Generation = generation
			reissue = false
		}
		generation := progress.Pending.Generation
		a.logger.Info(
			"tool approval requested", "session_id", value.id,
			"tool_call_id", call.ID, "tool", call.Function.Name,
			"generation", generation, "rule_scoped", tool.Suggest != nil,
			"rule_derived", rule != "",
		)
		response, askErr := ask(ctx, progress.Pending.Request)
		decision := decideApproval(response, askErr)
		if ctx.Err() != nil {
			decision = decisionCancelled
		}
		if decision == decisionAllowAlways && tool.Suggest != nil && rule == "" {
			decision = decisionAllowOnce
		}
		recorded, err := a.commitPermissionDecision(value, permissionDecidedRecord{
			TurnID: progress.TurnID, CallID: call.ID, Generation: generation,
			Decision: decision, Rule: permissionDecisionRule(decision, rule),
		})
		if err != nil {
			value.approvalMu.Unlock()
			return nil, false, fmt.Errorf("persist permission decision: %w", err)
		}
		if !recorded {
			value.approvalMu.Unlock()
			index--
			continue
		}
		if decision == decisionAllowAlways {
			value.grant(call.Function.Name, rule)
		}
		value.approvalMu.Unlock()
		results[index].approval = decision
		a.logger.Info(
			"tool approval decided", "session_id", value.id,
			"tool_call_id", call.ID, "tool", call.Function.Name,
			"generation", generation, "decision", decision,
		)
		switch decision {
		case decisionAllowOnce, decisionAllowAlways:
			ready[index] = true
		case decisionCancelled:
			results[index].content = "tool call cancelled before start"
			results[index].failed = true
			batchCancelled = true
		default:
			results[index].content = "the user rejected this tool call"
			results[index].failed = true
		}
		if batchCancelled {
			break
		}
	}

	if batchCancelled {
		for index := range calls {
			if ready[index] {
				ready[index] = false
				results[index].content = "tool call cancelled before start"
				results[index].failed = true
			}
			if results[index].content == "" {
				results[index] = toolResult{
					content: "tool call cancelled before start", failed: true,
					approval: decisionCancelled,
					target:   progress.ToolTargets[calls[index].ID],
				}
			}
		}
	}
	var err error
	results, err = a.dispatchApprovedBatch(
		ctx, value, tools, value.primaryFileReads(), calls, ask,
		fileSystem, terminal, events, "", results, ready, turn,
	)
	if err != nil {
		return nil, false, err
	}
	a.logger.Info("suspended tool batch completed", "session_id", value.id, "calls", len(calls))
	return results, batchCancelled, nil
}

func permissionDecisionRule(decision approvalDecision, rule string) string {
	if decision == decisionAllowAlways {
		return rule
	}
	return ""
}

func (a *Agent) permissionRequest(
	sessionID string,
	root string,
	tool Tool,
	call openrouter.ToolCall,
	rule string,
	parent string,
	target string,
) acp.RequestPermissionRequest {
	arguments := json.RawMessage(call.Function.Arguments)
	return acp.RequestPermissionRequest{
		SessionID: sessionID,
		ToolCall: acp.ToolCallUpdate{
			ToolCallID: call.ID, Kind: tool.Kind,
			Title: a.toolTitle(call.Function.Name, arguments), Name: call.Function.Name,
			Locations: toolLocations(root, target), RawInput: arguments,
			Meta: toolEventMetadata(parent, false),
		},
		Options: permissionOptions(rule, tool.Suggest != nil),
	}
}

func (a *Agent) executeBatch(
	ctx context.Context,
	value *session,
	calls []openrouter.ToolCall,
	ask requestPermission,
	events chan<- event,
) []toolResult {
	results, _ := a.executeBatchWith(
		ctx,
		value,
		a.primaryTools,
		value.primaryFileReads(),
		calls,
		ask,
		ClientFileSystem{},
		ClientTerminal{},
		events,
		"",
		diagnostictrace.Turn{},
	)
	return results
}

func (a *Agent) executeBatchWith(
	ctx context.Context,
	value *session,
	tools toolSet,
	reads FileReads,
	calls []openrouter.ToolCall,
	ask requestPermission,
	fileSystem ClientFileSystem,
	terminal ClientTerminal,
	events chan<- event,
	parent string,
	turn diagnostictrace.Turn,
) ([]toolResult, error) {
	value.stateMu.Lock()
	configuration := value.state.turnConfiguration()
	value.stateMu.Unlock()
	declarations := configuration.Tools
	if parent != "" {
		declarations = configuration.Subagent.Tools
	}
	if configuration.Mode != "" {
		tools = constrainedToolSet(tools, declarations)
	}
	a.logger.Info("tool batch started", "session_id", value.id, "calls", len(calls))
	value.stateMu.Lock()
	root := value.state.cwd
	value.stateMu.Unlock()
	targets := normalizedToolTargets(root, tools, calls)
	for _, call := range calls {
		turn.ToolPending(call.ID, call.Function.Name, parent)
		var kind acp.ToolKind
		var delegates bool
		if index, ok := tools.byName[call.Function.Name]; ok {
			kind = tools.tools[index].Kind
			delegates = tools.tools[index].Delegates
		}
		events <- event{
			kind:      eventToolPending,
			call:      call,
			toolKind:  kind,
			parent:    parent,
			title:     a.toolTitle(call.Function.Name, json.RawMessage(call.Function.Arguments)),
			target:    targets[call.ID],
			delegates: delegates,
		}
	}

	results := make([]toolResult, len(calls))
	for index, call := range calls {
		results[index].target = targets[call.ID]
	}
	ready := make([]bool, len(calls))
	handled := make([]bool, len(calls))
	batchCancelled := false
approvalLoop:
	for index, call := range calls {
		if ctx.Err() != nil {
			break
		}
		handled[index] = true
		toolIndex, known := tools.byName[call.Function.Name]
		if !known || tools.tools[toolIndex].Approval == ApprovalNone {
			ready[index] = true
			continue
		}
		tool := tools.tools[toolIndex]
		arguments := json.RawMessage(call.Function.Arguments)
		if value.granted(tool, arguments) {
			ready[index] = true
			continue
		}
		rule := ""
		if tool.Suggest != nil {
			rule = tool.Suggest(arguments)
		}
		value.approvalMu.Lock()
		if ctx.Err() != nil {
			value.approvalMu.Unlock()
			results[index] = toolResult{
				content:  "tool call cancelled before start",
				failed:   true,
				approval: decisionCancelled,
				target:   targets[call.ID],
			}
			batchCancelled = true
			break approvalLoop
		}
		if value.granted(tool, arguments) {
			value.approvalMu.Unlock()
			ready[index] = true
			continue
		}
		request := a.permissionRequest(
			value.id, root, tool, call, rule, parent, results[index].target,
		)
		a.logger.Info(
			"tool approval requested",
			"session_id", value.id,
			"tool_call_id", call.ID,
			"tool", call.Function.Name,
			"rule_scoped", tool.Suggest != nil,
			"rule_derived", rule != "",
		)
		response, err := ask(ctx, request)
		decision := decideApproval(response, err)
		if ctx.Err() != nil {
			decision = decisionCancelled
		}
		if decision == decisionAllowAlways {
			if tool.Suggest != nil && rule == "" {
				decision = decisionAllowOnce
				a.logger.Info(
					"tool allow-always ignored without a derivable rule",
					"session_id", value.id,
					"tool_call_id", call.ID,
					"tool", call.Function.Name,
				)
			} else {
				value.grant(call.Function.Name, rule)
				a.logger.Info(
					"tool approval granted for activation",
					"session_id", value.id,
					"tool_call_id", call.ID,
					"tool", call.Function.Name,
					"rule_scoped", tool.Suggest != nil,
				)
			}
		}
		value.approvalMu.Unlock()
		results[index].approval = decision
		a.logger.Info(
			"tool approval decided",
			"session_id", value.id,
			"tool_call_id", call.ID,
			"tool", call.Function.Name,
			"decision", decision,
			"rule_scoped", tool.Suggest != nil,
			"rule_derived", rule != "",
		)
		switch decision {
		case decisionAllowOnce, decisionAllowAlways:
			ready[index] = true
		case decisionCancelled:
			results[index].content = "tool call cancelled before start"
			results[index].failed = true
			batchCancelled = true
			break approvalLoop
		default:
			results[index].content = "the user rejected this tool call"
			results[index].failed = true
		}
	}
	for index := range calls {
		if !handled[index] {
			results[index] = toolResult{
				content:  "tool call cancelled before start",
				failed:   true,
				approval: decisionCancelled,
				target:   targets[calls[index].ID],
			}
		}
	}
	if batchCancelled {
		for index := range ready {
			if ready[index] {
				ready[index] = false
				results[index].content = "tool call cancelled before start"
				results[index].failed = true
			}
		}
	}

	var err error
	results, err = a.dispatchApprovedBatch(
		ctx, value, tools, reads, calls, ask, fileSystem, terminal,
		events, parent, results, ready, turn,
	)
	a.logger.Info("tool batch completed", "session_id", value.id, "calls", len(calls))
	return results, err
}

func (a *Agent) dispatchApprovedBatch(
	ctx context.Context,
	value *session,
	tools toolSet,
	reads FileReads,
	calls []openrouter.ToolCall,
	ask requestPermission,
	fileSystem ClientFileSystem,
	terminal ClientTerminal,
	events chan<- event,
	parent string,
	results []toolResult,
	ready []bool,
	turn diagnostictrace.Turn,
) ([]toolResult, error) {
	groups := a.partitionWith(tools, calls)
	executed := make([]bool, len(calls))
	for _, group := range groups {
		if ctx.Err() != nil {
			break
		}
		groupCtx, cancelGroup := context.WithCancel(ctx)
		var wait sync.WaitGroup
		errorsByIndex := make([]error, group.end-group.start)
		startFailed := false
		for index := group.start; index < group.end; index++ {
			if !ready[index] {
				continue
			}
			if value.log != nil {
				progress, exists := toolExecution(value, calls[index].ID)
				if exists {
					executed[index] = true
					if progress.Result == nil {
						unknown := storedToolResult{
							CallID: calls[index].ID, Content: unknownToolOutcome,
							Failed: true, ApprovalDecision: progress.ApprovalDecision,
							Target: progress.Target, Unknown: true,
						}
						if err := a.completeToolExecution(
							value, progress.TurnID, progress.ParentCallID, calls[index].ID, unknown,
						); err != nil {
							cancelGroup()
							return results, fmt.Errorf("persist unknown tool outcome: %w", err)
						}
						results[index] = toolResultFromStored(unknown)
						results[index].delegation = interruptedDelegation(value, calls[index].ID)
					} else {
						results[index] = toolResultFromStored(*progress.Result)
						if progress.Result.Unknown {
							results[index].delegation = interruptedDelegation(value, calls[index].ID)
						}
					}
					continue
				}
				value.stateMu.Lock()
				turnID := value.state.openTurn
				value.stateMu.Unlock()
				if err := a.startToolExecution(
					value, turnID, parent, calls[index], results[index].approval,
					results[index].target,
				); err != nil {
					startFailed = true
					errorsByIndex[index-group.start] = fmt.Errorf("persist tool dispatch: %w", err)
					cancelGroup()
					break
				}
			}
			current := index
			wait.Add(1)
			executed[current] = true
			go func() {
				defer wait.Done()
				decision := results[current].approval
				results[current] = a.executeOne(
					groupCtx,
					value,
					tools,
					reads,
					calls[current],
					ask,
					fileSystem,
					terminal,
					events,
					parent,
					results[current].target,
					turn,
				)
				results[current].approval = decision
				if value.log != nil {
					stored := storedResult(calls[current].ID, results[current])
					value.stateMu.Lock()
					turnID := value.state.openTurn
					value.stateMu.Unlock()
					if err := a.completeToolExecution(
						value, turnID, parent, calls[current].ID, stored,
					); err != nil {
						errorsByIndex[current-group.start] = fmt.Errorf(
							"persist tool completion: %w", err,
						)
						cancelGroup()
					}
				}
			}()
		}
		wait.Wait()
		cancelGroup()
		for _, err := range errorsByIndex {
			if err != nil {
				return results, err
			}
		}
		if startFailed {
			break
		}
	}
	for index := range calls {
		if ready[index] && !executed[index] {
			results[index] = toolResult{
				content:  "tool call cancelled before start",
				failed:   true,
				approval: results[index].approval,
				target:   results[index].target,
			}
		}
	}
	return results, nil
}

func storedResult(callID string, result toolResult) storedToolResult {
	return storedToolResult{
		CallID:           callID,
		Content:          result.content,
		Failed:           result.failed,
		ApprovalDecision: result.approval,
		Delegation:       result.delegation,
		Target:           result.target,
		Unknown:          result.unknown,
	}
}

func toolResultFromStored(result storedToolResult) toolResult {
	return toolResult{
		content: result.Content, failed: result.Failed,
		approval: result.ApprovalDecision, delegation: result.Delegation,
		target: result.Target, unknown: result.Unknown,
	}
}

func (a *Agent) partition(calls []openrouter.ToolCall) []toolGroup {
	return a.partitionWith(a.primaryTools, calls)
}

func (a *Agent) partitionWith(tools toolSet, calls []openrouter.ToolCall) []toolGroup {
	groups := make([]toolGroup, 0, len(calls))
	for index := 0; index < len(calls); {
		toolIndex, known := tools.byName[calls[index].Function.Name]
		if !known || !tools.tools[toolIndex].ParallelSafe {
			groups = append(groups, toolGroup{start: index, end: index + 1})
			index++
			continue
		}
		end := index + 1
		for end < len(calls) {
			nextIndex, exists := tools.byName[calls[end].Function.Name]
			if !exists || !tools.tools[nextIndex].ParallelSafe {
				break
			}
			end++
		}
		groups = append(groups, toolGroup{start: index, end: end})
		index = end
	}
	return groups
}

func (a *Agent) executeOne(
	ctx context.Context,
	value *session,
	tools toolSet,
	reads FileReads,
	call openrouter.ToolCall,
	ask requestPermission,
	fileSystem ClientFileSystem,
	terminal ClientTerminal,
	events chan<- event,
	parent string,
	target string,
	turn diagnostictrace.Turn,
) (result toolResult) {
	index, ok := tools.byName[call.Function.Name]
	if ok && !tools.tools[index].ParallelSafe {
		value.exclusiveMu.Lock()
		defer value.exclusiveMu.Unlock()
		if ctx.Err() != nil {
			return toolResult{
				content: "tool call cancelled before start", failed: true, target: target,
			}
		}
	} else if ctx.Err() != nil {
		return toolResult{
			content: "tool call cancelled before start", failed: true, target: target,
		}
	}
	turn.ToolStarted(call.ID, call.Function.Name, parent)
	events <- a.toolEvent(tools, call, eventToolStarted, parent, "", target)
	started := time.Now()
	path := toolCallPath(call.Function.Arguments)
	defer func() {
		if recovered := recover(); recovered != nil {
			result = toolResult{
				content: fmt.Sprintf("tool panicked: %v", recovered),
				failed:  true,
				target:  target,
			}
		}
		a.logger.Info(
			"tool call finished",
			"tool_call_id", call.ID,
			"tool", call.Function.Name,
			"path", path,
			"duration", time.Since(started),
			"failed", result.failed,
		)
	}()
	if !ok {
		return toolResult{
			content: fmt.Sprintf("unknown tool %q", call.Function.Name),
			failed:  true,
			target:  target,
		}
	}
	tool := tools.tools[index]
	if tool.Execute == nil {
		return toolResult{content: "tool has no executor", failed: true, target: target}
	}
	value.stateMu.Lock()
	root := value.state.cwd
	configuration := value.state.turnConfiguration()
	value.stateMu.Unlock()
	var delegation *delegationRecord
	invocation := Invocation{
		Arguments:  json.RawMessage(call.Function.Arguments),
		SessionID:  value.id,
		Root:       root,
		SpillDir:   a.store.spillDir(value.id),
		CallID:     call.ID,
		FileReads:  reads,
		FileSystem: fileSystem,
		Terminal:   terminal,
		LoadSkill: func(name string) (string, error) {
			for _, reference := range configuration.Skills {
				if reference.Name == name {
					return skills.Load(root, reference)
				}
			}
			return "", fmt.Errorf("workspace skill %q is not in the active catalog", name)
		},
		Emit: func(text string) {
			events <- event{kind: eventToolOutput, call: call, text: text}
		},
		ReportSpill: func(path string) {
			a.logger.Info(
				"tool output spilled",
				"tool_call_id", call.ID,
				"tool", call.Function.Name,
				"path", path,
			)
		},
	}
	if parent == "" && tool.Name == "todo" {
		invocation.ReplaceTodo = func(entries []acp.PlanEntry) error {
			value.stateMu.Lock()
			turnID := value.state.openTurn
			value.stateMu.Unlock()
			if err := a.commit(value, recordTodoChanged, todoChanged{
				TurnID: turnID, CallID: call.ID, Entries: clonePlanEntries(entries),
			}); err != nil {
				return fmt.Errorf("persist todo replacement: %w", err)
			}
			events <- event{kind: eventPlan, plan: clonePlanEntries(entries)}
			return nil
		}
	}
	if tool.Delegates {
		invocation.Delegate = func(delegateCtx context.Context, prompt string) (string, error) {
			var err error
			var answer string
			answer, delegation, err = a.delegate(
				delegateCtx,
				value,
				call.ID,
				prompt,
				ask,
				fileSystem,
				terminal,
				events,
				turn,
			)
			return answer, err
		}
	}
	output, err := tool.Execute(ctx, invocation)
	if err != nil {
		if ctx.Err() != nil {
			result := toolResult{
				content: "tool call cancelled",
				failed:  true,
				target:  target,
			}
			if delegationHasActivity(delegation) {
				result.delegation = delegation
			}
			return result
		}
		a.logger.Info(
			"tool call refused",
			"tool_call_id", call.ID,
			"tool", call.Function.Name,
			"error", err,
		)
		return toolResult{
			content:    "tool error: " + err.Error(),
			failed:     true,
			delegation: delegation,
			target:     target,
		}
	}
	return toolResult{content: output, delegation: delegation, target: target}
}

func toolOutcome(result toolResult) string {
	if result.approval == decisionCancelled {
		return "cancelled"
	}
	if result.approval == decisionRefused {
		return "refused"
	}
	if result.failed {
		return "failed"
	}
	return "completed"
}

func providerRequestBytes(request openrouter.Request) int {
	data, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return len(data)
}

func providerResponseBytes(completion *openrouter.Completion) int {
	if completion == nil {
		return 0
	}
	size := len(completion.Text) + len(completion.Reasoning)
	for _, detail := range completion.ReasoningDetails {
		size += len(detail)
	}
	for _, call := range completion.ToolCalls {
		size += len(call.ID) + len(call.Function.Name) + len(call.Function.Arguments)
	}
	return size
}

func providerUsage(completion *openrouter.Completion) diagnostictrace.Usage {
	if completion == nil || completion.Usage == nil {
		return diagnostictrace.Usage{}
	}
	usage := diagnostictrace.Usage{
		InputTokens:  completion.Usage.PromptTokens,
		OutputTokens: completion.Usage.CompletionTokens,
	}
	if completion.Usage.CompletionTokensDetails != nil {
		usage.ReasoningTokens = completion.Usage.CompletionTokensDetails.ReasoningTokens
	}
	return usage
}

func providerStopReason(completion *openrouter.Completion) string {
	if completion == nil {
		return ""
	}
	return completion.FinishReason
}

func providerOutcome(
	ctx context.Context,
	err error,
	completion *openrouter.Completion,
) string {
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return "cancelled"
		}
		return "failed"
	}
	if completion != nil &&
		(completion.FinishReason == "refusal" || completion.FinishReason == "content_filter") {
		return "refused"
	}
	return "completed"
}

func delegationHasActivity(value *delegationRecord) bool {
	return value != nil &&
		(value.Prompt != "" || len(value.Calls) > 0 || len(value.Usage) > 0 || value.Answer != "")
}

func (a *Agent) toolEvent(
	tools toolSet,
	call openrouter.ToolCall,
	kind eventKind,
	parent string,
	text string,
	target string,
) event {
	delegates := false
	var toolKind acp.ToolKind
	if index, ok := tools.byName[call.Function.Name]; ok {
		delegates = tools.tools[index].Delegates
		toolKind = tools.tools[index].Kind
	}
	return event{
		kind:      kind,
		call:      call,
		toolKind:  toolKind,
		parent:    parent,
		title:     a.toolTitle(call.Function.Name, json.RawMessage(call.Function.Arguments)),
		target:    target,
		delegates: delegates,
		text:      text,
	}
}

func (a *Agent) toolTitle(name string, arguments json.RawMessage) string {
	if index, ok := a.primaryTools.byName[name]; ok {
		if label := a.primaryTools.tools[index].Label; label != nil {
			if title := label(arguments); title != "" {
				return title
			}
		}
	}
	return name
}

func (a *Agent) toolDelegates(name string) bool {
	index, ok := a.primaryTools.byName[name]
	return ok && a.primaryTools.tools[index].Delegates
}

func toolCallPath(arguments string) string {
	var input struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return ""
	}
	return input.Path
}

func normalizedToolTargets(
	root string,
	tools toolSet,
	calls []openrouter.ToolCall,
) map[string]string {
	targets := make(map[string]string)
	files := workspace.NewWorkspace(root)
	for _, call := range calls {
		index, ok := tools.byName[call.Function.Name]
		if !ok || tools.tools[index].Kind != acp.ToolKindEdit {
			continue
		}
		path := toolCallPath(call.Function.Arguments)
		if path == "" {
			continue
		}
		target, ok := files.Key(path)
		if ok && validStoredTarget(target) {
			targets[call.ID] = target
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return targets
}

func (a *Agent) publishUsage(
	value *session,
	current *openrouter.Usage,
	events chan<- event,
) {
	if current == nil {
		a.logger.Info("model request completed without usage", "session_id", value.id)
		return
	}
	var cachedRead, cachedWrite int
	if current.PromptTokensDetails != nil {
		cachedRead = current.PromptTokensDetails.CachedTokens
		cachedWrite = current.PromptTokensDetails.CacheWriteTokens
	}
	a.logger.Info(
		"model request completed",
		"session_id", value.id,
		"total_tokens", current.TotalTokens,
		"cache_read_tokens", cachedRead,
		"cache_write_tokens", cachedWrite,
		"session_cost", value.state.cost,
	)
	configuration := value.state.turnConfiguration()
	if configuration.ContextWindow > 0 {
		events <- event{
			kind:             eventUsage,
			contextOccupancy: current.PromptTokens,
			contextWindow:    configuration.ContextWindow,
			totalCost:        value.state.cost,
		}
	}
}

func (u turnUsage) acp() *acp.Usage {
	if !u.seen {
		return nil
	}
	thought := u.thought
	cachedRead := u.cachedRead
	cachedWrite := u.cachedWrite
	return &acp.Usage{
		TotalTokens:       u.input + u.output,
		InputTokens:       u.input,
		OutputTokens:      u.output,
		ThoughtTokens:     &thought,
		CachedReadTokens:  &cachedRead,
		CachedWriteTokens: &cachedWrite,
	}
}

func stopReason(reason string) acp.StopReason {
	switch reason {
	case "length":
		return acp.StopReasonMaxTokens
	case "refusal", "content_filter":
		return acp.StopReasonRefusal
	default:
		return acp.StopReasonEndTurn
	}
}

func cloneMessages(messages []openrouter.Message) []openrouter.Message {
	cloned := make([]openrouter.Message, len(messages))
	for index, message := range messages {
		cloned[index] = message
		cloned[index].Content = append([]openrouter.ContentBlock(nil), message.Content...)
		cloned[index].ToolCalls = append([]openrouter.ToolCall(nil), message.ToolCalls...)
		cloned[index].ReasoningDetails = cloneRawMessages(message.ReasoningDetails)
	}
	return cloned
}

func cloneRawMessages(messages []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, len(messages))
	for index := range messages {
		cloned[index] = append([]byte(nil), messages[index]...)
	}
	return cloned
}
