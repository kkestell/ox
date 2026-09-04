package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
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
	recordVersion     = 1
	checkpointVersion = 2

	recordSessionCreated  = "session_created"
	recordConfigChanged   = "request_configuration_changed"
	recordUserMessage     = "user_message"
	recordExchangePaused  = "suspended_model_exchange"
	recordPermissionOpen  = "permission_requested"
	recordPermissionRetry = "permission_reissued"
	recordPermissionDone  = "permission_decided"
	recordModelExchange   = "completed_model_exchange"
	recordTurnFinished    = "turn_finished"
	recordCheckpoint      = "checkpoint"
)

type sessionRecord struct {
	Version  int             `json:"version"`
	Sequence uint64          `json:"sequence"`
	Type     string          `json:"type"`
	At       time.Time       `json:"at"`
	Data     json.RawMessage `json:"data"`
}

type requestConfiguration struct {
	Settings             settings.Resolved       `json:"settings"`
	ContextWindow        int                     `json:"contextWindow"`
	SystemPrompt         string                  `json:"systemPrompt,omitempty"`
	Tools                []openrouter.Tool       `json:"tools,omitempty"`
	ToolKinds            map[string]acp.ToolKind `json:"toolKinds,omitempty"`
	Subagent             subagentConfiguration   `json:"subagent,omitempty"`
	ExecutorCapabilities executorCapabilities    `json:"executorCapabilities"`
}

type executorCapabilities struct {
	FileSystemRead  bool `json:"fileSystemRead"`
	FileSystemWrite bool `json:"fileSystemWrite"`
	Terminal        bool `json:"terminal"`
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
	Target           string            `json:"target,omitempty"`
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
	Target           string           `json:"target,omitempty"`
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

type suspendedModelExchangeRecord struct {
	TurnID           string                     `json:"turnId"`
	AnswerID         string                     `json:"answerId"`
	ThoughtID        string                     `json:"thoughtId"`
	Text             string                     `json:"text,omitempty"`
	Reasoning        string                     `json:"reasoning,omitempty"`
	ReasoningDetails [][]byte                   `json:"reasoningDetails,omitempty"`
	FinishReason     string                     `json:"finishReason"`
	Usage            *openrouter.Usage          `json:"usage,omitempty"`
	ToolCalls        []openrouter.ToolCall      `json:"toolCalls"`
	ToolTargets      map[string]string          `json:"toolTargets,omitempty"`
	RequestCount     int                        `json:"requestCount"`
	Decisions        []storedPermissionDecision `json:"decisions,omitempty"`
	Pending          *pendingPermissionRecord   `json:"pending,omitempty"`
}

type storedPermissionDecision struct {
	CallID   string           `json:"callId"`
	Decision approvalDecision `json:"decision"`
	Rule     string           `json:"rule,omitempty"`
}

type pendingPermissionRecord struct {
	CallID     string                       `json:"callId"`
	Generation uint64                       `json:"generation"`
	Request    acp.RequestPermissionRequest `json:"request"`
}

type permissionRequestedRecord struct {
	TurnID  string                  `json:"turnId"`
	Pending pendingPermissionRecord `json:"pending"`
}

type permissionReissuedRecord struct {
	TurnID     string `json:"turnId"`
	CallID     string `json:"callId"`
	Generation uint64 `json:"generation"`
}

type permissionDecidedRecord struct {
	TurnID     string           `json:"turnId"`
	CallID     string           `json:"callId"`
	Generation uint64           `json:"generation"`
	Decision   approvalDecision `json:"decision"`
	Rule       string           `json:"rule,omitempty"`
}

type turnFinishedRecord struct {
	TurnID     string         `json:"turnId"`
	Kind       string         `json:"kind"`
	StopReason acp.StopReason `json:"stopReason,omitempty"`
	MessageID  string         `json:"messageId,omitempty"`
	Message    string         `json:"message,omitempty"`
}

type checkpointRecord struct {
	Version int                  `json:"version"`
	State   checkpointProjection `json:"state"`
}

type checkpointProjection struct {
	SessionID     string               `json:"sessionId"`
	CWD           string               `json:"cwd"`
	CreatedAt     time.Time            `json:"createdAt"`
	UpdatedAt     time.Time            `json:"updatedAt"`
	Sequence      uint64               `json:"sequence"`
	Configuration requestConfiguration `json:"configuration"`
	History       []openrouter.Message `json:"history,omitempty"`
	Usage         checkpointUsage      `json:"usage"`
	Cost          float64              `json:"cost"`
	MessageIDs    []string             `json:"messageIds,omitempty"`
	ToolCallIDs   []string             `json:"toolCallIds,omitempty"`
	ChangedFiles  []string             `json:"changedFiles,omitempty"`
	OpenTurn      string               `json:"openTurn,omitempty"`
	Title         string               `json:"title,omitempty"`
}

type checkpointUsage struct {
	Seen        bool   `json:"seen"`
	Input       uint64 `json:"input"`
	Output      uint64 `json:"output"`
	Thought     uint64 `json:"thought"`
	CachedRead  uint64 `json:"cachedRead"`
	CachedWrite uint64 `json:"cachedWrite"`
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
	changedFiles    map[string]struct{}
	openTurn        string
	openTurnHistory int
	suspended       *suspendedModelExchangeRecord
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
	var checkpoint durableState
	start := 0
	for index := range records {
		record := records[index]
		if err := validateRecordEnvelope(record, uint64(index)); err != nil {
			return durableState{}, fmt.Errorf("record %d: %w", index+1, err)
		}
		if record.Type != recordCheckpoint {
			continue
		}
		if index == 0 || records[index-1].Type != recordTurnFinished {
			return durableState{}, fmt.Errorf("record %d: checkpoint does not follow a finished turn", index+1)
		}
		restored, err := restoreCheckpoint(record, records[index-1])
		if err != nil {
			return durableState{}, fmt.Errorf("record %d: %w", index+1, err)
		}
		checkpoint = restored
		start = index + 1
	}

	state := checkpoint
	for index := start; index < len(records); index++ {
		if err := state.apply(records[index]); err != nil {
			return durableState{}, fmt.Errorf("record %d: %w", index+1, err)
		}
	}
	state.records = append([]sessionRecord(nil), records...)
	return state, nil
}

func validateRecordEnvelope(record sessionRecord, previous uint64) error {
	if record.Version != recordVersion {
		return fmt.Errorf("unsupported record version %d", record.Version)
	}
	if record.Sequence != previous+1 {
		return fmt.Errorf("sequence %d follows %d", record.Sequence, previous)
	}
	if record.At.IsZero() {
		return errors.New("record timestamp is required")
	}
	if len(record.Data) == 0 || !json.Valid(record.Data) {
		return errors.New("record data must be valid JSON")
	}
	if previous == 0 && record.Type != recordSessionCreated {
		return errors.New("first record must create the session")
	}
	switch record.Type {
	case recordSessionCreated, recordConfigChanged, recordUserMessage,
		recordExchangePaused, recordPermissionOpen, recordPermissionRetry,
		recordPermissionDone, recordModelExchange, recordTurnFinished, recordCheckpoint:
		return nil
	default:
		return fmt.Errorf("unsupported record type %q", record.Type)
	}
}

func newCheckpointRecord(state durableState) (sessionRecord, error) {
	if state.openTurn != "" {
		return sessionRecord{}, errors.New("checkpoint cannot represent an open turn")
	}
	payload := checkpointRecord{
		Version: checkpointVersion,
		State: checkpointProjection{
			SessionID:     state.id,
			CWD:           state.cwd,
			CreatedAt:     state.createdAt,
			UpdatedAt:     state.updatedAt,
			Sequence:      state.sequence,
			Configuration: cloneConfiguration(state.configuration),
			History:       cloneMessages(state.history),
			Usage: checkpointUsage{
				Seen:        state.usage.seen,
				Input:       state.usage.input,
				Output:      state.usage.output,
				Thought:     state.usage.thought,
				CachedRead:  state.usage.cachedRead,
				CachedWrite: state.usage.cachedWrite,
			},
			Cost:         state.cost,
			MessageIDs:   sortedIdentitySet(state.messageIDs),
			ToolCallIDs:  sortedIdentitySet(state.toolCallIDs),
			ChangedFiles: sortedIdentitySet(state.changedFiles),
			Title:        state.title,
		},
	}
	return newRecord(state.sequence+1, recordCheckpoint, payload)
}

func restoreCheckpoint(record, previous sessionRecord) (durableState, error) {
	var value checkpointRecord
	if err := decodeRecord(record.Data, &value); err != nil {
		return durableState{}, err
	}
	if value.Version != checkpointVersion {
		return durableState{}, fmt.Errorf("unsupported checkpoint version %d", value.Version)
	}
	projection := value.State
	if projection.OpenTurn != "" {
		return durableState{}, errors.New("checkpoint represents an open turn")
	}
	if projection.Sequence != record.Sequence-1 {
		return durableState{}, fmt.Errorf(
			"checkpoint sequence %d does not precede record %d",
			projection.Sequence,
			record.Sequence,
		)
	}
	if !projection.UpdatedAt.Equal(previous.At) {
		return durableState{}, errors.New("checkpoint timestamp does not match the preceding record")
	}
	if record.At.Before(projection.UpdatedAt) {
		return durableState{}, errors.New("checkpoint predates its projection")
	}
	if !validSessionID(projection.SessionID) {
		return durableState{}, errors.New("invalid checkpoint session ID")
	}
	if !filepath.IsAbs(projection.CWD) {
		return durableState{}, errors.New("checkpoint cwd must be absolute")
	}
	if projection.CreatedAt.IsZero() || projection.UpdatedAt.IsZero() ||
		projection.UpdatedAt.Before(projection.CreatedAt) {
		return durableState{}, errors.New("checkpoint timestamps are invalid")
	}
	if err := validateConfiguration(projection.Configuration); err != nil {
		return durableState{}, err
	}
	messageIDs, err := identitySet(projection.MessageIDs)
	if err != nil {
		return durableState{}, fmt.Errorf("checkpoint message IDs: %w", err)
	}
	toolCallIDs, err := identitySet(projection.ToolCallIDs)
	if err != nil {
		return durableState{}, fmt.Errorf("checkpoint tool call IDs: %w", err)
	}
	if !slices.IsSorted(projection.ChangedFiles) {
		return durableState{}, errors.New("checkpoint changed files are not sorted")
	}
	changedFiles, err := targetSet(projection.ChangedFiles)
	if err != nil {
		return durableState{}, fmt.Errorf("checkpoint changed files: %w", err)
	}
	return durableState{
		id:            projection.SessionID,
		cwd:           projection.CWD,
		createdAt:     projection.CreatedAt,
		updatedAt:     projection.UpdatedAt,
		sequence:      record.Sequence,
		configuration: cloneConfiguration(projection.Configuration),
		history:       cloneMessages(projection.History),
		usage: turnUsage{
			seen:        projection.Usage.Seen,
			input:       projection.Usage.Input,
			output:      projection.Usage.Output,
			thought:     projection.Usage.Thought,
			cachedRead:  projection.Usage.CachedRead,
			cachedWrite: projection.Usage.CachedWrite,
		},
		cost:         projection.Cost,
		messageIDs:   messageIDs,
		toolCallIDs:  toolCallIDs,
		changedFiles: changedFiles,
		title:        projection.Title,
	}, nil
}

func sortedIdentitySet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func identitySet(values []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return nil, errors.New("empty identity")
		}
		if _, exists := result[value]; exists {
			return nil, fmt.Errorf("duplicate identity %q", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func targetSet(values []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validStoredTarget(value) {
			return nil, fmt.Errorf("invalid target %q", value)
		}
		if _, exists := result[value]; exists {
			return nil, fmt.Errorf("duplicate target %q", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func validStoredTarget(value string) bool {
	return value != "" && value != "." && !path.IsAbs(value) &&
		path.Clean(value) == value && value != ".." &&
		!strings.HasPrefix(value, "../") && !strings.Contains(value, `\`)
}

func (s durableState) clone() durableState {
	s.history = cloneMessages(s.history)
	s.records = append([]sessionRecord(nil), s.records...)
	s.configuration = cloneConfiguration(s.configuration)
	s.suspended = cloneSuspendedExchange(s.suspended)
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
	changedFiles := s.changedFiles
	s.changedFiles = make(map[string]struct{}, len(changedFiles))
	for path := range changedFiles {
		s.changedFiles[path] = struct{}{}
	}
	return s
}

func (s *durableState) apply(record sessionRecord) error {
	if err := validateRecordEnvelope(record, s.sequence); err != nil {
		return err
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
		s.changedFiles = make(map[string]struct{})
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
		if s.suspended != nil {
			return errors.New("user message arrived while a model exchange was suspended")
		}
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
	case recordExchangePaused:
		if s.openTurn == "" {
			return errors.New("suspended model exchange has no open turn")
		}
		if s.suspended != nil {
			return errors.New("model exchange was suspended twice")
		}
		var value suspendedModelExchangeRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if err := s.validateSuspendedExchange(value); err != nil {
			return err
		}
		s.suspended = cloneSuspendedExchange(&value)
	case recordPermissionOpen:
		var value permissionRequestedRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if s.suspended == nil || value.TurnID != s.openTurn || value.TurnID != s.suspended.TurnID {
			return errors.New("permission request has no matching suspended exchange")
		}
		if s.suspended.Pending != nil {
			return errors.New("suspended exchange already has a pending permission request")
		}
		if err := s.validatePendingPermission(value.Pending); err != nil {
			return err
		}
		pending := value.Pending
		s.suspended.Pending = &pending
	case recordPermissionRetry:
		var value permissionReissuedRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if s.suspended == nil || s.suspended.Pending == nil ||
			value.TurnID != s.openTurn || value.TurnID != s.suspended.TurnID ||
			value.CallID != s.suspended.Pending.CallID {
			return errors.New("reissued permission does not match the pending request")
		}
		if value.Generation != s.suspended.Pending.Generation+1 {
			return errors.New("reissued permission generation is not consecutive")
		}
		s.suspended.Pending.Generation = value.Generation
	case recordPermissionDone:
		var value permissionDecidedRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if s.suspended == nil || s.suspended.Pending == nil ||
			value.TurnID != s.openTurn || value.TurnID != s.suspended.TurnID ||
			value.CallID != s.suspended.Pending.CallID {
			return errors.New("permission decision does not match the pending request")
		}
		if value.Generation != s.suspended.Pending.Generation {
			return errors.New("permission decision generation is stale")
		}
		if !validApprovalDecision(value.Decision) {
			return fmt.Errorf("invalid permission decision %q", value.Decision)
		}
		if value.Decision != decisionAllowAlways && value.Rule != "" {
			return errors.New("only an allow-always decision may carry a rule")
		}
		if s.suspended.decision(value.CallID) != nil {
			return fmt.Errorf("duplicate permission decision for tool call %q", value.CallID)
		}
		s.suspended.Decisions = append(s.suspended.Decisions, storedPermissionDecision{
			CallID: value.CallID, Decision: value.Decision, Rule: value.Rule,
		})
		s.suspended.Pending = nil
	case recordModelExchange:
		var value modelExchangeRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if s.suspended != nil {
			if err := validateCompletedSuspension(*s.suspended, value); err != nil {
				return err
			}
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
				if result.Target != "" &&
					(s.configuration.ToolKinds[call.Function.Name] != acp.ToolKindEdit ||
						!validStoredTarget(result.Target)) {
					return fmt.Errorf("tool call %q has invalid target %q", call.ID, result.Target)
				}
				if _, exists := s.toolCallIDs[call.ID]; exists {
					return fmt.Errorf("duplicate tool call ID %q", call.ID)
				}
				s.toolCallIDs[call.ID] = struct{}{}
				if !result.Failed && result.Target != "" {
					s.changedFiles[result.Target] = struct{}{}
				}
				if result.Delegation != nil {
					for _, child := range result.Delegation.Calls {
						if child.CallID == "" || child.Name == "" ||
							!json.Valid(child.Arguments) {
							return errors.New("delegated tool call identity, name, and JSON arguments are required")
						}
						if _, exists := s.toolCallIDs[child.CallID]; exists {
							return fmt.Errorf("duplicate tool call ID %q", child.CallID)
						}
						if child.Target != "" &&
							(s.configuration.ToolKinds[child.Name] != acp.ToolKindEdit ||
								!validStoredTarget(child.Target)) {
							return fmt.Errorf(
								"delegated tool call %q has invalid target %q",
								child.CallID,
								child.Target,
							)
						}
						s.toolCallIDs[child.CallID] = struct{}{}
						if !child.Failed && child.Target != "" {
							s.changedFiles[child.Target] = struct{}{}
						}
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
		s.suspended = nil
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
		if s.suspended != nil {
			if value.Kind != "cancelled" &&
				(value.Kind != "interrupted" || s.suspended.Pending != nil) {
				return errors.New("only cancellation may finish a suspended exchange with pending permission")
			}
			s.suspended = nil
		}
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

func (s *durableState) validateSuspendedExchange(value suspendedModelExchangeRecord) error {
	if value.TurnID != s.openTurn {
		return errors.New("suspended model exchange belongs to another turn")
	}
	if value.AnswerID == "" || value.ThoughtID == "" || value.AnswerID == value.ThoughtID {
		return errors.New("suspended model exchange identities are invalid")
	}
	if _, exists := s.messageIDs[value.AnswerID]; exists {
		return fmt.Errorf("duplicate message ID %q", value.AnswerID)
	}
	if _, exists := s.messageIDs[value.ThoughtID]; exists {
		return fmt.Errorf("duplicate message ID %q", value.ThoughtID)
	}
	if value.FinishReason != "tool_calls" || len(value.ToolCalls) == 0 {
		return errors.New("suspended model exchange requires tool calls")
	}
	if value.RequestCount < 1 || value.RequestCount > maxTurnRequests {
		return errors.New("suspended model exchange request count is invalid")
	}
	if len(value.Decisions) != 0 || value.Pending != nil {
		return errors.New("new suspended model exchange cannot contain permission progress")
	}
	for _, detail := range value.ReasoningDetails {
		if !json.Valid(detail) {
			return errors.New("reasoning detail is not valid JSON")
		}
	}
	seen := make(map[string]struct{}, len(value.ToolCalls))
	names := make(map[string]string, len(value.ToolCalls))
	for _, call := range value.ToolCalls {
		if call.ID == "" || call.Function.Name == "" ||
			!json.Valid([]byte(call.Function.Arguments)) {
			return errors.New("suspended tool call identity, name, and JSON arguments are required")
		}
		if _, exists := s.toolCallIDs[call.ID]; exists {
			return fmt.Errorf("duplicate tool call ID %q", call.ID)
		}
		if _, exists := seen[call.ID]; exists {
			return fmt.Errorf("duplicate tool call ID %q", call.ID)
		}
		seen[call.ID] = struct{}{}
		names[call.ID] = call.Function.Name
	}
	for callID, target := range value.ToolTargets {
		name, exists := names[callID]
		if !exists {
			return fmt.Errorf("tool target names unknown tool call %q", callID)
		}
		if s.configuration.ToolKinds[name] != acp.ToolKindEdit || !validStoredTarget(target) {
			return fmt.Errorf("tool call %q has invalid target %q", callID, target)
		}
	}
	return nil
}

func (s *durableState) validatePendingPermission(value pendingPermissionRecord) error {
	if value.Generation != 1 {
		return errors.New("new permission request must start at generation 1")
	}
	index := s.suspended.callIndex(value.CallID)
	if index < 0 {
		return errors.New("permission request names an unknown tool call")
	}
	if s.suspended.decision(value.CallID) != nil {
		return errors.New("permission request follows a decision for the same tool call")
	}
	if len(s.suspended.Decisions) > 0 {
		last := s.suspended.callIndex(s.suspended.Decisions[len(s.suspended.Decisions)-1].CallID)
		if index <= last {
			return errors.New("permission request is out of tool-call order")
		}
	}
	call := s.suspended.ToolCalls[index]
	request := value.Request
	if request.SessionID != s.id || request.ToolCall.ToolCallID != call.ID ||
		request.ToolCall.Name != call.Function.Name ||
		!sameJSON(request.ToolCall.RawInput, []byte(call.Function.Arguments)) ||
		!reflect.DeepEqual(
			request.ToolCall.Locations,
			toolLocations(s.cwd, s.suspended.ToolTargets[call.ID]),
		) ||
		len(request.Options) == 0 {
		return errors.New("permission request does not match its tool call")
	}
	optionIDs := make(map[string]struct{}, len(request.Options))
	for _, option := range request.Options {
		if option.OptionID == "" || option.Name == "" || option.Kind == "" {
			return errors.New("permission request contains an invalid option")
		}
		if _, exists := optionIDs[option.OptionID]; exists {
			return errors.New("permission request contains duplicate options")
		}
		optionIDs[option.OptionID] = struct{}{}
	}
	return nil
}

func sameJSON(left, right []byte) bool {
	if !json.Valid(left) || !json.Valid(right) {
		return false
	}
	decode := func(data []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	leftValue, leftErr := decode(left)
	rightValue, rightErr := decode(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(leftValue, rightValue)
}

func (s *suspendedModelExchangeRecord) callIndex(id string) int {
	for index := range s.ToolCalls {
		if s.ToolCalls[index].ID == id {
			return index
		}
	}
	return -1
}

func (s *suspendedModelExchangeRecord) decision(id string) *storedPermissionDecision {
	for index := range s.Decisions {
		if s.Decisions[index].CallID == id {
			return &s.Decisions[index]
		}
	}
	return nil
}

func validApprovalDecision(value approvalDecision) bool {
	switch value {
	case decisionAllowOnce, decisionAllowAlways, decisionRefused, decisionCancelled:
		return true
	default:
		return false
	}
}

func validateCompletedSuspension(
	suspended suspendedModelExchangeRecord,
	completed modelExchangeRecord,
) error {
	if len(completed.ToolCalls) != len(completed.ToolResults) {
		return errors.New("tool calls and results must be one complete group")
	}
	if suspended.Pending != nil {
		return errors.New("model exchange completed with a pending permission request")
	}
	want := modelExchangeRecord{
		TurnID: suspended.TurnID, AnswerID: suspended.AnswerID,
		ThoughtID: suspended.ThoughtID, Text: suspended.Text,
		Reasoning: suspended.Reasoning, ReasoningDetails: suspended.ReasoningDetails,
		FinishReason: suspended.FinishReason, Usage: suspended.Usage,
		ToolCalls: suspended.ToolCalls,
	}
	got := completed
	got.ToolResults = nil
	wantJSON, err := json.Marshal(want)
	if err != nil {
		panic(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		panic(err)
	}
	if !bytes.Equal(wantJSON, gotJSON) {
		return errors.New("completed model exchange does not match its suspension")
	}
	for _, decision := range suspended.Decisions {
		index := suspended.callIndex(decision.CallID)
		if index < 0 || index >= len(completed.ToolResults) ||
			completed.ToolResults[index].ApprovalDecision != decision.Decision {
			return errors.New("completed tool results do not match recorded permission decisions")
		}
	}
	for index, call := range completed.ToolCalls {
		if completed.ToolResults[index].Target != suspended.ToolTargets[call.ID] {
			return errors.New("completed tool targets do not match the suspended exchange")
		}
	}
	return nil
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
	var suspended bool
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
		case recordExchangePaused:
			var value suspendedModelExchangeRecord
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			updates = append(updates, a.replaySuspendedExchange(value, configuration, s.cwd)...)
			suspended = true
		case recordPermissionOpen, recordPermissionRetry, recordPermissionDone:
			continue
		case recordModelExchange:
			var value modelExchangeRecord
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			if !suspended {
				updates = append(updates, a.replayModelContent(value)...)
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
				if !suspended {
					updates = append(
						updates,
						replayToolCall(a, call, configuration, parentMeta, s.cwd, result.Target),
					)
				}
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
								Locations:     toolLocations(s.cwd, child.Target),
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
			suspended = false
		case recordTurnFinished:
			var value turnFinishedRecord
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			if update := outcomeUpdate(value); update != nil {
				updates = append(updates, update)
			}
			suspended = false
		}
	}
	return updates, nil
}

func (a *Agent) replaySuspendedExchange(
	value suspendedModelExchangeRecord,
	configuration requestConfiguration,
	root string,
) []any {
	completed := modelExchangeRecord{
		TurnID: value.TurnID, AnswerID: value.AnswerID, ThoughtID: value.ThoughtID,
		Text: value.Text, Reasoning: value.Reasoning, ToolCalls: value.ToolCalls,
	}
	updates := a.replayModelContent(completed)
	for _, call := range value.ToolCalls {
		meta := acp.Metadata(nil)
		if a.toolDelegates(call.Function.Name) {
			meta = acp.Metadata{acp.MetaSubagent: true}
		}
		updates = append(
			updates,
			replayToolCall(a, call, configuration, meta, root, value.ToolTargets[call.ID]),
		)
	}
	return updates
}

func (a *Agent) replayModelContent(value modelExchangeRecord) []any {
	updates := make([]any, 0, 2)
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
	return updates
}

func replayToolCall(
	a *Agent,
	call openrouter.ToolCall,
	configuration requestConfiguration,
	meta acp.Metadata,
	root string,
	target string,
) acp.ToolCall {
	return acp.ToolCall{
		SessionUpdate: "tool_call",
		ToolCallID:    call.ID,
		Title:         a.toolTitle(call.Function.Name, json.RawMessage(call.Function.Arguments)),
		Name:          call.Function.Name,
		Kind:          configuration.ToolKinds[call.Function.Name],
		Status:        acp.ToolCallStatusPending,
		Locations:     toolLocations(root, target),
		RawInput:      json.RawMessage(call.Function.Arguments),
		Meta:          meta,
	}
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

func cloneSuspendedExchange(
	value *suspendedModelExchangeRecord,
) *suspendedModelExchangeRecord {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var cloned suspendedModelExchangeRecord
	if err := json.Unmarshal(data, &cloned); err != nil {
		panic(err)
	}
	return &cloned
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
	if len(values) == 0 {
		return nil
	}
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
	leftJSON, err := json.Marshal(left)
	if err != nil {
		panic(err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		panic(err)
	}
	return bytes.Equal(leftJSON, rightJSON)
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
