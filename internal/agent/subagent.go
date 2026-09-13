package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/openrouter"
	diagnostictrace "github.com/kkestell/ox/internal/trace"
)

const (
	maxActiveSubagents      = 4
	maxTurnSubagents        = 8
	maxSubagentName         = 80
	maxSubagentTask         = 64 << 10
	maxSubagentMessage      = 16 << 10
	maxSubagentReportBytes  = 64 << 10
	maxSubagentReports      = 32
	maxSubagentResult       = 64 << 10
	subagentStatusRunning   = "running"
	subagentStatusStopping  = "stopping"
	subagentStatusCompleted = "completed"
	subagentStatusFailed    = "failed"
	subagentStatusCancelled = "cancelled"
)

const subagentPromptSuffix = `

<subagent-role>
You are a subagent working on one standalone task for the primary agent. You do
not see the user's conversation. Work only from your task and later messages
from the primary agent. Other subagents may use the same workspace concurrently,
so avoid files outside your assigned work and re-read shared files before
editing them. You cannot start or control other subagents. Use subagent_report
for useful interim findings or coordination; your final answer is delivered to
the primary automatically and must be complete.
</subagent-role>`

type SubagentMessage struct {
	Sequence int    `json:"sequence"`
	Text     string `json:"text"`
}

type SubagentSnapshot struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Status   string            `json:"status"`
	Messages []SubagentMessage `json:"messages,omitempty"`
	Result   string            `json:"result,omitempty"`
	Error    string            `json:"error,omitempty"`
}

type subagentGroup struct {
	agent         *Agent
	ctx           context.Context
	cancel        context.CancelFunc
	run           turnRun
	configuration requestConfiguration
	tools         toolSet

	mu       sync.Mutex
	children map[string]*subagent
	order    []string
	changed  chan struct{}
	closed   bool
	wait     sync.WaitGroup
}

type subagent struct {
	id               string
	name             string
	status           string
	result           string
	err              string
	inbox            []string
	reports          []SubagentMessage
	reportBytes      int
	observed         int
	terminalObserved bool
	cancel           context.CancelFunc
}

func newSubagentGroup(a *Agent, ctx context.Context, run turnRun) *subagentGroup {
	groupCtx, cancel := context.WithCancel(ctx)
	configuration := run.session.turnConfiguration()
	tools := run.session.subagentTools
	if configuration.Mode == modePlan {
		allowed := configuredPlanTools(tools)
		tools = constrainedToolSet(tools, planTools(tools.modelTools, allowed))
	}
	group := &subagentGroup{
		agent: a, ctx: groupCtx, cancel: cancel, run: run,
		configuration: configuration, tools: tools,
		children: make(map[string]*subagent), changed: make(chan struct{}),
	}
	group.run.subagents = group
	return group
}

func (g *subagentGroup) start(name, task string) (SubagentSnapshot, error) {
	if len(name) > maxSubagentName {
		return SubagentSnapshot{}, fmt.Errorf("subagent name exceeds %d bytes", maxSubagentName)
	}
	if len(task) > maxSubagentTask {
		return SubagentSnapshot{}, fmt.Errorf("subagent task exceeds %d bytes", maxSubagentTask)
	}
	id, err := randomID()
	if err != nil {
		return SubagentSnapshot{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return SubagentSnapshot{}, errors.New("the subagent group is closed")
	}
	if len(g.order) >= maxTurnSubagents {
		return SubagentSnapshot{}, fmt.Errorf("a turn may start at most %d subagents", maxTurnSubagents)
	}
	active := 0
	for _, child := range g.children {
		if child.status == subagentStatusRunning || child.status == subagentStatusStopping {
			active++
		}
		if child.name == name {
			return SubagentSnapshot{}, fmt.Errorf("subagent name %q is already used in this turn", name)
		}
	}
	if active >= maxActiveSubagents {
		return SubagentSnapshot{}, fmt.Errorf("at most %d subagents may run concurrently", maxActiveSubagents)
	}
	childCtx, cancel := context.WithCancel(g.ctx)
	child := &subagent{id: id, name: name, status: subagentStatusRunning, cancel: cancel}
	g.children[id] = child
	g.order = append(g.order, id)
	g.signalLocked()
	g.wait.Add(1)
	go func() {
		defer g.wait.Done()
		reads, releaseReads := g.run.session.childFileReads()
		defer releaseReads()
		status, result, runErr := g.agent.runSubagent(childCtx, g, id, task, reads)
		g.complete(id, status, result, runErr)
	}()
	return snapshotSubagent(child), nil
}

func (g *subagentGroup) send(id, message string) (SubagentSnapshot, error) {
	if len(message) > maxSubagentMessage {
		return SubagentSnapshot{}, fmt.Errorf("subagent message exceeds %d bytes", maxSubagentMessage)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	child, err := g.runningChildLocked(id)
	if err != nil {
		return SubagentSnapshot{}, err
	}
	child.inbox = append(child.inbox, message)
	g.signalLocked()
	result := snapshotSubagent(child)
	child.observed = len(child.reports)
	return result, nil
}

func (g *subagentGroup) stop(id string) (SubagentSnapshot, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	child, ok := g.children[id]
	if !ok {
		return SubagentSnapshot{}, fmt.Errorf("unknown subagent %q", id)
	}
	if terminalSubagentStatus(child.status) {
		result := snapshotSubagent(child)
		child.observed = len(child.reports)
		child.terminalObserved = true
		return result, nil
	}
	child.status = subagentStatusStopping
	child.cancel()
	g.signalLocked()
	result := snapshotSubagent(child)
	child.observed = len(child.reports)
	return result, nil
}

func (g *subagentGroup) list() []SubagentSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.snapshotsLocked(nil, true)
}

func (g *subagentGroup) waitFor(ctx context.Context, ids []string) ([]SubagentSnapshot, error) {
	for {
		g.mu.Lock()
		selected, err := g.selectedLocked(ids)
		if err != nil {
			g.mu.Unlock()
			return nil, err
		}
		if len(selected) == 0 {
			g.mu.Unlock()
			return nil, errors.New("no subagents have been started")
		}
		ready, allTerminal := false, true
		for _, id := range selected {
			child := g.children[id]
			terminal := terminalSubagentStatus(child.status)
			if child.observed < len(child.reports) || terminal && !child.terminalObserved {
				ready = true
			}
			if !terminal {
				allTerminal = false
			}
		}
		if ready || allTerminal {
			result := g.snapshotsLocked(selected, true)
			g.mu.Unlock()
			return result, nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (g *subagentGroup) report(id, message string) error {
	if len(message) > maxSubagentMessage {
		return fmt.Errorf("subagent report exceeds %d bytes", maxSubagentMessage)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	child, err := g.runningChildLocked(id)
	if err != nil {
		return err
	}
	if len(child.reports) >= maxSubagentReports {
		return fmt.Errorf("subagent may send at most %d interim reports", maxSubagentReports)
	}
	if child.reportBytes+len(message) > maxSubagentReportBytes {
		return fmt.Errorf("subagent reports may contain at most %d bytes total", maxSubagentReportBytes)
	}
	child.reports = append(child.reports, SubagentMessage{
		Sequence: len(child.reports) + 1,
		Text:     message,
	})
	child.reportBytes += len(message)
	g.signalLocked()
	return nil
}

func (g *subagentGroup) drainInbox(id string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	child := g.children[id]
	if child == nil || len(child.inbox) == 0 {
		return nil
	}
	messages := slices.Clone(child.inbox)
	child.inbox = nil
	return messages
}

func (g *subagentGroup) complete(id, status, result string, runErr error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	child := g.children[id]
	if child == nil || terminalSubagentStatus(child.status) {
		return
	}
	child.status = status
	child.result = boundedSubagentResult(result)
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		child.err = runErr.Error()
	}
	g.signalLocked()
}

func (g *subagentGroup) close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		g.wait.Wait()
		return
	}
	g.closed = true
	for _, child := range g.children {
		if !terminalSubagentStatus(child.status) {
			child.status = subagentStatusStopping
			child.cancel()
		}
	}
	g.cancel()
	g.signalLocked()
	g.mu.Unlock()
	g.wait.Wait()
}

func (g *subagentGroup) runningChildLocked(id string) (*subagent, error) {
	child, ok := g.children[id]
	if !ok {
		return nil, fmt.Errorf("unknown subagent %q", id)
	}
	if child.status != subagentStatusRunning {
		return nil, fmt.Errorf("subagent %q is %s", id, child.status)
	}
	return child, nil
}

func (g *subagentGroup) selectedLocked(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return slices.Clone(g.order), nil
	}
	selected := make([]string, len(ids))
	for index, id := range ids {
		if g.children[id] == nil {
			return nil, fmt.Errorf("unknown subagent %q", id)
		}
		selected[index] = id
	}
	return selected, nil
}

func (g *subagentGroup) snapshotsLocked(ids []string, observe bool) []SubagentSnapshot {
	if ids == nil {
		ids = g.order
	}
	result := make([]SubagentSnapshot, 0, len(ids))
	for _, id := range ids {
		child := g.children[id]
		result = append(result, snapshotSubagent(child))
		if observe {
			child.observed = len(child.reports)
			child.terminalObserved = terminalSubagentStatus(child.status)
		}
	}
	return result
}

func snapshotSubagent(child *subagent) SubagentSnapshot {
	return SubagentSnapshot{
		ID: child.id, Name: child.name, Status: child.status,
		Messages: slices.Clone(child.reports), Result: child.result, Error: child.err,
	}
}

func (g *subagentGroup) signalLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func terminalSubagentStatus(status string) bool {
	return status == subagentStatusCompleted || status == subagentStatusFailed ||
		status == subagentStatusCancelled
}

func (a *Agent) runSubagent(
	ctx context.Context,
	group *subagentGroup,
	id string,
	task string,
	reads FileReads,
) (string, string, error) {
	history := []openrouter.Message{{
		Role:    openrouter.RoleUser,
		Content: []openrouter.ContentBlock{{Type: "text", Text: task}},
	}}
	providerIDs := make(map[string]struct{})
	run := group.run
	run.subagentID = id
	for requestCount := 1; ; requestCount++ {
		if err := ctx.Err(); err != nil {
			return subagentStatusCancelled, "", err
		}
		if incoming := group.drainInbox(id); len(incoming) > 0 {
			history = append(history, subagentInboxMessage(incoming))
		}
		request := subagentRequest(group.configuration, group.tools, group.run.session.id, history)
		planned, err := planRequestAdmission(request, group.configuration.ContextWindow)
		if err != nil {
			return subagentStatusFailed, "", err
		}
		var admissionUsage *openrouter.Usage
		if planned.plan != nil {
			summary, summaryErr := a.summarizeContext(
				ctx, group.run.session.id, group.configuration,
				request.Messages[planned.plan.headEnd:planned.plan.tailStart],
				group.run.active.trace, requestCount,
			)
			if summaryErr != nil {
				return subagentStatusFailed, "", summaryErr
			}
			admitted, compactErr := compactRequest(
				request, *planned.plan, summary.Text, group.configuration.ContextWindow,
			)
			if compactErr != nil {
				return subagentStatusFailed, "", compactErr
			}
			request = admitted.request
			history = cloneMessages(request.Messages[1:])
			admissionUsage = summary.Usage
		}
		provider := group.run.active.trace.Provider(
			diagnostictrace.ProviderSubagent,
			requestCount,
			tracedRequestBytes(group.run.active.trace, request),
		)
		completion, streamErr := a.client.Stream(ctx, request, func(openrouter.Delta) {})
		provider.Complete(
			providerOutcome(ctx, streamErr, completion), providerStopReason(completion),
			providerUsage(completion), providerResponseBytes(completion),
		)
		if completion != nil || admissionUsage != nil {
			usage := admissionUsage
			occupancy := 0
			if completion != nil {
				usage = combinedUsage(admissionUsage, completion.Usage)
				if completion.Usage != nil {
					occupancy = completion.Usage.PromptTokens
				}
			}
			if usage != nil {
				if err := a.commitSubagentUsage(group.run.session, id, usage, occupancy); err != nil {
					return subagentStatusFailed, "", fmt.Errorf("persist subagent usage: %w", err)
				}
				if occupancy > 0 && group.configuration.ContextWindow > 0 {
					group.run.events <- event{
						kind: eventUsage, contextOccupancy: occupancy,
						contextWindow: group.configuration.ContextWindow,
						totalCost:     group.run.session.cost(),
					}
				}
			}
		}
		if streamErr != nil {
			if ctx.Err() != nil || errors.Is(streamErr, context.Canceled) {
				return subagentStatusCancelled, "", ctx.Err()
			}
			return subagentStatusFailed, "", fmt.Errorf("stream subagent response: %w", streamErr)
		}
		if err := ctx.Err(); err != nil {
			return subagentStatusCancelled, "", err
		}
		if completion == nil {
			return subagentStatusFailed, "", errors.New("subagent provider returned no completion")
		}
		assistant := openrouter.Message{
			Role:             openrouter.RoleAssistant,
			ReasoningDetails: cloneRawMessages(completion.ReasoningDetails),
			ToolCalls:        append([]openrouter.ToolCall(nil), completion.ToolCalls...),
		}
		if completion.Text != "" {
			assistant.Content = []openrouter.ContentBlock{{Type: "text", Text: completion.Text}}
		}
		history = append(history, assistant)
		if completion.FinishReason != "tool_calls" || len(completion.ToolCalls) == 0 {
			if completion.FinishReason == "refusal" || completion.FinishReason == "content_filter" {
				return subagentStatusFailed, "", errors.New("the provider refused the subagent response")
			}
			if incoming := group.drainInbox(id); len(incoming) > 0 {
				history = append(history, subagentInboxMessage(incoming))
				continue
			}
			if strings.TrimSpace(completion.Text) == "" {
				return subagentStatusFailed, "", errors.New("subagent returned an empty final answer")
			}
			return subagentStatusCompleted, completion.Text, nil
		}
		for _, call := range completion.ToolCalls {
			if call.ID == "" {
				return subagentStatusFailed, "", errors.New("subagent tool call ID is required")
			}
			if _, exists := providerIDs[call.ID]; exists {
				return subagentStatusFailed, "", fmt.Errorf("duplicate subagent tool call ID %q", call.ID)
			}
			providerIDs[call.ID] = struct{}{}
		}
		results, err := a.executeSubagentCalls(
			ctx, run, group.configuration.Mode, group.tools, reads, completion.ToolCalls,
		)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return subagentStatusCancelled, "", err
			}
			return subagentStatusFailed, "", err
		}
		for index, call := range completion.ToolCalls {
			history = append(history, openrouter.Message{
				Role: openrouter.RoleTool, ToolCallID: call.ID,
				Content: []openrouter.ContentBlock{{Type: "text", Text: results[index].content}},
			})
		}
	}
}

func subagentRequest(
	configuration requestConfiguration,
	tools toolSet,
	sessionID string,
	history []openrouter.Message,
) openrouter.Request {
	messages := make([]openrouter.Message, 1, len(history)+1)
	messages[0] = openrouter.Message{
		Role: openrouter.RoleSystem,
		Content: []openrouter.ContentBlock{{
			Type: "text", Text: configuration.SystemPrompt + subagentPromptSuffix,
		}},
	}
	messages = append(messages, cloneMessages(history)...)
	return openrouter.Request{
		Model: configuration.Settings.Model, Messages: messages,
		Tools: cloneTools(tools.modelTools), CacheControl: &openrouter.CacheControl{Type: "ephemeral"},
		SessionID: sessionID, MaxTokens: configuration.Settings.MaxTokens,
		Temperature: configuration.Settings.Temperature, Reasoning: configuration.Settings.Reasoning,
		Provider: configuration.Settings.Provider,
	}
}

func subagentInboxMessage(messages []string) openrouter.Message {
	return openrouter.Message{
		Role: openrouter.RoleUser,
		Content: []openrouter.ContentBlock{{
			Type: "text",
			Text: "Messages from the primary agent:\n\n" + strings.Join(messages, "\n\n---\n\n"),
		}},
	}
}

func (a *Agent) executeSubagentCalls(
	ctx context.Context,
	run turnRun,
	mode string,
	tools toolSet,
	reads FileReads,
	calls []openrouter.ToolCall,
) ([]toolResult, error) {
	results := make([]toolResult, len(calls))
	for index, providerCall := range calls {
		callID, err := allocateToolCallID(run.session)
		if err != nil {
			return nil, err
		}
		call := providerCall
		call.ID = callID
		target := normalizedToolTargets(run.session.workspaceRoot(), tools, []openrouter.ToolCall{call})[call.ID]
		run.active.trace.ToolPending(call.ID, call.Function.Name)
		run.events <- a.toolEvent(tools, call, eventToolPending, "", target)

		toolIndex, known := tools.byName[call.Function.Name]
		if known {
			tool := tools.tools[toolIndex]
			arguments := json.RawMessage(call.Function.Arguments)
			if !authorized(mode, run.session, tool, arguments) {
				run.session.approvalMu.Lock()
				if !run.session.granted(tool, arguments) {
					rule := ""
					if tool.Suggest != nil {
						rule = tool.Suggest(arguments)
					}
					request := a.permissionRequest(
						run.session.id, run.session.workspaceRoot(), tool, call, rule, target,
					)
					response, askErr := run.ask(ctx, request)
					decision := decideApproval(response, askErr)
					if ctx.Err() != nil {
						decision = decisionCancelled
					}
					if decision == decisionAllowAlways && tool.Suggest != nil && rule == "" {
						decision = decisionAllowOnce
					}
					results[index].approval = decision
					if decision == decisionAllowAlways {
						run.session.grant(tool.Name, rule)
					}
				}
				run.session.approvalMu.Unlock()
			}
		}
		switch results[index].approval {
		case decisionRefused:
			results[index] = toolResult{content: "the user rejected this tool call", failed: true, approval: decisionRefused, target: target}
		case decisionCancelled:
			results[index] = toolResult{content: toolCancelledBeforeStart, failed: true, approval: decisionCancelled, target: target}
		default:
			decision := results[index].approval
			results[index] = a.executeOne(ctx, run, tools, reads, call, target)
			results[index].approval = decision
		}
		run.active.trace.ToolCompleted(
			call.ID, call.Function.Name, toolOutcome(results[index]), len(results[index].content),
		)
		kind := eventToolCompleted
		if results[index].failed {
			kind = eventToolFailed
		}
		run.events <- a.toolEvent(tools, call, kind, results[index].content, target)
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
	}
	return results, nil
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
		_, live := value.callIDs[id]
		if !durable && !live {
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

func (a *Agent) commitSubagentUsage(
	value *session,
	subagentID string,
	usage *openrouter.Usage,
	occupancy int,
) error {
	return a.commit(value, recordSubagentUsage, subagentUsageRecord{
		TurnID: value.openTurn(), SubagentID: subagentID, Usage: usage, Occupancy: occupancy,
	})
}

func boundedSubagentResult(value string) string {
	if len(value) <= maxSubagentResult {
		return value
	}
	const marker = "\n[subagent result truncated]"
	end := maxSubagentResult - len(marker)
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + marker
}
