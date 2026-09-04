package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
)

const (
	recordVersion = 1

	recordSessionCreated = "session_created"
	recordConfigChanged  = "request_configuration_changed"
	recordUserMessage    = "user_message"
	recordModelExchange  = "completed_model_exchange"
	recordTurnFinished   = "turn_finished"
)

type sessionRecord struct {
	Version  int             `json:"version"`
	Sequence uint64          `json:"sequence"`
	Type     string          `json:"type"`
	At       time.Time       `json:"at"`
	Data     json.RawMessage `json:"data"`
}

type requestConfiguration struct {
	Settings      settings.Resolved       `json:"settings"`
	ContextWindow int                     `json:"contextWindow"`
	SystemPrompt  string                  `json:"systemPrompt,omitempty"`
	Tools         []openrouter.Tool       `json:"tools,omitempty"`
	ToolKinds     map[string]acp.ToolKind `json:"toolKinds,omitempty"`
	Subagent      subagentConfiguration   `json:"subagent,omitempty"`
}

type subagentConfiguration struct {
	SystemPrompt string            `json:"systemPrompt,omitempty"`
	Tools        []openrouter.Tool `json:"tools,omitempty"`
}

type sessionCreated struct {
	SessionID     string               `json:"sessionId"`
	CWD           string               `json:"cwd"`
	Configuration requestConfiguration `json:"configuration"`
}

type configurationChanged struct {
	Configuration requestConfiguration `json:"configuration"`
}

type userMessageRecord struct {
	TurnID    string             `json:"turnId"`
	MessageID string             `json:"messageId"`
	Content   []acp.ContentBlock `json:"content"`
}

type storedToolResult struct {
	CallID           string            `json:"callId"`
	Content          string            `json:"content"`
	Failed           bool              `json:"failed,omitempty"`
	ApprovalDecision approvalDecision  `json:"approvalDecision,omitempty"`
	Delegation       *delegationRecord `json:"delegation,omitempty"`
}

type delegationRecord struct {
	Prompt string             `json:"prompt"`
	Answer string             `json:"answer,omitempty"`
	Calls  []delegatedCall    `json:"calls,omitempty"`
	Usage  []openrouter.Usage `json:"usage,omitempty"`
}

type delegatedCall struct {
	CallID           string           `json:"callId"`
	Name             string           `json:"name"`
	Arguments        json.RawMessage  `json:"arguments"`
	Content          string           `json:"content"`
	Failed           bool             `json:"failed,omitempty"`
	ApprovalDecision approvalDecision `json:"approvalDecision,omitempty"`
}

type modelExchangeRecord struct {
	TurnID           string                `json:"turnId"`
	AnswerID         string                `json:"answerId"`
	ThoughtID        string                `json:"thoughtId"`
	Text             string                `json:"text,omitempty"`
	Reasoning        string                `json:"reasoning,omitempty"`
	ReasoningDetails [][]byte              `json:"reasoningDetails,omitempty"`
	FinishReason     string                `json:"finishReason"`
	Usage            *openrouter.Usage     `json:"usage,omitempty"`
	ToolCalls        []openrouter.ToolCall `json:"toolCalls,omitempty"`
	ToolResults      []storedToolResult    `json:"toolResults,omitempty"`
}

type turnFinishedRecord struct {
	TurnID     string         `json:"turnId"`
	Kind       string         `json:"kind"`
	StopReason acp.StopReason `json:"stopReason,omitempty"`
	MessageID  string         `json:"messageId,omitempty"`
	Message    string         `json:"message,omitempty"`
}

type durableState struct {
	id              string
	cwd             string
	createdAt       time.Time
	updatedAt       time.Time
	sequence        uint64
	configuration   requestConfiguration
	history         []openrouter.Message
	usage           turnUsage
	cost            float64
	records         []sessionRecord
	messageIDs      map[string]struct{}
	toolCallIDs     map[string]struct{}
	openTurn        string
	openTurnHistory int
	title           string
}

func newRecord(sequence uint64, kind string, value any) (sessionRecord, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return sessionRecord{}, fmt.Errorf("encode %s record: %w", kind, err)
	}
	return sessionRecord{
		Version:  recordVersion,
		Sequence: sequence,
		Type:     kind,
		At:       time.Now().UTC(),
		Data:     data,
	}, nil
}

func foldRecords(records []sessionRecord) (durableState, error) {
	var state durableState
	for index := range records {
		if err := state.apply(records[index]); err != nil {
			return durableState{}, fmt.Errorf("record %d: %w", index+1, err)
		}
	}
	return state, nil
}

func (s durableState) clone() durableState {
	s.history = cloneMessages(s.history)
	s.records = append([]sessionRecord(nil), s.records...)
	s.configuration.Tools = cloneTools(s.configuration.Tools)
	s.configuration.Subagent.Tools = cloneTools(s.configuration.Subagent.Tools)
	s.configuration.ToolKinds = cloneToolKinds(s.configuration.ToolKinds)
	s.configuration.Settings = cloneResolved(s.configuration.Settings)
	messageIDs := s.messageIDs
	s.messageIDs = make(map[string]struct{}, len(messageIDs))
	for id := range messageIDs {
		s.messageIDs[id] = struct{}{}
	}
	toolCallIDs := s.toolCallIDs
	s.toolCallIDs = make(map[string]struct{}, len(toolCallIDs))
	for id := range toolCallIDs {
		s.toolCallIDs[id] = struct{}{}
	}
	return s
}

func (s *durableState) apply(record sessionRecord) error {
	if record.Version != recordVersion {
		return fmt.Errorf("unsupported record version %d", record.Version)
	}
	if record.Sequence != s.sequence+1 {
		return fmt.Errorf("sequence %d follows %d", record.Sequence, s.sequence)
	}
	if record.At.IsZero() {
		return errors.New("record timestamp is required")
	}
	if s.sequence == 0 && record.Type != recordSessionCreated {
		return errors.New("first record must create the session")
	}

	switch record.Type {
	case recordSessionCreated:
		if s.sequence != 0 {
			return errors.New("session was created twice")
		}
		var value sessionCreated
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if !validSessionID(value.SessionID) {
			return errors.New("invalid session ID")
		}
		if !filepath.IsAbs(value.CWD) {
			return errors.New("session cwd must be absolute")
		}
		if err := validateConfiguration(value.Configuration); err != nil {
			return err
		}
		s.id = value.SessionID
		s.cwd = value.CWD
		s.createdAt = record.At
		s.configuration = cloneConfiguration(value.Configuration)
		s.messageIDs = make(map[string]struct{})
		s.toolCallIDs = make(map[string]struct{})
	case recordConfigChanged:
		if s.openTurn != "" {
			return errors.New("configuration changed during a turn")
		}
		var value configurationChanged
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if err := validateConfiguration(value.Configuration); err != nil {
			return err
		}
		s.configuration = cloneConfiguration(value.Configuration)
	case recordUserMessage:
		var value userMessageRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if value.TurnID == "" || value.MessageID == "" || len(value.Content) == 0 {
			return errors.New("user message identities and content are required")
		}
		if _, exists := s.messageIDs[value.MessageID]; exists {
			return fmt.Errorf("duplicate message ID %q", value.MessageID)
		}
		if s.openTurn == "" {
			s.openTurn = value.TurnID
			s.openTurnHistory = len(s.history)
		} else if s.openTurn != value.TurnID {
			return errors.New("user message belongs to another open turn")
		}
		message, err := promptMessage(value.Content)
		if err != nil {
			return err
		}
		s.history = append(s.history, message)
		s.messageIDs[value.MessageID] = struct{}{}
		if s.title == "" {
			s.title = sessionTitle(value.Content)
		}
	case recordModelExchange:
		var value modelExchangeRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if s.openTurn == "" || value.TurnID != s.openTurn {
			return errors.New("model exchange has no matching open turn")
		}
		if value.AnswerID == "" || value.ThoughtID == "" ||
			value.AnswerID == value.ThoughtID {
			return errors.New("model exchange identities are invalid")
		}
		if _, exists := s.messageIDs[value.AnswerID]; exists {
			return fmt.Errorf("duplicate message ID %q", value.AnswerID)
		}
		if _, exists := s.messageIDs[value.ThoughtID]; exists {
			return fmt.Errorf("duplicate message ID %q", value.ThoughtID)
		}
		if len(value.ToolCalls) != len(value.ToolResults) {
			return errors.New("tool calls and results must be one complete group")
		}
		for _, detail := range value.ReasoningDetails {
			if !json.Valid(detail) {
				return errors.New("reasoning detail is not valid JSON")
			}
		}
		assistant := openrouter.Message{
			Role:             openrouter.RoleAssistant,
			ToolCalls:        append([]openrouter.ToolCall(nil), value.ToolCalls...),
			ReasoningDetails: rawMessages(value.ReasoningDetails),
		}
		if value.Text != "" {
			assistant.Content = []openrouter.ContentBlock{{Type: "text", Text: value.Text}}
		}
		if len(value.ToolCalls) > 0 {
			s.history = append(s.history, assistant)
			for index, call := range value.ToolCalls {
				result := value.ToolResults[index]
				if call.ID == "" || result.CallID != call.ID {
					return errors.New("tool result order does not match tool calls")
				}
				if call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
					return errors.New("tool call name and JSON arguments are required")
				}
				if _, exists := s.toolCallIDs[call.ID]; exists {
					return fmt.Errorf("duplicate tool call ID %q", call.ID)
				}
				s.toolCallIDs[call.ID] = struct{}{}
				if result.Delegation != nil {
					for _, child := range result.Delegation.Calls {
						if child.CallID == "" || child.Name == "" ||
							!json.Valid(child.Arguments) {
							return errors.New("delegated tool call identity, name, and JSON arguments are required")
						}
						if _, exists := s.toolCallIDs[child.CallID]; exists {
							return fmt.Errorf("duplicate tool call ID %q", child.CallID)
						}
						s.toolCallIDs[child.CallID] = struct{}{}
					}
					for index := range result.Delegation.Usage {
						s.addUsage(&result.Delegation.Usage[index])
					}
				}
				s.history = append(s.history, openrouter.Message{
					Role:       openrouter.RoleTool,
					ToolCallID: call.ID,
					Content: []openrouter.ContentBlock{{
						Type: "text",
						Text: result.Content,
					}},
				})
			}
		} else if stopReason(value.FinishReason) != acp.StopReasonRefusal {
			s.history = append(s.history, assistant)
		}
		s.messageIDs[value.AnswerID] = struct{}{}
		s.messageIDs[value.ThoughtID] = struct{}{}
		s.addUsage(value.Usage)
	case recordTurnFinished:
		var value turnFinishedRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if s.openTurn == "" || value.TurnID != s.openTurn {
			return errors.New("turn outcome has no matching open turn")
		}
		if value.Kind == "refusal" {
			s.history = s.history[:s.openTurnHistory]
		}
		switch value.Kind {
		case "completed", "cancelled", "interrupted", "failed", "refusal", "request_limit":
		default:
			return fmt.Errorf("unknown turn outcome %q", value.Kind)
		}
		if value.MessageID != "" {
			if _, exists := s.messageIDs[value.MessageID]; exists {
				return fmt.Errorf("duplicate message ID %q", value.MessageID)
			}
			s.messageIDs[value.MessageID] = struct{}{}
		}
		s.openTurn = ""
		s.openTurnHistory = 0
	default:
		return fmt.Errorf("unsupported record type %q", record.Type)
	}

	s.sequence = record.Sequence
	s.updatedAt = record.At
	s.records = append(s.records, record)
	return nil
}

func (s *durableState) addUsage(current *openrouter.Usage) {
	if current == nil {
		return
	}
	s.usage.seen = true
	s.usage.input += uint64(current.PromptTokens)
	s.usage.output += uint64(current.CompletionTokens)
	if current.PromptTokensDetails != nil {
		s.usage.cachedRead += uint64(current.PromptTokensDetails.CachedTokens)
		s.usage.cachedWrite += uint64(current.PromptTokensDetails.CacheWriteTokens)
	}
	if current.CompletionTokensDetails != nil {
		s.usage.thought += uint64(current.CompletionTokensDetails.ReasoningTokens)
	}
	s.cost += current.Cost
}

func combinedUsage(values ...*openrouter.Usage) *openrouter.Usage {
	var result openrouter.Usage
	var seen bool
	for _, value := range values {
		if value == nil {
			continue
		}
		seen = true
		result.PromptTokens += value.PromptTokens
		result.CompletionTokens += value.CompletionTokens
		result.TotalTokens += value.TotalTokens
		result.Cost += value.Cost
		if value.PromptTokensDetails != nil {
			if result.PromptTokensDetails == nil {
				result.PromptTokensDetails = &openrouter.PromptTokensDetails{}
			}
			result.PromptTokensDetails.CachedTokens += value.PromptTokensDetails.CachedTokens
			result.PromptTokensDetails.CacheWriteTokens += value.PromptTokensDetails.CacheWriteTokens
		}
		if value.CompletionTokensDetails != nil {
			if result.CompletionTokensDetails == nil {
				result.CompletionTokensDetails = &openrouter.CompletionTokensDetails{}
			}
			result.CompletionTokensDetails.ReasoningTokens += value.CompletionTokensDetails.ReasoningTokens
		}
	}
	if !seen {
		return nil
	}
	return &result
}

func (a *Agent) replay(s durableState) ([]any, error) {
	updates := make([]any, 0, len(s.records))
	var cost float64
	var configuration requestConfiguration
	for _, record := range s.records {
		switch record.Type {
		case recordSessionCreated:
			var value sessionCreated
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			configuration = value.Configuration
		case recordConfigChanged:
			var value configurationChanged
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			configuration = value.Configuration
		case recordUserMessage:
			var value userMessageRecord
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			for index, content := range value.Content {
				updates = append(updates, acp.UserMessageChunk{
					SessionUpdate: "user_message_chunk",
					Content:       replayContent(content, index),
					MessageID:     value.MessageID,
				})
			}
		case recordModelExchange:
			var value modelExchangeRecord
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			if value.Reasoning != "" {
				updates = append(updates, acp.AgentThoughtChunk{
					SessionUpdate: "agent_thought_chunk",
					Content:       acp.ContentBlock{Type: "text", Text: value.Reasoning},
					MessageID:     value.ThoughtID,
				})
			}
			if value.Text != "" {
				updates = append(updates, acp.AgentMessageChunk{
					SessionUpdate: "agent_message_chunk",
					Content:       acp.ContentBlock{Type: "text", Text: value.Text},
					MessageID:     value.AnswerID,
				})
			}
			for index, call := range value.ToolCalls {
				result := value.ToolResults[index]
				status := acp.ToolCallStatusCompleted
				if result.Failed {
					status = acp.ToolCallStatusFailed
				}
				parentMeta := acp.Metadata(nil)
				if result.Delegation != nil || a.toolDelegates(call.Function.Name) {
					parentMeta = acp.Metadata{acp.MetaSubagent: true}
				}
				updates = append(updates, acp.ToolCall{
					SessionUpdate: "tool_call",
					ToolCallID:    call.ID,
					Title:         a.toolTitle(call.Function.Name, json.RawMessage(call.Function.Arguments)),
					Name:          call.Function.Name,
					Kind:          configuration.ToolKinds[call.Function.Name],
					Status:        acp.ToolCallStatusPending,
					RawInput:      json.RawMessage(call.Function.Arguments),
					Meta:          parentMeta,
				})
				if result.Delegation != nil {
					for _, child := range result.Delegation.Calls {
						childStatus := acp.ToolCallStatusCompleted
						if child.Failed {
							childStatus = acp.ToolCallStatusFailed
						}
						meta := acp.Metadata{acp.MetaParentToolCallID: call.ID}
						updates = append(updates,
							acp.ToolCall{
								SessionUpdate: "tool_call",
								ToolCallID:    child.CallID,
								Title:         a.toolTitle(child.Name, child.Arguments),
								Name:          child.Name,
								Kind:          configuration.ToolKinds[child.Name],
								Status:        acp.ToolCallStatusPending,
								RawInput:      append(json.RawMessage(nil), child.Arguments...),
								Meta:          meta,
							},
							acp.ToolCallUpdate{
								SessionUpdate: "tool_call_update",
								ToolCallID:    child.CallID,
								Status:        childStatus,
								Content: []acp.ToolCallContent{{
									Type: "content",
									Content: acp.ContentBlock{
										Type: "text",
										Text: outputTail(child.Content),
									},
								}},
								Meta: meta,
							},
						)
					}
				}
				updates = append(updates, acp.ToolCallUpdate{
					SessionUpdate: "tool_call_update",
					ToolCallID:    call.ID,
					Status:        status,
					Content: []acp.ToolCallContent{{
						Type: "content",
						Content: acp.ContentBlock{
							Type: "text",
							Text: outputTail(result.Content),
						},
					}},
					Meta: parentMeta,
				})
			}
			exchangeUsage := value.Usage
			for _, result := range value.ToolResults {
				if result.Delegation != nil {
					usages := []*openrouter.Usage{exchangeUsage}
					for index := range result.Delegation.Usage {
						usages = append(usages, &result.Delegation.Usage[index])
					}
					exchangeUsage = combinedUsage(usages...)
				}
			}
			if exchangeUsage != nil {
				cost += exchangeUsage.Cost
				if configuration.ContextWindow > 0 {
					updates = append(updates, acp.UsageUpdate{
						SessionUpdate: "usage_update",
						Used:          uint64(exchangeUsage.TotalTokens),
						Size:          uint64(configuration.ContextWindow),
						Cost:          &acp.Cost{Amount: cost, Currency: "USD"},
					})
				}
			}
		case recordTurnFinished:
			var value turnFinishedRecord
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			if update := outcomeUpdate(value); update != nil {
				updates = append(updates, update)
			}
		}
	}
	return updates, nil
}

func outcomeUpdate(value turnFinishedRecord) any {
	if value.Kind == "completed" || value.Kind == "refusal" || value.Kind == "request_limit" {
		return nil
	}
	text := value.Message
	if text == "" {
		switch value.Kind {
		case "cancelled":
			text = "Prompt cancelled."
		case "interrupted":
			text = "Prompt interrupted by a runtime restart."
		default:
			text = "Ox could not complete the prompt."
		}
	}
	return acp.AgentMessageChunk{
		SessionUpdate: "agent_message_chunk",
		Content:       acp.ContentBlock{Type: "text", Text: text},
		MessageID:     value.MessageID,
		Meta:          acp.Metadata{acp.MetaOutcome: value.Kind},
	}
}

func decodeRecord(data json.RawMessage, target any) error {
	if len(data) == 0 {
		return errors.New("record data is required")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode record data: %w", err)
	}
	return nil
}

func cloneConfiguration(value requestConfiguration) requestConfiguration {
	value.Settings = cloneResolved(value.Settings)
	value.Tools = cloneTools(value.Tools)
	value.Subagent.Tools = cloneTools(value.Subagent.Tools)
	value.ToolKinds = cloneToolKinds(value.ToolKinds)
	return value
}

func cloneToolKinds(values map[string]acp.ToolKind) map[string]acp.ToolKind {
	if values == nil {
		return nil
	}
	cloned := make(map[string]acp.ToolKind, len(values))
	for name, kind := range values {
		cloned[name] = kind
	}
	return cloned
}

func cloneResolved(value settings.Resolved) settings.Resolved {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var cloned settings.Resolved
	if err := json.Unmarshal(raw, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func cloneTools(values []openrouter.Tool) []openrouter.Tool {
	cloned := slices.Clone(values)
	for index := range cloned {
		cloned[index].Function.Parameters = slices.Clone(cloned[index].Function.Parameters)
	}
	return cloned
}

func opaqueMessages(values []json.RawMessage) [][]byte {
	cloned := make([][]byte, len(values))
	for index := range values {
		cloned[index] = append([]byte(nil), values[index]...)
	}
	return cloned
}

func rawMessages(values [][]byte) []json.RawMessage {
	cloned := make([]json.RawMessage, len(values))
	for index := range values {
		cloned[index] = append(json.RawMessage(nil), values[index]...)
	}
	return cloned
}

func sameRequestConfiguration(left, right requestConfiguration) bool {
	left.Settings.ModelSource = ""
	right.Settings.ModelSource = ""
	return reflect.DeepEqual(left, right)
}

func validateConfiguration(value requestConfiguration) error {
	if value.Settings.Model == "" {
		return errors.New("configuration model is required")
	}
	if value.ContextWindow < 0 {
		return errors.New("configuration context window is invalid")
	}
	names := make(map[string]struct{}, len(value.Tools))
	for _, tool := range value.Tools {
		if tool.Type != "function" || tool.Function.Name == "" ||
			(len(tool.Function.Parameters) > 0 && !json.Valid(tool.Function.Parameters)) {
			return errors.New("configuration contains an invalid tool declaration")
		}
		if _, exists := names[tool.Function.Name]; exists {
			return fmt.Errorf("duplicate configured tool %q", tool.Function.Name)
		}
		names[tool.Function.Name] = struct{}{}
	}
	subagentNames := make(map[string]struct{}, len(value.Subagent.Tools))
	for _, tool := range value.Subagent.Tools {
		if tool.Type != "function" || tool.Function.Name == "" ||
			(len(tool.Function.Parameters) > 0 && !json.Valid(tool.Function.Parameters)) {
			return errors.New("configuration contains an invalid subagent tool declaration")
		}
		if _, exists := subagentNames[tool.Function.Name]; exists {
			return fmt.Errorf("duplicate configured subagent tool %q", tool.Function.Name)
		}
		subagentNames[tool.Function.Name] = struct{}{}
	}
	for name, kind := range value.ToolKinds {
		if _, exists := names[name]; !exists {
			return fmt.Errorf("tool kind names unknown tool %q", name)
		}
		if kind != acp.ToolKindRead && kind != acp.ToolKindSearch &&
			kind != acp.ToolKindEdit && kind != acp.ToolKindExecute &&
			kind != acp.ToolKindOther {
			return fmt.Errorf("configured tool %q has invalid kind %q", name, kind)
		}
	}
	return nil
}

func sessionTitle(blocks []acp.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	title := strings.Join(parts, " ")
	const limit = 80
	if utf8.RuneCountInString(title) <= limit {
		return title
	}
	runes := []rune(title)
	return string(runes[:limit-1]) + "…"
}

func replayContent(content acp.ContentBlock, index int) acp.ContentBlock {
	content.Meta = cloneMetadata(content.Meta)
	content.Meta[acp.MetaBlockIndex] = index
	return content
}

func cloneMetadata(value acp.Metadata) acp.Metadata {
	cloned := make(acp.Metadata, len(value)+1)
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}
