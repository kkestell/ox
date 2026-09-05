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
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
	"github.com/kkestell/ox/internal/skills"
)

const (
	recordVersion     = 1
	checkpointVersion = 9

	recordSessionCreated  = "session_created"
	recordConfigChanged   = "request_configuration_changed"
	recordOptionChanged   = "session_config_option_changed"
	recordTodoChanged     = "todo_replaced"
	recordCompaction      = "model_context_compacted"
	recordChildContext    = "child_context_updated"
	recordUserMessage     = "user_message"
	recordExchangePaused  = "suspended_model_exchange"
	recordPermissionOpen  = "permission_requested"
	recordPermissionRetry = "permission_reissued"
	recordPermissionDone  = "permission_decided"
	recordToolStarted     = "tool_started"
	recordToolCompleted   = "tool_completed"
	recordModelExchange   = "completed_model_exchange"
	recordTurnFinished    = "turn_finished"
	recordCheckpoint      = "checkpoint"

	unknownToolOutcome     = "tool call outcome is unknown after interruption"
	interruptedBeforeStart = "tool call interrupted before start"
	interruptedQuestion    = "question interrupted"
)

type sessionRecord struct {
	Version  int             `json:"version"`
	Sequence uint64          `json:"sequence"`
	Type     string          `json:"type"`
	At       time.Time       `json:"at"`
	Data     json.RawMessage `json:"data"`
}

type requestConfiguration struct {
	Mode                 string                  `json:"mode"`
	Settings             settings.Resolved       `json:"settings"`
	ContextWindow        int                     `json:"contextWindow"`
	SystemPrompt         string                  `json:"systemPrompt,omitempty"`
	Tools                []openrouter.Tool       `json:"tools,omitempty"`
	ToolKinds            map[string]acp.ToolKind `json:"toolKinds,omitempty"`
	PlanTools            map[string]bool         `json:"planTools,omitempty"`
	MCPTools             []mcpToolConfiguration  `json:"mcpTools,omitempty"`
	Skills               []skills.Reference      `json:"skills,omitempty"`
	Subagent             subagentConfiguration   `json:"subagent,omitempty"`
	ExecutorCapabilities executorCapabilities    `json:"executorCapabilities"`
}

type mcpToolConfiguration struct {
	Name       string `json:"name"`
	ServerName string `json:"serverName"`
	ToolName   string `json:"toolName"`
	Title      string `json:"title,omitempty"`
	Identity   string `json:"identity"`
}

type sessionSelections struct {
	Mode      string  `json:"mode,omitempty"`
	Model     string  `json:"model,omitempty"`
	Reasoning *string `json:"reasoning,omitempty"`
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
	Selections    sessionSelections    `json:"selections,omitempty"`
}

type configurationChanged struct {
	Configuration requestConfiguration `json:"configuration"`
}

type optionChanged struct {
	Selections    sessionSelections         `json:"selections"`
	Configuration requestConfiguration      `json:"configuration"`
	Options       []acp.SessionConfigOption `json:"options"`
}

type todoChanged struct {
	TurnID  string          `json:"turnId"`
	CallID  string          `json:"callId"`
	Entries []acp.PlanEntry `json:"entries"`
}

type compactionRecord struct {
	TurnID       string             `json:"turnId"`
	ParentCallID string             `json:"parentCallId,omitempty"`
	HeadEnd      int                `json:"headEnd"`
	TailStart    int                `json:"tailStart"`
	Summary      openrouter.Message `json:"summary"`
	Usage        *openrouter.Usage  `json:"usage,omitempty"`
	Occupancy    int                `json:"occupancy"`
}

type childContextRecord struct {
	TurnID     string            `json:"turnId"`
	Child      childContext      `json:"child"`
	Compaction *compactionRecord `json:"compaction,omitempty"`
}

type childContext struct {
	ParentCallID string               `json:"parentCallId"`
	Prompt       string               `json:"prompt"`
	History      []openrouter.Message `json:"history"`
	Calls        []delegatedCall      `json:"calls,omitempty"`
	Usage        []openrouter.Usage   `json:"usage,omitempty"`
	RequestCount int                  `json:"requestCount,omitempty"`
	Occupancy    int                  `json:"occupancy,omitempty"`
	Answer       string               `json:"answer,omitempty"`
}

type userMessageRecord struct {
	TurnID        string               `json:"turnId"`
	MessageID     string               `json:"messageId"`
	Content       []acp.ContentBlock   `json:"content"`
	Configuration requestConfiguration `json:"configuration,omitempty"`
}

type storedToolResult struct {
	CallID           string            `json:"callId"`
	Content          string            `json:"content"`
	Failed           bool              `json:"failed,omitempty"`
	ApprovalDecision approvalDecision  `json:"approvalDecision,omitempty"`
	Delegation       *delegationRecord `json:"delegation,omitempty"`
	Target           string            `json:"target,omitempty"`
	Unknown          bool              `json:"unknown,omitempty"`
}

type toolStartedRecord struct {
	TurnID           string              `json:"turnId"`
	ParentCallID     string              `json:"parentCallId,omitempty"`
	Call             openrouter.ToolCall `json:"call"`
	ApprovalDecision approvalDecision    `json:"approvalDecision,omitempty"`
	Target           string              `json:"target,omitempty"`
}

type toolCompletedRecord struct {
	TurnID       string           `json:"turnId"`
	ParentCallID string           `json:"parentCallId,omitempty"`
	CallID       string           `json:"callId"`
	Result       storedToolResult `json:"result"`
}

type durableToolExecution struct {
	TurnID           string              `json:"turnId"`
	ParentCallID     string              `json:"parentCallId,omitempty"`
	Call             openrouter.ToolCall `json:"call"`
	ApprovalDecision approvalDecision    `json:"approvalDecision,omitempty"`
	Target           string              `json:"target,omitempty"`
	StartedSequence  uint64              `json:"startedSequence"`
	Result           *storedToolResult   `json:"result,omitempty"`
}

type delegationRecord struct {
	Prompt       string               `json:"prompt"`
	Answer       string               `json:"answer,omitempty"`
	Calls        []delegatedCall      `json:"calls,omitempty"`
	Usage        []openrouter.Usage   `json:"usage,omitempty"`
	History      []openrouter.Message `json:"history,omitempty"`
	RequestCount int                  `json:"requestCount,omitempty"`
	Occupancy    int                  `json:"occupancy,omitempty"`
}

type delegatedCall struct {
	CallID           string           `json:"callId"`
	Name             string           `json:"name"`
	Arguments        json.RawMessage  `json:"arguments"`
	Content          string           `json:"content"`
	Failed           bool             `json:"failed,omitempty"`
	ApprovalDecision approvalDecision `json:"approvalDecision,omitempty"`
	Target           string           `json:"target,omitempty"`
	Unknown          bool             `json:"unknown,omitempty"`
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
	Interrupted      bool                  `json:"interrupted,omitempty"`
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
	SessionID             string                        `json:"sessionId"`
	CWD                   string                        `json:"cwd"`
	CreatedAt             time.Time                     `json:"createdAt"`
	UpdatedAt             time.Time                     `json:"updatedAt"`
	Sequence              uint64                        `json:"sequence"`
	Configuration         requestConfiguration          `json:"configuration"`
	Selections            sessionSelections             `json:"selections,omitempty"`
	Todo                  []acp.PlanEntry               `json:"todo"`
	History               []openrouter.Message          `json:"history,omitempty"`
	Usage                 checkpointUsage               `json:"usage"`
	Occupancy             int                           `json:"occupancy"`
	Cost                  float64                       `json:"cost"`
	MessageIDs            []string                      `json:"messageIds,omitempty"`
	ToolCallIDs           []string                      `json:"toolCallIds,omitempty"`
	ChangedFiles          []string                      `json:"changedFiles,omitempty"`
	OpenTurn              string                        `json:"openTurn,omitempty"`
	OpenTurnBase          []openrouter.Message          `json:"openTurnBase,omitempty"`
	OpenTurnConfiguration *requestConfiguration         `json:"openTurnConfiguration,omitempty"`
	Suspended             *suspendedModelExchangeRecord `json:"suspended,omitempty"`
	Children              []childContext                `json:"children,omitempty"`
	ToolExecutions        []durableToolExecution        `json:"toolExecutions,omitempty"`
	Title                 string                        `json:"title,omitempty"`
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
	id                    string
	cwd                   string
	createdAt             time.Time
	updatedAt             time.Time
	sequence              uint64
	configuration         requestConfiguration
	selections            sessionSelections
	todo                  []acp.PlanEntry
	history               []openrouter.Message
	usage                 turnUsage
	occupancy             int
	cost                  float64
	records               []sessionRecord
	messageIDs            map[string]struct{}
	toolCallIDs           map[string]struct{}
	changedFiles          map[string]struct{}
	openTurn              string
	openTurnHistory       int
	openTurnBase          []openrouter.Message
	openTurnConfiguration requestConfiguration
	suspended             *suspendedModelExchangeRecord
	children              map[string]childContext
	toolExecutions        map[string]durableToolExecution
	title                 string
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
		if index == 0 || !checkpointBoundary(records[index-1].Type) {
			return durableState{}, fmt.Errorf("record %d: checkpoint does not follow a durable provider boundary", index+1)
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
	case recordSessionCreated, recordConfigChanged, recordOptionChanged, recordTodoChanged, recordCompaction, recordChildContext, recordUserMessage,
		recordExchangePaused, recordPermissionOpen, recordPermissionRetry,
		recordPermissionDone, recordToolStarted, recordToolCompleted,
		recordModelExchange, recordTurnFinished, recordCheckpoint:
		return nil
	default:
		return fmt.Errorf("unsupported record type %q", record.Type)
	}
}

func newCheckpointRecord(state durableState) (sessionRecord, error) {
	if len(state.records) == 0 || !checkpointBoundary(state.records[len(state.records)-1].Type) {
		return sessionRecord{}, errors.New("checkpoint requires a durable provider boundary")
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
			Selections:    cloneSelections(state.selections),
			Todo:          clonePlanEntries(state.todo),
			History:       cloneMessages(state.history),
			Usage: checkpointUsage{
				Seen:        state.usage.seen,
				Input:       state.usage.input,
				Output:      state.usage.output,
				Thought:     state.usage.thought,
				CachedRead:  state.usage.cachedRead,
				CachedWrite: state.usage.cachedWrite,
			},
			Occupancy:             state.occupancy,
			Cost:                  state.cost,
			MessageIDs:            sortedIdentitySet(state.messageIDs),
			ToolCallIDs:           sortedIdentitySet(state.toolCallIDs),
			ChangedFiles:          sortedIdentitySet(state.changedFiles),
			OpenTurn:              state.openTurn,
			OpenTurnBase:          cloneMessages(state.openTurnBase),
			OpenTurnConfiguration: optionalConfiguration(state.openTurnConfiguration),
			Suspended:             cloneSuspendedExchange(state.suspended),
			Children:              sortedChildContexts(state.children),
			ToolExecutions:        sortedToolExecutions(state.toolExecutions),
			Title:                 state.title,
		},
	}
	return newRecord(state.sequence+1, recordCheckpoint, payload)
}

func checkpointBoundary(kind string) bool {
	return kind == recordTurnFinished || kind == recordCompaction || kind == recordChildContext
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
	if previous.Type == recordTurnFinished && projection.OpenTurn != "" {
		return durableState{}, errors.New("checkpoint after a finished turn is still open")
	}
	if previous.Type != recordTurnFinished && projection.OpenTurn == "" {
		return durableState{}, errors.New("provider-boundary checkpoint has no open turn")
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
	if projection.Todo != nil {
		if err := validateTodoEntries(projection.Todo); err != nil {
			return durableState{}, fmt.Errorf("checkpoint todo: %w", err)
		}
	}
	if projection.Occupancy < 0 {
		return durableState{}, errors.New("checkpoint context occupancy is invalid")
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
	children, err := childContextMap(projection.Children)
	if err != nil {
		return durableState{}, err
	}
	toolExecutions, err := toolExecutionMap(projection.ToolExecutions)
	if err != nil {
		return durableState{}, err
	}
	state := durableState{
		id:            projection.SessionID,
		cwd:           projection.CWD,
		createdAt:     projection.CreatedAt,
		updatedAt:     projection.UpdatedAt,
		sequence:      record.Sequence,
		configuration: cloneConfiguration(projection.Configuration),
		selections:    cloneSelections(projection.Selections),
		todo:          clonePlanEntries(projection.Todo),
		history:       cloneMessages(projection.History),
		usage: turnUsage{
			seen:        projection.Usage.Seen,
			input:       projection.Usage.Input,
			output:      projection.Usage.Output,
			thought:     projection.Usage.Thought,
			cachedRead:  projection.Usage.CachedRead,
			cachedWrite: projection.Usage.CachedWrite,
		},
		occupancy:             projection.Occupancy,
		cost:                  projection.Cost,
		messageIDs:            messageIDs,
		toolCallIDs:           toolCallIDs,
		changedFiles:          changedFiles,
		openTurn:              projection.OpenTurn,
		openTurnHistory:       len(projection.OpenTurnBase),
		openTurnBase:          cloneMessages(projection.OpenTurnBase),
		openTurnConfiguration: configurationValue(projection.OpenTurnConfiguration),
		suspended:             cloneSuspendedExchange(projection.Suspended),
		children:              children,
		toolExecutions:        toolExecutions,
		title:                 projection.Title,
	}
	if err := validateCheckpointTurnState(state); err != nil {
		return durableState{}, err
	}
	return state, nil
}

func sortedChildContexts(values map[string]childContext) []childContext {
	result := make([]childContext, 0, len(values))
	for _, value := range values {
		result = append(result, cloneChildContext(value))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ParentCallID < result[j].ParentCallID
	})
	return result
}

func sortedToolExecutions(values map[string]durableToolExecution) []durableToolExecution {
	result := make([]durableToolExecution, 0, len(values))
	for _, value := range values {
		result = append(result, cloneToolExecution(value))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedSequence < result[j].StartedSequence
	})
	return result
}

func toolExecutionMap(values []durableToolExecution) (map[string]durableToolExecution, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]durableToolExecution, len(values))
	var previous uint64
	for _, value := range values {
		if value.Call.ID == "" || value.StartedSequence == 0 || value.StartedSequence <= previous {
			return nil, errors.New("checkpoint tool executions are not in durable order")
		}
		if _, duplicate := result[value.Call.ID]; duplicate {
			return nil, fmt.Errorf("checkpoint contains duplicate tool execution %q", value.Call.ID)
		}
		result[value.Call.ID] = cloneToolExecution(value)
		previous = value.StartedSequence
	}
	return result, nil
}

func childContextMap(values []childContext) (map[string]childContext, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]childContext, len(values))
	for _, value := range values {
		if value.ParentCallID == "" {
			return nil, errors.New("checkpoint child context identity is required")
		}
		if _, duplicate := result[value.ParentCallID]; duplicate {
			return nil, fmt.Errorf("checkpoint contains duplicate child context %q", value.ParentCallID)
		}
		result[value.ParentCallID] = cloneChildContext(value)
	}
	return result, nil
}

func validateCheckpointTurnState(state durableState) error {
	if state.openTurn == "" {
		if len(state.openTurnBase) != 0 || state.openTurnConfiguration.Settings.Model != "" || state.suspended != nil ||
			len(state.children) != 0 || len(state.toolExecutions) != 0 {
			return errors.New("checkpoint has turn state without an open turn")
		}
		return nil
	}
	if err := validateConfiguration(state.openTurnConfiguration); err != nil {
		return fmt.Errorf("checkpoint open-turn configuration: %w", err)
	}
	if state.suspended != nil {
		progress := cloneSuspendedExchange(state.suspended)
		decisions := append([]storedPermissionDecision(nil), progress.Decisions...)
		pending := progress.Pending
		progress.Decisions = nil
		progress.Pending = nil
		if err := state.validateSuspendedExchange(*progress); err != nil {
			return fmt.Errorf("checkpoint suspended exchange: %w", err)
		}
		progress.Decisions = decisions
		progress.Pending = pending
		for index, decision := range decisions {
			if !validApprovalDecision(decision.Decision) || progress.callIndex(decision.CallID) < 0 {
				return errors.New("checkpoint contains an invalid permission decision")
			}
			if index > 0 && progress.callIndex(decisions[index-1].CallID) >= progress.callIndex(decision.CallID) {
				return errors.New("checkpoint permission decisions are out of order")
			}
		}
		if pending != nil {
			copy := state
			copy.suspended = progress
			if err := copy.validatePendingPermission(*pending); err != nil {
				return fmt.Errorf("checkpoint pending permission: %w", err)
			}
		}
	} else if len(state.children) != 0 {
		return errors.New("checkpoint has child context without a suspended exchange")
	}
	for _, execution := range state.toolExecutions {
		copy := state.clone()
		delete(copy.toolExecutions, execution.Call.ID)
		started := cloneToolExecution(execution)
		started.Result = nil
		if err := copy.validateToolExecution(started); err != nil {
			return fmt.Errorf("checkpoint tool execution: %w", err)
		}
		if execution.Result != nil {
			if err := validateStoredExecutionResult(execution, *execution.Result); err != nil {
				return fmt.Errorf("checkpoint tool execution: %w", err)
			}
		}
	}
	seen := make(map[string]struct{}, len(state.children))
	for id, child := range state.children {
		if id == "" || id != child.ParentCallID {
			return errors.New("checkpoint child context identity is invalid")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("checkpoint contains duplicate child context %q", id)
		}
		seen[id] = struct{}{}
		if state.suspended.callIndex(id) < 0 {
			return fmt.Errorf("checkpoint child context %q has no parent tool call", id)
		}
		if err := validateChildHistory(child); err != nil {
			return fmt.Errorf("checkpoint child context %q: %w", id, err)
		}
	}
	return nil
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
	s.openTurnBase = cloneMessages(s.openTurnBase)
	s.records = append([]sessionRecord(nil), s.records...)
	s.configuration = cloneConfiguration(s.configuration)
	s.selections = cloneSelections(s.selections)
	s.todo = clonePlanEntries(s.todo)
	s.openTurnConfiguration = cloneConfiguration(s.openTurnConfiguration)
	s.suspended = cloneSuspendedExchange(s.suspended)
	children := s.children
	if len(children) == 0 {
		s.children = nil
	} else {
		s.children = make(map[string]childContext, len(children))
		for id, child := range children {
			s.children[id] = cloneChildContext(child)
		}
	}
	toolExecutions := s.toolExecutions
	if len(toolExecutions) == 0 {
		s.toolExecutions = nil
	} else {
		s.toolExecutions = make(map[string]durableToolExecution, len(toolExecutions))
		for id, execution := range toolExecutions {
			s.toolExecutions[id] = cloneToolExecution(execution)
		}
	}
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
		if err := validateSelections(value.Selections); err != nil {
			return err
		}
		s.selections = cloneSelections(value.Selections)
		s.todo = nil
		s.messageIDs = make(map[string]struct{})
		s.toolCallIDs = make(map[string]struct{})
		s.changedFiles = make(map[string]struct{})
	case recordConfigChanged:
		var value configurationChanged
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if err := validateConfiguration(value.Configuration); err != nil {
			return err
		}
		s.configuration = cloneConfiguration(value.Configuration)
	case recordOptionChanged:
		var value optionChanged
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if err := validateSelections(value.Selections); err != nil {
			return err
		}
		if err := validateConfiguration(value.Configuration); err != nil {
			return err
		}
		if len(value.Options) < 2 {
			return errors.New("configuration option change requires the complete option list")
		}
		for _, option := range value.Options {
			if err := option.Validate(); err != nil {
				return fmt.Errorf("configuration option change: %w", err)
			}
		}
		s.selections = cloneSelections(value.Selections)
		s.configuration = cloneConfiguration(value.Configuration)
	case recordTodoChanged:
		var value todoChanged
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if value.TurnID == "" || value.TurnID != s.openTurn || value.CallID == "" {
			return errors.New("todo replacement has no matching open turn")
		}
		execution, exists := s.toolExecutions[value.CallID]
		if !exists || execution.Result != nil || execution.TurnID != value.TurnID ||
			execution.ParentCallID != "" || execution.Call.Function.Name != "todo" {
			return errors.New("todo replacement has no matching started top-level call")
		}
		if err := validateTodoEntries(value.Entries); err != nil {
			return err
		}
		s.todo = clonePlanEntries(value.Entries)
	case recordCompaction:
		if s.suspended != nil {
			return errors.New("model context compacted with an unresolved tool group")
		}
		var value compactionRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if err := validateCompaction(*s, value); err != nil {
			return err
		}
		history := make([]openrouter.Message, 0, value.HeadEnd+1+len(s.history)-value.TailStart)
		history = append(history, cloneMessages(s.history[:value.HeadEnd])...)
		history = append(history, cloneMessages([]openrouter.Message{value.Summary})...)
		history = append(history, cloneMessages(s.history[value.TailStart:])...)
		s.history = history
		s.addUsage(value.Usage)
		s.occupancy = value.Occupancy
	case recordChildContext:
		var value childContextRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if err := s.applyChildContext(value); err != nil {
			return err
		}
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
		turnConfiguration := value.Configuration
		if turnConfiguration.Settings.Model == "" {
			turnConfiguration = s.configuration
		}
		if err := validateConfiguration(turnConfiguration); err != nil {
			return fmt.Errorf("turn configuration: %w", err)
		}
		if s.openTurn == "" {
			s.openTurn = value.TurnID
			s.openTurnHistory = len(s.history)
			s.openTurnBase = cloneMessages(s.history)
			s.openTurnConfiguration = cloneConfiguration(turnConfiguration)
		} else if s.openTurn != value.TurnID {
			return errors.New("user message belongs to another open turn")
		} else if !sameRequestConfiguration(s.openTurnConfiguration, turnConfiguration) {
			return errors.New("user message changed the open turn configuration")
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
	case recordToolStarted:
		var value toolStartedRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		execution := durableToolExecution{
			TurnID:           value.TurnID,
			ParentCallID:     value.ParentCallID,
			Call:             value.Call,
			ApprovalDecision: value.ApprovalDecision,
			Target:           value.Target,
			StartedSequence:  record.Sequence,
		}
		if _, exists := s.toolCallIDs[value.Call.ID]; exists {
			return fmt.Errorf("tool call %q was already completed", value.Call.ID)
		}
		if err := s.validateToolExecution(execution); err != nil {
			return err
		}
		if s.toolExecutions == nil {
			s.toolExecutions = make(map[string]durableToolExecution)
		}
		s.toolExecutions[value.Call.ID] = execution
	case recordToolCompleted:
		var value toolCompletedRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		execution, exists := s.toolExecutions[value.CallID]
		if !exists || execution.Result != nil || value.TurnID != s.openTurn ||
			value.TurnID != execution.TurnID || value.ParentCallID != execution.ParentCallID {
			return errors.New("tool completion has no matching started call")
		}
		if err := validateStoredExecutionResult(execution, value.Result); err != nil {
			return err
		}
		result := cloneStoredToolResult(value.Result)
		execution.Result = &result
		s.toolExecutions[value.CallID] = execution
		if !result.Failed && result.Target != "" {
			s.changedFiles[result.Target] = struct{}{}
		}
	case recordModelExchange:
		var value modelExchangeRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if s.suspended != nil {
			if err := s.validateCompletedSuspension(*s.suspended, value); err != nil {
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
					(s.turnConfiguration().ToolKinds[call.Function.Name] != acp.ToolKindEdit ||
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
					child, durableChild := s.children[call.ID]
					if durableChild {
						if !value.Interrupted && !delegationMatchesChild(*result.Delegation, child) {
							return fmt.Errorf("delegation result for tool call %q does not match durable child context", call.ID)
						}
						if value.Interrupted && !s.interruptedDelegationExtendsChild(*result.Delegation, child) {
							return fmt.Errorf("interrupted delegation for tool call %q does not extend durable child context", call.ID)
						}
						delete(s.children, call.ID)
					}
					for _, child := range result.Delegation.Calls {
						if child.CallID == "" || child.Name == "" ||
							!json.Valid(child.Arguments) {
							return errors.New("delegated tool call identity, name, and JSON arguments are required")
						}
						if _, exists := s.toolCallIDs[child.CallID]; exists && !durableChild {
							return fmt.Errorf("duplicate tool call ID %q", child.CallID)
						}
						if child.Target != "" &&
							(s.turnConfiguration().ToolKinds[child.Name] != acp.ToolKindEdit ||
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
					if !durableChild {
						for index := range result.Delegation.Usage {
							s.addUsage(&result.Delegation.Usage[index])
						}
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
		if value.Usage != nil {
			s.occupancy = value.Usage.PromptTokens
		}
		if len(s.children) != 0 {
			return errors.New("completed model exchange has unfinished child context")
		}
		s.suspended = nil
		s.toolExecutions = nil
	case recordTurnFinished:
		var value turnFinishedRecord
		if err := decodeRecord(record.Data, &value); err != nil {
			return err
		}
		if s.openTurn == "" || value.TurnID != s.openTurn {
			return errors.New("turn outcome has no matching open turn")
		}
		if value.Kind == "refusal" {
			s.history = cloneMessages(s.openTurnBase)
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
		s.openTurnBase = nil
		s.openTurnConfiguration = requestConfiguration{}
		s.children = nil
		s.toolExecutions = nil
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

func validateCompaction(state durableState, value compactionRecord) error {
	if state.openTurn == "" || value.TurnID != state.openTurn || value.ParentCallID != "" {
		return errors.New("compaction does not match the open parent turn")
	}
	return validateCompactionHistory(state.history, value)
}

func validateCompactionHistory(history []openrouter.Message, value compactionRecord) error {
	firstUser := -1
	for index := range history {
		if history[index].Role == openrouter.RoleUser {
			firstUser = index
			break
		}
	}
	if firstUser < 0 || value.HeadEnd != firstUser+1 {
		return errors.New("compaction does not preserve the first user message")
	}
	if value.TailStart <= value.HeadEnd || value.TailStart >= len(history) {
		return errors.New("compaction splice boundaries are invalid")
	}
	if !messageGroupBoundary(history, value.HeadEnd) ||
		!messageGroupBoundary(history, value.TailStart) {
		return errors.New("compaction separates a tool call from its result")
	}
	if value.Summary.Role != openrouter.RoleUser || len(value.Summary.Content) != 1 ||
		value.Summary.Content[0].Type != "text" ||
		!strings.HasPrefix(value.Summary.Content[0].Text, summaryMessagePrefix) ||
		strings.TrimSpace(strings.TrimPrefix(value.Summary.Content[0].Text, summaryMessagePrefix)) == "" ||
		len(value.Summary.ToolCalls) != 0 || value.Summary.ToolCallID != "" ||
		len(value.Summary.ReasoningDetails) != 0 {
		return errors.New("compaction summary message is invalid")
	}
	if value.Occupancy <= 0 {
		return errors.New("compaction context occupancy is invalid")
	}
	return nil
}

func applyCompaction(history []openrouter.Message, value compactionRecord) []openrouter.Message {
	compacted := make([]openrouter.Message, 0, value.HeadEnd+1+len(history)-value.TailStart)
	compacted = append(compacted, cloneMessages(history[:value.HeadEnd])...)
	compacted = append(compacted, cloneMessages([]openrouter.Message{value.Summary})...)
	compacted = append(compacted, cloneMessages(history[value.TailStart:])...)
	return compacted
}

func (s *durableState) applyChildContext(value childContextRecord) error {
	if s.openTurn == "" || value.TurnID != s.openTurn || s.suspended == nil {
		return errors.New("child context has no matching suspended exchange")
	}
	child := value.Child
	if child.ParentCallID == "" || strings.TrimSpace(child.Prompt) == "" ||
		child.RequestCount < 0 || child.RequestCount > maxTurnRequests || child.Occupancy < 0 {
		return errors.New("child context identity, prompt, and occupancy are invalid")
	}
	parentIndex := s.suspended.callIndex(child.ParentCallID)
	if parentIndex < 0 {
		return errors.New("child context names an unknown parent tool call")
	}
	configuration := s.turnConfiguration()
	if !configuredDelegatingTool(configuration, s.suspended.ToolCalls[parentIndex].Function.Name) {
		return errors.New("child context parent is not a delegating tool")
	}
	if err := validateChildHistory(child); err != nil {
		return err
	}
	previous, exists := s.children[child.ParentCallID]
	if !exists {
		if value.Compaction != nil || len(child.Calls) != 0 || len(child.Usage) != 0 || child.RequestCount != 0 ||
			child.Answer != "" || len(child.History) != 1 {
			return errors.New("new child context must contain only its prompt")
		}
	} else {
		if child.Prompt != previous.Prompt || child.RequestCount < previous.RequestCount ||
			child.RequestCount > previous.RequestCount+1 || !slicePrefix(previous.Calls, child.Calls) ||
			!slicePrefix(previous.Usage, child.Usage) {
			return errors.New("child context does not extend its durable progress")
		}
		if value.Compaction == nil {
			if child.RequestCount != previous.RequestCount+1 {
				return errors.New("child context request count did not advance")
			}
			if !jsonSlicePrefix(previous.History, child.History) {
				return errors.New("child context rewrote history without a compaction")
			}
		} else {
			if child.RequestCount != previous.RequestCount {
				return errors.New("child context compaction changed its request count")
			}
			if value.Compaction.TurnID != value.TurnID ||
				value.Compaction.ParentCallID != child.ParentCallID {
				return errors.New("child context compaction scope is invalid")
			}
			if err := validateCompactionHistory(previous.History, *value.Compaction); err != nil {
				return fmt.Errorf("child context compaction: %w", err)
			}
			want := applyCompaction(previous.History, *value.Compaction)
			if len(want) != len(child.History) || !jsonSlicePrefix(want, child.History) ||
				child.Occupancy != value.Compaction.Occupancy {
				return errors.New("child context does not match its compaction")
			}
			wantUsage := len(previous.Usage)
			if value.Compaction.Usage != nil {
				wantUsage++
			}
			if len(child.Usage) != wantUsage || value.Compaction.Usage != nil &&
				!reflect.DeepEqual(child.Usage[len(child.Usage)-1], *value.Compaction.Usage) {
				return errors.New("child context compaction usage is invalid")
			}
		}
		for _, current := range child.Calls[len(previous.Calls):] {
			if current.CallID == "" || current.Name == "" || !json.Valid(current.Arguments) {
				return errors.New("child tool call identity, name, and arguments are required")
			}
			if _, duplicate := s.toolCallIDs[current.CallID]; duplicate {
				return fmt.Errorf("duplicate tool call ID %q", current.CallID)
			}
			if current.Target != "" &&
				(configuration.ToolKinds[current.Name] != acp.ToolKindEdit || !validStoredTarget(current.Target)) {
				return fmt.Errorf("child tool call %q has invalid target %q", current.CallID, current.Target)
			}
			execution, exists := s.toolExecutions[current.CallID]
			if !exists && !current.Failed {
				return fmt.Errorf("successful child tool call %q has no durable dispatch", current.CallID)
			}
			if exists && (execution.ParentCallID != child.ParentCallID ||
				execution.Result == nil || !delegatedCallMatchesExecution(current, execution)) {
				return fmt.Errorf("child tool call %q has no matching durable completion", current.CallID)
			}
			s.toolCallIDs[current.CallID] = struct{}{}
			if !current.Failed && current.Target != "" {
				s.changedFiles[current.Target] = struct{}{}
			}
		}
		for index := len(previous.Usage); index < len(child.Usage); index++ {
			s.addUsage(&child.Usage[index])
		}
	}
	if s.children == nil {
		s.children = make(map[string]childContext)
	}
	s.children[child.ParentCallID] = cloneChildContext(child)
	return nil
}

func delegatedCallMatchesExecution(value delegatedCall, execution durableToolExecution) bool {
	if value.CallID != execution.Call.ID || value.Name != execution.Call.Function.Name ||
		!sameJSON(value.Arguments, []byte(execution.Call.Function.Arguments)) {
		return false
	}
	result := execution.Result
	return result != nil && value.Content == result.Content && value.Failed == result.Failed &&
		value.ApprovalDecision == result.ApprovalDecision && value.Target == result.Target &&
		value.Unknown == result.Unknown
}

func validateChildHistory(child childContext) error {
	if len(child.History) == 0 || child.History[0].Role != openrouter.RoleUser ||
		len(child.History[0].Content) != 1 || child.History[0].Content[0].Type != "text" ||
		child.History[0].Content[0].Text != child.Prompt {
		return errors.New("child history does not begin with its prompt")
	}
	for index := 0; index < len(child.History); index++ {
		message := child.History[index]
		if message.Role == openrouter.RoleTool {
			return errors.New("child history contains an orphaned tool result")
		}
		if message.Role != openrouter.RoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		if index+len(message.ToolCalls) >= len(child.History)+1 {
			return errors.New("child history contains an unresolved tool group")
		}
		for callIndex, call := range message.ToolCalls {
			resultIndex := index + callIndex + 1
			if resultIndex >= len(child.History) || child.History[resultIndex].Role != openrouter.RoleTool ||
				child.History[resultIndex].ToolCallID != call.ID {
				return errors.New("child history contains an incomplete tool group")
			}
		}
		index += len(message.ToolCalls)
	}
	return nil
}

func slicePrefix[T any](prefix, values []T) bool {
	if len(prefix) == 0 {
		return true
	}
	return len(prefix) <= len(values) && reflect.DeepEqual(prefix, values[:len(prefix)])
}

func jsonSlicePrefix[T any](prefix, values []T) bool {
	if len(prefix) == 0 {
		return true
	}
	if len(prefix) > len(values) {
		return false
	}
	left, err := json.Marshal(prefix)
	if err != nil {
		panic(err)
	}
	right, err := json.Marshal(values[:len(prefix)])
	if err != nil {
		panic(err)
	}
	return bytes.Equal(left, right)
}

func cloneChildContext(value childContext) childContext {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var cloned childContext
	if err := json.Unmarshal(data, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func delegationMatchesChild(value delegationRecord, child childContext) bool {
	want := delegationRecord{
		Prompt: child.Prompt, Answer: child.Answer, Calls: child.Calls,
		Usage: child.Usage, History: child.History, RequestCount: child.RequestCount,
		Occupancy: child.Occupancy,
	}
	gotJSON, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		panic(err)
	}
	return bytes.Equal(gotJSON, wantJSON)
}

func (s *durableState) interruptedDelegationExtendsChild(
	value delegationRecord,
	child childContext,
) bool {
	if value.Prompt != child.Prompt || value.Answer != child.Answer ||
		!reflect.DeepEqual(value.Usage, child.Usage) ||
		!reflect.DeepEqual(value.History, child.History) ||
		value.RequestCount != child.RequestCount || value.Occupancy != child.Occupancy ||
		!slicePrefix(child.Calls, value.Calls) {
		return false
	}
	for _, call := range value.Calls[len(child.Calls):] {
		execution, exists := s.toolExecutions[call.CallID]
		if !exists || execution.ParentCallID != child.ParentCallID ||
			execution.Result == nil || !delegatedCallMatchesExecution(call, execution) {
			return false
		}
	}
	return true
}

func configuredDelegatingTool(configuration requestConfiguration, name string) bool {
	var primary bool
	for _, tool := range configuration.Tools {
		if tool.Function.Name == name {
			primary = true
			break
		}
	}
	if !primary {
		return false
	}
	for _, tool := range configuration.Subagent.Tools {
		if tool.Function.Name == name {
			return false
		}
	}
	return true
}

func configuredSubagentTool(configuration requestConfiguration, name string) bool {
	for _, tool := range configuration.Subagent.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func (s *durableState) validateToolExecution(value durableToolExecution) error {
	if s.openTurn == "" || s.suspended == nil || value.TurnID != s.openTurn ||
		value.Call.ID == "" || value.Call.Function.Name == "" ||
		!json.Valid([]byte(value.Call.Function.Arguments)) || value.StartedSequence == 0 {
		return errors.New("started tool call identity, scope, and arguments are required")
	}
	if _, exists := s.toolExecutions[value.Call.ID]; exists {
		return fmt.Errorf("tool call %q was started twice", value.Call.ID)
	}
	if value.ApprovalDecision != "" && !validApprovalDecision(value.ApprovalDecision) {
		return fmt.Errorf("tool call %q has an invalid approval decision", value.Call.ID)
	}
	if value.ParentCallID == "" {
		index := s.suspended.callIndex(value.Call.ID)
		if index < 0 || !sameToolCall(s.suspended.ToolCalls[index], value.Call) ||
			value.Target != s.suspended.ToolTargets[value.Call.ID] {
			return errors.New("started tool call does not match the suspended exchange")
		}
		if decision := s.suspended.decision(value.Call.ID); decision != nil &&
			decision.Decision != value.ApprovalDecision {
			return errors.New("started tool call does not match its permission decision")
		}
	} else {
		if s.suspended.callIndex(value.ParentCallID) < 0 ||
			!configuredDelegatingTool(
				s.turnConfiguration(),
				s.suspended.ToolCalls[s.suspended.callIndex(value.ParentCallID)].Function.Name,
			) {
			return errors.New("started child call has no delegating parent")
		}
		if _, exists := s.children[value.ParentCallID]; !exists ||
			!configuredSubagentTool(s.turnConfiguration(), value.Call.Function.Name) {
			return errors.New("started child call is not part of durable child context")
		}
	}
	if value.Target != "" &&
		(s.turnConfiguration().ToolKinds[value.Call.Function.Name] != acp.ToolKindEdit ||
			!validStoredTarget(value.Target)) {
		return fmt.Errorf("tool call %q has invalid target %q", value.Call.ID, value.Target)
	}
	if value.Result != nil {
		return errors.New("new started tool call cannot contain a result")
	}
	return nil
}

func validateStoredExecutionResult(
	execution durableToolExecution,
	result storedToolResult,
) error {
	if result.CallID != execution.Call.ID || result.Target != execution.Target ||
		result.ApprovalDecision != execution.ApprovalDecision {
		return errors.New("tool completion does not match its started call")
	}
	if result.Unknown && (!result.Failed || result.Content != unknownToolOutcome ||
		result.Delegation != nil) {
		return errors.New("unknown tool completion is invalid")
	}
	return nil
}

func sameToolCall(left, right openrouter.ToolCall) bool {
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

func cloneStoredToolResult(value storedToolResult) storedToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var cloned storedToolResult
	if err := json.Unmarshal(data, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func cloneToolExecution(value durableToolExecution) durableToolExecution {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var cloned durableToolExecution
	if err := json.Unmarshal(data, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func messageGroupBoundary(messages []openrouter.Message, index int) bool {
	if index < 0 || index > len(messages) {
		return false
	}
	if index < len(messages) && messages[index].Role == openrouter.RoleTool {
		return false
	}
	return index == 0 || messages[index-1].Role != openrouter.RoleAssistant ||
		len(messages[index-1].ToolCalls) == 0
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
		if s.turnConfiguration().ToolKinds[name] != acp.ToolKindEdit || !validStoredTarget(target) {
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

func (s *durableState) validateCompletedSuspension(
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
	got.Interrupted = false
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
		execution, started := s.toolExecutions[call.ID]
		if !started {
			if !completed.ToolResults[index].Failed {
				return errors.New("successful tool result has no durable dispatch")
			}
			continue
		}
		if execution.ParentCallID != "" {
			return errors.New("parent tool result matched a child execution")
		}
		if execution.Result == nil {
			if !completed.ToolResults[index].Unknown {
				return errors.New("started tool without completion must have unknown outcome")
			}
			continue
		}
		gotResult := completed.ToolResults[index]
		wantResult := cloneStoredToolResult(*execution.Result)
		if wantResult.Unknown {
			wantResult.Delegation = interruptedDelegationFromState(s, call.ID)
		}
		gotResultJSON, err := json.Marshal(gotResult)
		if err != nil {
			panic(err)
		}
		wantResultJSON, err := json.Marshal(wantResult)
		if err != nil {
			panic(err)
		}
		if !bytes.Equal(gotResultJSON, wantResultJSON) {
			return fmt.Errorf("completed tool result %q does not match its durable completion", call.ID)
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
	var turnConfiguration requestConfiguration
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
		case recordOptionChanged:
			var value optionChanged
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			configuration = value.Configuration
			updates = append(updates, acp.ConfigOptionUpdate{
				SessionUpdate: "config_option_update",
				ConfigOptions: cloneConfigOptions(value.Options),
			})
		case recordTodoChanged:
			var value todoChanged
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			updates = append(updates, acp.Plan{
				SessionUpdate: acp.SessionUpdatePlan,
				Entries:       clonePlanEntries(value.Entries),
			})
		case recordCompaction:
			var value compactionRecord
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			if value.Usage != nil {
				cost += value.Usage.Cost
			}
			effective := configuration
			if turnConfiguration.Settings.Model != "" {
				effective = turnConfiguration
			}
			if effective.ContextWindow > 0 {
				updates = append(updates, acp.UsageUpdate{
					SessionUpdate: "usage_update",
					Used:          uint64(value.Occupancy),
					Size:          uint64(effective.ContextWindow),
					Cost:          &acp.Cost{Amount: cost, Currency: "USD"},
				})
			}
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
			turnConfiguration = value.Configuration
			if turnConfiguration.Settings.Model == "" {
				turnConfiguration = configuration
			}
		case recordExchangePaused:
			var value suspendedModelExchangeRecord
			if err := decodeRecord(record.Data, &value); err != nil {
				return nil, err
			}
			updates = append(updates, a.replaySuspendedExchange(value, turnConfiguration, s.cwd)...)
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
						replayToolCall(a, call, turnConfiguration, parentMeta, s.cwd, result.Target),
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
								Title:         a.configuredToolTitle(turnConfiguration, child.Name, child.Arguments),
								Name:          child.Name,
								Kind:          turnConfiguration.ToolKinds[child.Name],
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
			}
			if value.Usage != nil && turnConfiguration.ContextWindow > 0 {
				updates = append(updates, acp.UsageUpdate{
					SessionUpdate: "usage_update",
					Used:          uint64(value.Usage.PromptTokens),
					Size:          uint64(turnConfiguration.ContextWindow),
					Cost:          &acp.Cost{Amount: cost, Currency: "USD"},
				})
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
			turnConfiguration = requestConfiguration{}
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
		Title:         a.configuredToolTitle(configuration, call.Function.Name, json.RawMessage(call.Function.Arguments)),
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
	value.PlanTools = cloneBoolMap(value.PlanTools)
	value.MCPTools = slices.Clone(value.MCPTools)
	value.Skills = cloneSkillReferences(value.Skills)
	return value
}

func cloneSkillReferences(values []skills.Reference) []skills.Reference {
	if len(values) == 0 {
		return nil
	}
	return slices.Clone(values)
}

func optionalConfiguration(value requestConfiguration) *requestConfiguration {
	if value.Settings.Model == "" {
		return nil
	}
	cloned := cloneConfiguration(value)
	return &cloned
}

func configurationValue(value *requestConfiguration) requestConfiguration {
	if value == nil {
		return requestConfiguration{}
	}
	return cloneConfiguration(*value)
}

func cloneSelections(value sessionSelections) sessionSelections {
	if value.Reasoning != nil {
		reasoning := *value.Reasoning
		value.Reasoning = &reasoning
	}
	return value
}

func cloneConfigOptions(values []acp.SessionConfigOption) []acp.SessionConfigOption {
	if values == nil {
		return nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}
	var cloned []acp.SessionConfigOption
	if err := json.Unmarshal(raw, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func clonePlanEntries(values []acp.PlanEntry) []acp.PlanEntry {
	if values == nil {
		return nil
	}
	cloned := make([]acp.PlanEntry, len(values))
	for index, value := range values {
		cloned[index] = value
		if value.Meta != nil {
			cloned[index].Meta = make(acp.Metadata, len(value.Meta))
			for key, metaValue := range value.Meta {
				cloned[index].Meta[key] = metaValue
			}
		}
	}
	return cloned
}

func validateTodoEntries(entries []acp.PlanEntry) error {
	if entries == nil {
		return errors.New("todo entries are required")
	}
	inProgress := 0
	for index, entry := range entries {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("todo item %d: %w", index+1, err)
		}
		if entry.Status == acp.PlanEntryStatusInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return errors.New("todo has more than one in-progress item")
	}
	return nil
}

func validateSelections(value sessionSelections) error {
	if value.Mode != "" && value.Mode != "code" && value.Mode != "plan" {
		return fmt.Errorf("invalid mode selection %q", value.Mode)
	}
	if value.Model != strings.TrimSpace(value.Model) {
		return errors.New("model selection must not have surrounding whitespace")
	}
	if value.Reasoning != nil && (*value.Reasoning == "" || *value.Reasoning != strings.TrimSpace(*value.Reasoning)) {
		return errors.New("reasoning selection must be a nonempty trimmed value")
	}
	return nil
}

func (s durableState) turnConfiguration() requestConfiguration {
	if s.openTurn != "" {
		return cloneConfiguration(s.openTurnConfiguration)
	}
	return cloneConfiguration(s.configuration)
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

func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	cloned := make(map[string]bool, len(values))
	for name, enabled := range values {
		cloned[name] = enabled
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
	if value.Mode == "" {
		value.Mode = modeCode
	}
	if value.Mode != modeCode && value.Mode != modePlan {
		return errors.New("configuration mode is invalid")
	}
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
	for name, enabled := range value.PlanTools {
		if !enabled {
			return fmt.Errorf("plan tool %q is disabled", name)
		}
		if _, exists := names[name]; !exists {
			return fmt.Errorf("plan tool names unknown tool %q", name)
		}
	}
	mcpNames := make(map[string]struct{}, len(value.MCPTools))
	for _, tool := range value.MCPTools {
		if tool.Name == "" || tool.ServerName == "" || tool.ToolName == "" || tool.Identity == "" {
			return errors.New("configuration contains invalid MCP tool evidence")
		}
		if _, exists := names[tool.Name]; !exists && value.Mode != modePlan {
			return fmt.Errorf("MCP tool evidence names unknown tool %q", tool.Name)
		}
		if _, exists := mcpNames[tool.Name]; exists {
			return fmt.Errorf("duplicate MCP tool evidence %q", tool.Name)
		}
		mcpNames[tool.Name] = struct{}{}
	}
	if err := skills.ValidateReferences(value.Skills); err != nil {
		return fmt.Errorf("configuration skills: %w", err)
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
