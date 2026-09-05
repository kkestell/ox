package e2e

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestDiagnosticTraceRecordsTurnShapeWithoutContent(t *testing.T) {
	const (
		credential = "credential-trace-sentinel"
		promptText = "prompt-trace-sentinel"
		reasoning  = "reasoning-trace-sentinel"
		answer     = "answer-trace-sentinel"
		shellText  = "shell-trace-sentinel"
	)
	arguments := `{"command":"printf shell-trace-sentinel"}`
	model := startModel(t,
		sse(
			evToolCall(0, "call-shell", "function", "shell", arguments),
			evFinishReason("tool_calls"),
			evUsage(11, 7, 18),
		),
		sse(
			evReasoning(reasoning),
			evText(answer),
			evFinishReason("stop"),
			evUsage(19, 5, 24),
		),
	)
	child := start(t,
		withModel(model),
		withCredential(credential),
		withArguments("--trace", "trace.jsonl"),
	)
	initialize(t, child)
	session := newSession(t, child, child.cwd)
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt(promptText),
	})
	allowPermission(t, child, "call-shell", true)
	response := promptResponse(t, child.result(child.await(turn)))
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	child.stop()

	path := filepath.Join(child.cwd, "trace.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{credential, promptText, reasoning, answer, shellText, arguments} {
		if strings.Contains(string(data), sentinel) {
			t.Errorf("trace contains private sentinel %q", sentinel)
		}
	}
	records := readTrace(t, path)
	wantTypes := []string{
		"turn_started",
		"provider_request_started",
		"provider_request_completed",
		"tool_pending",
		"permission_requested",
		"permission_decided",
		"tool_started",
		"tool_completed",
		"provider_request_started",
		"provider_request_completed",
		"turn_completed",
	}
	gotTypes := make([]string, len(records))
	turnID := stringField(t, records[0], "turn_id")
	for index, record := range records {
		gotTypes[index] = stringField(t, record, "type")
		if record["version"] != float64(1) || stringField(t, record, "session_id") != session ||
			stringField(t, record, "turn_id") != turnID || numberField(t, record, "timestamp_ms") <= 0 {
			t.Fatalf("record %d lacks trace correlation: %#v", index, record)
		}
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types = %#v, want %#v", gotTypes, wantTypes)
	}
	if stringField(t, records[1], "provider_kind") != "primary" ||
		stringField(t, records[1], "provider_request_id") != stringField(t, records[2], "provider_request_id") ||
		numberField(t, records[1], "request_count") != 1 ||
		numberField(t, records[1], "request_bytes") <= 0 ||
		numberField(t, records[2], "input_tokens") != 11 ||
		numberField(t, records[2], "output_tokens") != 7 {
		t.Fatalf("first provider request = %#v / %#v", records[1], records[2])
	}
	for _, index := range []int{3, 4, 5, 6, 7} {
		if stringField(t, records[index], "tool_call_id") != "call-shell" ||
			stringField(t, records[index], "tool_name") != "shell" {
			t.Fatalf("tool record %d = %#v", index, records[index])
		}
	}
	if stringField(t, records[5], "outcome") != "allow_once" ||
		stringField(t, records[7], "outcome") != "completed" ||
		numberField(t, records[7], "output_bytes") == 0 ||
		numberField(t, records[7], "elapsed_ms") < 0 {
		t.Fatalf("tool outcomes = %#v / %#v", records[5], records[7])
	}
	if stringField(t, records[10], "outcome") != "completed" ||
		stringField(t, records[10], "stop_reason") != string(acp.StopReasonEndTurn) ||
		numberField(t, records[10], "elapsed_ms") < 0 {
		t.Fatalf("turn outcome = %#v", records[10])
	}
}

func TestDiagnosticTraceFileLifecycleAndArguments(t *testing.T) {
	t.Run("replaces existing file", func(t *testing.T) {
		child := start(t,
			withFile("trace.jsonl", "old trace sentinel"),
			withArguments("--trace", "trace.jsonl"),
		)
		child.stop()
		data, err := os.ReadFile(filepath.Join(child.cwd, "trace.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 0 {
			t.Fatalf("trace after startup and shutdown = %q", data)
		}
	})

	t.Run("unavailable path fails before serving", func(t *testing.T) {
		result := runCommand(t, "", []string{"--trace", "missing/trace.jsonl"})
		if result.ExitCode != 1 || result.Stdout != "" ||
			!strings.Contains(result.Stderr, "create diagnostic trace") {
			t.Fatalf("result = %#v", result)
		}
	})

	for _, arguments := range [][]string{
		{"--trace"},
		{"--trace", "trace.jsonl", "extra"},
		{"--trace", ""},
		{"login", "--trace", "trace.jsonl"},
	} {
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			result := runCommand(t, "", arguments)
			if result.ExitCode != 2 ||
				!strings.Contains(result.Stderr, "usage: ox [") {
				t.Fatalf("result = %#v", result)
			}
		})
	}

	t.Run("omitted", func(t *testing.T) {
		child := start(t)
		child.stop()
		if _, err := os.Stat(filepath.Join(child.cwd, "trace.jsonl")); !os.IsNotExist(err) {
			t.Fatalf("trace exists without flag: %v", err)
		}
	})
}

func TestDiagnosticTraceClassifiesCompactionSubagentAndCancellation(t *testing.T) {
	const (
		summarySentinel  = "summary-trace-sentinel"
		subagentSentinel = "subagent-trace-sentinel"
		cancelSentinel   = "cancel-trace-sentinel"
	)
	oldAnswer := strings.Repeat("old trace detail ", 240)
	recentAnswer := strings.Repeat("recent trace answer ", 100)
	taskArguments := `{"description":"inspect","prompt":"subagent-trace-sentinel"}`
	held := (*modelResponse)(nil)
	model := startModel(t,
		sse(evText(oldAnswer), evFinishReason("stop"), evUsage(100, 600, 700)),
		sse(evText(recentAnswer), evFinishReason("stop"), evUsage(800, 5, 805)),
		sse(evText(summarySentinel), evFinishReason("stop"), evUsage(650, 20, 670)),
		sse(
			evToolCall(0, "call-task", "function", "task", taskArguments),
			evFinishReason("tool_calls"), evUsage(100, 5, 105),
		),
		sse(evText("subagent final"), evFinishReason("stop"), evUsage(40, 4, 44)),
		sse(evText("primary final"), evFinishReason("stop"), evUsage(90, 3, 93)),
	)
	held = model.holdFor("cancel this turn", frames(evReasoning(cancelSentinel)))
	child := start(t,
		withModel(model),
		withModelContextWindow(model, 14800),
		withArguments("--trace", "trace.jsonl"),
	)
	initialize(t, child)
	session := newSession(t, child, child.cwd)
	for _, text := range []string{"first trace prompt", "second trace prompt", "delegate trace prompt"} {
		prompt(t, child, session, text)
		_ = updates(t, child, session)
	}

	cancelled := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("cancel this turn"),
	})
	held.await(t)
	child.notify("session/cancel", acp.CancelNotification{SessionID: session})
	response := promptResponse(t, child.result(child.await(cancelled)))
	if response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("cancelled stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	child.stop()

	path := filepath.Join(child.cwd, "trace.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{summarySentinel, subagentSentinel, cancelSentinel, taskArguments} {
		if strings.Contains(string(data), sentinel) {
			t.Errorf("trace contains private sentinel %q", sentinel)
		}
	}

	records := readTrace(t, path)
	var compaction, subagent, cancelledProvider, cancelledTurn bool
	for _, record := range records {
		switch record["type"] {
		case "provider_request_completed":
			switch record["provider_kind"] {
			case "compaction":
				compaction = true
			case "subagent":
				if record["parent_tool_call_id"] == "call-task" {
					subagent = true
				}
			}
			if record["outcome"] == "cancelled" {
				cancelledProvider = true
			}
		case "turn_completed":
			if record["outcome"] == "cancelled" &&
				record["stop_reason"] == string(acp.StopReasonCancelled) {
				cancelledTurn = true
			}
		}
	}
	if !compaction || !subagent || !cancelledProvider || !cancelledTurn {
		t.Fatalf(
			"trace classifications: compaction=%t subagent=%t provider cancellation=%t turn cancellation=%t",
			compaction, subagent, cancelledProvider, cancelledTurn,
		)
	}
}

func TestDiagnosticTraceRecordsOnlyLiveRecoveredWork(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t,
		shellToolCallResponse("true"),
		sse(evText("recovered answer"), evFinishReason("stop")),
	)
	options := []startOption{
		withModel(model),
		withEnvironment("XDG_DATA_HOME", dataDir),
	}
	first := start(t, options...)
	initialize(t, first)
	session := newSession(t, first, first.cwd)
	cwd := first.cwd
	_ = first.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("recover traced permission"),
	})
	_ = permissionRequest(t, first.serverRequest(), "call-shell")
	first.kill()

	recoveredOptions := append(
		append([]startOption(nil), options...),
		withArguments("--trace", "trace.jsonl"),
	)
	recovered := start(t, recoveredOptions...)
	initialize(t, recovered)
	load := recovered.begin("session/load", acp.LoadSessionRequest{
		SessionID: session, CWD: cwd, MCPServers: []json.RawMessage{},
	})
	permission := recovered.serverRequest()
	_ = permissionRequest(t, permission, "call-shell")
	recovered.respond(permission, acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{
			Outcome: "selected", OptionID: "reject_once",
		},
	})
	_ = recovered.result(recovered.await(load))
	_ = updates(t, recovered, session)
	recovered.stop()

	records := readTrace(t, filepath.Join(recovered.cwd, "trace.jsonl"))
	var types []string
	for _, record := range records {
		types = append(types, stringField(t, record, "type"))
	}
	if !reflect.DeepEqual(types, []string{
		"turn_started",
		"permission_requested",
		"permission_decided",
		"tool_completed",
		"provider_request_started",
		"provider_request_completed",
		"turn_completed",
	}) {
		t.Fatalf("recovered trace types = %#v", types)
	}
	if records[2]["outcome"] != "refused" || records[3]["outcome"] != "refused" {
		t.Fatalf("recovered rejection = %#v / %#v", records[2], records[3])
	}
}

func readTrace(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode trace line: %v", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func stringField(t *testing.T, record map[string]any, name string) string {
	t.Helper()
	value, ok := record[name].(string)
	if !ok || value == "" {
		t.Fatalf("%s = %#v in %#v", name, record[name], record)
	}
	return value
}

func numberField(t *testing.T, record map[string]any, name string) float64 {
	t.Helper()
	value, ok := record[name].(float64)
	if !ok {
		t.Fatalf("%s = %#v in %#v", name, record[name], record)
	}
	return value
}
