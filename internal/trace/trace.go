// Package trace emits a lossy, content-free diagnostic view of live turns.
package trace

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const Version = 1

type ProviderKind string

const (
	ProviderPrimary    ProviderKind = "primary"
	ProviderCompaction ProviderKind = "compaction"
	ProviderSubagent   ProviderKind = "subagent"
)

type Usage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
}

// Trace is a process-scoped sink. Its zero value is disabled.
type Trace struct {
	state *state
}

type state struct {
	mu       sync.Mutex
	writer   io.WriteCloser
	onError  func(error)
	disabled bool
	reported bool
	nextID   atomic.Uint64
	spansMu  sync.Mutex
	tools    map[toolKey]time.Time
}

type toolKey struct {
	session string
	turn    string
	call    string
}

// Open creates or replaces path with an owner-only JSONL trace.
func Open(path string, onError func(error)) (Trace, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return Trace{}, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return Trace{}, err
	}
	return newTrace(file, onError), nil
}

func newTrace(writer io.WriteCloser, onError func(error)) Trace {
	return Trace{state: &state{
		writer: writer, onError: onError, tools: make(map[toolKey]time.Time),
	}}
}

// Close closes the trace file. Closing a disabled trace does nothing.
func (t Trace) Close() error {
	if t.state == nil {
		return nil
	}
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	if t.state.disabled {
		return nil
	}
	t.state.disabled = true
	return t.state.writer.Close()
}

// Turn derives one live turn scope from the process sink.
func (t Trace) Turn(sessionID, turnID string) Turn {
	return Turn{
		trace: t, sessionID: sessionID, turnID: turnID,
		started: time.Now(), complete: new(sync.Once),
	}
}

// Turn is a session-and-turn-scoped trace. Its zero value is disabled.
type Turn struct {
	trace     Trace
	sessionID string
	turnID    string
	started   time.Time
	complete  *sync.Once
}

func (t Turn) Start() {
	t.emit(record{Type: "turn_started"})
}

func (t Turn) Complete(outcome, stopReason string) {
	if t.complete == nil {
		return
	}
	t.complete.Do(func() {
		elapsed := elapsedMilliseconds(t.started)
		t.emit(record{
			Type: "turn_completed", Outcome: outcome, StopReason: stopReason,
			ElapsedMS: &elapsed,
		})
	})
}

func (t Turn) Provider(kind ProviderKind, requestCount, requestBytes int, parentCallID string) ProviderRequest {
	if t.trace.state == nil {
		return ProviderRequest{}
	}
	id := fmt.Sprintf("provider-%d", t.trace.state.nextID.Add(1))
	bytes := int64(requestBytes)
	t.emit(record{
		Type: "provider_request_started", ProviderKind: kind,
		ProviderRequestID: id, RequestCount: requestCount,
		ParentToolCallID: parentCallID, RequestBytes: &bytes,
	})
	return ProviderRequest{
		turn: t, id: id, kind: kind, requestCount: requestCount,
		parentCallID: parentCallID, started: time.Now(), complete: new(sync.Once),
	}
}

type ProviderRequest struct {
	turn         Turn
	id           string
	kind         ProviderKind
	requestCount int
	parentCallID string
	started      time.Time
	complete     *sync.Once
}

func (r ProviderRequest) Complete(outcome, stopReason string, usage Usage, responseBytes int) {
	if r.complete == nil {
		return
	}
	r.complete.Do(func() {
		elapsed := elapsedMilliseconds(r.started)
		bytes := int64(responseBytes)
		input := int64(usage.InputTokens)
		output := int64(usage.OutputTokens)
		reasoning := int64(usage.ReasoningTokens)
		r.turn.emit(record{
			Type: "provider_request_completed", ProviderKind: r.kind,
			ProviderRequestID: r.id, RequestCount: r.requestCount,
			ParentToolCallID: r.parentCallID, Outcome: outcome,
			StopReason: stopReason, ElapsedMS: &elapsed, ResponseBytes: &bytes,
			InputTokens: &input, OutputTokens: &output, ReasoningTokens: &reasoning,
		})
	})
}

func (t Turn) ToolPending(callID, name, parentCallID string) {
	t.emit(record{
		Type: "tool_pending", ToolCallID: callID, ToolName: name,
		ParentToolCallID: parentCallID,
	})
}

func (t Turn) ToolStarted(callID, name, parentCallID string) {
	if t.trace.state != nil {
		t.trace.state.spansMu.Lock()
		t.trace.state.tools[toolKey{t.sessionID, t.turnID, callID}] = time.Now()
		t.trace.state.spansMu.Unlock()
	}
	t.emit(record{
		Type: "tool_started", ToolCallID: callID, ToolName: name,
		ParentToolCallID: parentCallID,
	})
}

func (t Turn) ToolCompleted(callID, name, parentCallID, outcome string, outputBytes int) {
	started := time.Time{}
	if t.trace.state != nil {
		key := toolKey{t.sessionID, t.turnID, callID}
		t.trace.state.spansMu.Lock()
		started = t.trace.state.tools[key]
		delete(t.trace.state.tools, key)
		t.trace.state.spansMu.Unlock()
	}
	elapsed := elapsedMilliseconds(started)
	bytes := int64(outputBytes)
	t.emit(record{
		Type: "tool_completed", ToolCallID: callID, ToolName: name,
		ParentToolCallID: parentCallID, Outcome: outcome,
		ElapsedMS: &elapsed, OutputBytes: &bytes,
	})
}

func (t Turn) PermissionRequested(callID, name, parentCallID string) {
	t.emit(record{
		Type: "permission_requested", ToolCallID: callID, ToolName: name,
		ParentToolCallID: parentCallID,
	})
}

func (t Turn) PermissionDecided(callID, name, parentCallID, outcome string) {
	t.emit(record{
		Type: "permission_decided", ToolCallID: callID, ToolName: name,
		ParentToolCallID: parentCallID, Outcome: outcome,
	})
}

type record struct {
	Version           int          `json:"version"`
	Type              string       `json:"type"`
	TimestampMS       int64        `json:"timestamp_ms"`
	SessionID         string       `json:"session_id"`
	TurnID            string       `json:"turn_id"`
	ProviderKind      ProviderKind `json:"provider_kind,omitempty"`
	ProviderRequestID string       `json:"provider_request_id,omitempty"`
	RequestCount      int          `json:"request_count,omitempty"`
	ToolCallID        string       `json:"tool_call_id,omitempty"`
	ParentToolCallID  string       `json:"parent_tool_call_id,omitempty"`
	ToolName          string       `json:"tool_name,omitempty"`
	Outcome           string       `json:"outcome,omitempty"`
	StopReason        string       `json:"stop_reason,omitempty"`
	ElapsedMS         *int64       `json:"elapsed_ms,omitempty"`
	RequestBytes      *int64       `json:"request_bytes,omitempty"`
	ResponseBytes     *int64       `json:"response_bytes,omitempty"`
	OutputBytes       *int64       `json:"output_bytes,omitempty"`
	InputTokens       *int64       `json:"input_tokens,omitempty"`
	OutputTokens      *int64       `json:"output_tokens,omitempty"`
	ReasoningTokens   *int64       `json:"reasoning_tokens,omitempty"`
}

func (t Turn) emit(value record) {
	if t.trace.state == nil {
		return
	}
	value.Version = Version
	value.TimestampMS = time.Now().UnixMilli()
	value.SessionID = t.sessionID
	value.TurnID = t.turnID
	t.trace.write(value)
}

func (t Trace) write(value record) {
	line, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	line = append(line, '\n')

	var report func(error)
	var reportErr error
	t.state.mu.Lock()
	if !t.state.disabled {
		n, err := t.state.writer.Write(line)
		if err == nil && n != len(line) {
			err = io.ErrShortWrite
		}
		if err != nil {
			t.state.disabled = true
			_ = t.state.writer.Close()
			if !t.state.reported {
				t.state.reported = true
				report = t.state.onError
				reportErr = err
			}
		}
	}
	t.state.mu.Unlock()
	if report != nil {
		report(reportErr)
	}
}

func elapsedMilliseconds(started time.Time) int64 {
	if started.IsZero() {
		return 0
	}
	elapsed := time.Since(started).Milliseconds()
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
