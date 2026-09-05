package trace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"sync"
	"testing"
)

func TestDisabledTraceDoesNothing(t *testing.T) {
	var disabled Trace
	turn := disabled.Turn("session", "turn")
	turn.Start()
	turn.Provider(ProviderPrimary, 1, 12, "").Complete("completed", "stop", Usage{}, 4)
	turn.ToolPending("call", "read", "")
	turn.ToolStarted("call", "read", "")
	turn.ToolCompleted("call", "read", "", "completed", 2)
	turn.PermissionRequested("call", "read", "")
	turn.PermissionDecided("call", "read", "", "allowed_once")
	turn.Complete("completed", "end_turn")
	if err := disabled.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceWritesCompleteVersionedCorrelatedLines(t *testing.T) {
	writer := &bufferCloser{}
	trace := newTrace(writer, nil)
	turn := trace.Turn("session", "turn")
	turn.Start()
	request := turn.Provider(ProviderPrimary, 1, 12, "parent")
	request.Complete("completed", "stop", Usage{1, 2, 3}, 4)
	turn.ToolPending("call", "read", "parent")
	turn.ToolStarted("call", "read", "parent")
	turn.ToolCompleted("call", "read", "parent", "completed", 5)
	turn.Complete("completed", "end_turn")

	for number, line := range bytes.Split(bytes.TrimSpace(writer.Bytes()), []byte{'\n'}) {
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatalf("line %d is not JSON: %v", number+1, err)
		}
		if value["version"] != float64(Version) || value["session_id"] != "session" || value["turn_id"] != "turn" {
			t.Fatalf("line %d correlation = %#v", number+1, value)
		}
		if value["timestamp_ms"].(float64) <= 0 {
			t.Fatalf("line %d timestamp = %#v", number+1, value["timestamp_ms"])
		}
	}
}

func TestConcurrentTraceWritesDoNotInterleave(t *testing.T) {
	writer := &fragmentingWriter{}
	trace := newTrace(writer, nil)
	turn := trace.Turn("session", "turn")
	var wait sync.WaitGroup
	for index := 0; index < 50; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			turn.ToolPending("call", "tool", "")
		}()
	}
	wait.Wait()
	lines := bytes.Split(bytes.TrimSpace(writer.Bytes()), []byte{'\n'})
	if len(lines) != 50 {
		t.Fatalf("lines = %d", len(lines))
	}
	for _, line := range lines {
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWriteFailureReportsOnceAndDisablesTrace(t *testing.T) {
	writer := &failingWriter{}
	var reports int
	trace := newTrace(writer, func(error) { reports++ })
	turn := trace.Turn("session", "turn")
	turn.Start()
	turn.Start()
	if reports != 1 {
		t.Fatalf("reports = %d", reports)
	}
	if writer.writes != 1 || writer.closes != 1 {
		t.Fatalf("writes = %d, closes = %d", writer.writes, writer.closes)
	}
}

type bufferCloser struct{ bytes.Buffer }

func (*bufferCloser) Close() error { return nil }

type fragmentingWriter struct {
	mu sync.Mutex
	bytes.Buffer
}

func (w *fragmentingWriter) Write(value []byte) (int, error) {
	for _, current := range value {
		w.mu.Lock()
		w.Buffer.WriteByte(current)
		w.mu.Unlock()
		runtime.Gosched()
	}
	return len(value), nil
}

func (*fragmentingWriter) Close() error { return nil }

type failingWriter struct {
	writes int
	closes int
}

func (w *failingWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("broken")
}

func (w *failingWriter) Close() error {
	w.closes++
	return nil
}

var _ io.WriteCloser = (*bufferCloser)(nil)
