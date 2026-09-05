package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

const (
	conformanceFile    = "executor-fixture.txt"
	conformanceMarker  = "executor-marker.txt"
	conformanceBefore  = "before\n"
	conformanceAfter   = "after\n"
	conformanceCommand = "printf shell-output | tee " + conformanceMarker
)

type executorSnapshot struct {
	toolResults  []string
	capabilities []executorCapabilitySnapshot
	records      []any
	replay       []any
}

type executorCapabilitySnapshot struct {
	FileSystemRead  bool `json:"fileSystemRead"`
	FileSystemWrite bool `json:"fileSystemWrite"`
	Terminal        bool `json:"terminal"`
}

type executorNormalizer struct {
	root       string
	identities map[string]map[string]string
}

func TestLocalAndDelegatedExecutorsHaveEqualDurableBehavior(t *testing.T) {
	var local, delegated executorSnapshot
	t.Run("local", func(t *testing.T) {
		local = runExecutorConformance(t, false)
	})
	t.Run("delegated", func(t *testing.T) {
		delegated = runExecutorConformance(t, true)
	})

	if !reflect.DeepEqual(local.toolResults, delegated.toolResults) {
		t.Fatalf("model-visible tool results differ\nlocal:     %#v\ndelegated: %#v",
			local.toolResults, delegated.toolResults)
	}
	assertExecutorCapabilitySnapshots(t, local.capabilities, executorCapabilitySnapshot{})
	assertExecutorCapabilitySnapshots(t, delegated.capabilities, executorCapabilitySnapshot{
		FileSystemRead:  true,
		FileSystemWrite: true,
		Terminal:        true,
	})
	if reflect.DeepEqual(local.capabilities, delegated.capabilities) {
		t.Fatal("local and delegated logs recorded equal executor capabilities")
	}
	if !reflect.DeepEqual(local.records, delegated.records) {
		localJSON, _ := json.MarshalIndent(local.records, "", "  ")
		delegatedJSON, _ := json.MarshalIndent(delegated.records, "", "  ")
		t.Fatalf("durable records differ\nlocal:\n%s\ndelegated:\n%s", localJSON, delegatedJSON)
	}
	if !reflect.DeepEqual(local.replay, delegated.replay) {
		localJSON, _ := json.MarshalIndent(local.replay, "", "  ")
		delegatedJSON, _ := json.MarshalIndent(delegated.replay, "", "  ")
		t.Fatalf("session/load replay differs\nlocal:\n%s\ndelegated:\n%s", localJSON, delegatedJSON)
	}
}

func TestExecutorCapabilitiesChangeOnceAcrossReactivation(t *testing.T) {
	model := startModel(t,
		sse(evText("baseline answer"), evFinishReason("stop")),
		toolResponse("reactivated-read", "read_file", `{"path":"`+conformanceFile+`"}`),
		toolResponse("reactivated-edit", "edit_file", `{"path":"`+conformanceFile+`","old_string":"before","new_string":"after"}`),
		toolResponse("reactivated-shell", "shell", `{"command":"`+conformanceCommand+`"}`),
		sse(evText("reactivated workflow complete"), evFinishReason("stop")),
	)
	dataDir := t.TempDir()
	options := []startOption{
		withModel(model),
		withEnvironment("XDG_DATA_HOME", dataDir),
		withFile(conformanceFile, conformanceBefore),
	}

	first := start(t, options...)
	initializeWithCapabilities(t, first, nil)
	session := newSession(t, first, first.cwd)
	cwd := first.cwd
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	prompt(t, first, session, "establish the baseline transcript")
	_ = updates(t, first, session)
	first.request("session/close", acp.CloseSessionRequest{SessionID: session})
	first.stop()

	beforeChange := loadExecutorReplay(t, options, nil, session, cwd, root)
	localRecords, _ := readNormalizedSessionLog(t, dataDir, session, root)
	if kinds := sessionRecordKinds(t, localRecords); slices.Contains(kinds, "request_configuration_changed") {
		t.Fatalf("local reactivation changed configuration: %#v", kinds)
	}
	delegatedCapabilities := executorCapabilities(true)
	afterChange := loadExecutorReplay(
		t, options, delegatedCapabilities, session, cwd, root,
	)
	if !reflect.DeepEqual(afterChange, beforeChange) {
		t.Fatalf("capability change altered replay\nbefore: %#v\nafter:  %#v",
			beforeChange, afterChange)
	}

	delegated := start(t, options...)
	initializeWithCapabilities(t, delegated, delegatedCapabilities)
	receivedAt := len(delegated.received)
	loadSession(t, delegated, session, cwd)
	sameCapabilitiesReplay := normalizedReplay(t, delegated.received[receivedAt:], root)
	_ = updates(t, delegated, session)
	if !reflect.DeepEqual(sameCapabilitiesReplay, afterChange) {
		t.Fatalf("same-capability reactivation altered replay\nfirst:  %#v\nsecond: %#v",
			afterChange, sameCapabilitiesReplay)
	}

	fixturePath := filepath.Join(root, conformanceFile)
	clientFile := conformanceBefore
	clientMarker := ""
	turn := delegated.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("run the reactivated executor workflow"),
	})
	read := delegated.serverRequest()
	assertReadRequest(t, read, session, fixturePath)
	delegated.respond(read, acp.ReadTextFileResponse{Content: clientFile})

	allowPermission(t, delegated, "reactivated-edit", true)
	read = delegated.serverRequest()
	assertReadRequest(t, read, session, fixturePath)
	delegated.respond(read, acp.ReadTextFileResponse{Content: clientFile})
	write := delegated.serverRequest()
	clientFile = assertWriteRequest(t, write, session, fixturePath, conformanceAfter)
	delegated.respond(write, acp.WriteTextFileResponse{})

	allowPermission(t, delegated, "reactivated-shell", true)
	terminalRoundTripAtCWD(
		t,
		delegated,
		session,
		cwd,
		conformanceCommand,
		acp.WaitForTerminalExitResponse{ExitCode: intPointer(0)},
		acp.TerminalOutputResponse{Output: "shell-output"},
	)
	clientMarker = "shell-output"
	response := promptResponse(t, delegated.result(delegated.await(turn)))
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want %q", response.StopReason, acp.StopReasonEndTurn)
	}
	_ = updates(t, delegated, session)
	delegated.request("session/close", acp.CloseSessionRequest{SessionID: session})
	delegated.stop()

	if local := readConformanceFile(t, filepath.Join(cwd, conformanceFile)); local != conformanceBefore {
		t.Fatalf("delegated reactivation changed local file to %q", local)
	}
	if marker, exists := optionalConformanceFile(t, filepath.Join(cwd, conformanceMarker)); exists {
		t.Fatalf("delegated reactivation created local marker %q", marker)
	}
	if clientFile != conformanceAfter || clientMarker != "shell-output" {
		t.Fatalf("delegated client state = file %q, marker %q", clientFile, clientMarker)
	}

	records, capabilities := readNormalizedSessionLog(t, dataDir, session, root)
	wantCapabilities := []executorCapabilitySnapshot{
		{},
		{},
		{},
		{FileSystemRead: true, FileSystemWrite: true, Terminal: true},
		{FileSystemRead: true, FileSystemWrite: true, Terminal: true},
		{FileSystemRead: true, FileSystemWrite: true, Terminal: true},
	}
	if !reflect.DeepEqual(capabilities, wantCapabilities) {
		t.Fatalf("executor capability snapshots = %#v, want %#v", capabilities, wantCapabilities)
	}
	kinds := sessionRecordKinds(t, records)
	changeIndex := slices.Index(kinds, "request_configuration_changed")
	if changeIndex < 0 || slices.Contains(kinds[changeIndex+1:], "request_configuration_changed") {
		t.Fatalf("record kinds contain more than one configuration change: %#v", kinds)
	}
	if changeIndex+1 >= len(kinds) || kinds[changeIndex+1] != "user_message" {
		t.Fatalf("configuration change does not precede the next turn: %#v", kinds)
	}
}

func loadExecutorReplay(
	t *testing.T,
	options []startOption,
	capabilities *acp.ClientCapabilities,
	session string,
	cwd string,
	root string,
) []any {
	t.Helper()
	child := start(t, options...)
	initializeWithCapabilities(t, child, capabilities)
	receivedAt := len(child.received)
	loadSession(t, child, session, cwd)
	replay := normalizedReplay(t, child.received[receivedAt:], root)
	_ = updates(t, child, session)
	child.request("session/close", acp.CloseSessionRequest{SessionID: session})
	child.stop()
	return replay
}

func runExecutorConformance(t *testing.T, delegated bool) executorSnapshot {
	t.Helper()
	model := startModel(t,
		toolResponse("conformance-read", "read_file", `{"path":"`+conformanceFile+`"}`),
		toolResponse("conformance-edit", "edit_file", `{"path":"`+conformanceFile+`","old_string":"before","new_string":"after"}`),
		toolResponse("conformance-shell", "shell", `{"command":"`+conformanceCommand+`"}`),
		sse(evText("executor workflow complete"), evFinishReason("stop")),
	)
	dataDir := t.TempDir()
	options := []startOption{
		withModel(model),
		withEnvironment("XDG_DATA_HOME", dataDir),
		withFile(conformanceFile, conformanceBefore),
	}
	capabilities := executorCapabilities(delegated)
	child := start(t, options...)
	initializeWithCapabilities(t, child, capabilities)
	session := newSession(t, child, child.cwd)
	root, err := filepath.EvalSymlinks(child.cwd)
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(root, conformanceFile)
	clientFile := conformanceBefore
	clientMarker := ""

	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("run the executor conformance workflow"),
	})
	if delegated {
		read := child.serverRequest()
		assertReadRequest(t, read, session, fixturePath)
		child.respond(read, acp.ReadTextFileResponse{Content: clientFile})
	}

	allowPermission(t, child, "conformance-edit", true)
	if delegated {
		read := child.serverRequest()
		assertReadRequest(t, read, session, fixturePath)
		child.respond(read, acp.ReadTextFileResponse{Content: clientFile})
		write := child.serverRequest()
		clientFile = assertWriteRequest(t, write, session, fixturePath, conformanceAfter)
		child.respond(write, acp.WriteTextFileResponse{})
	}

	allowPermission(t, child, "conformance-shell", true)
	if delegated {
		terminalRoundTrip(
			t,
			child,
			session,
			conformanceCommand,
			acp.WaitForTerminalExitResponse{ExitCode: intPointer(0)},
			acp.TerminalOutputResponse{Output: "shell-output"},
		)
		clientMarker = "shell-output"
	}
	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want %q", response.StopReason, acp.StopReasonEndTurn)
	}
	_ = updates(t, child, session)

	requests := model.requests()
	if len(requests) != 4 {
		t.Fatalf("model requests = %d, want 4", len(requests))
	}
	toolResults := make([]string, 0, 3)
	for _, request := range requests[1:] {
		toolResults = append(toolResults, request.Messages[len(request.Messages)-1].text())
	}
	wantResults := []string{
		conformanceBefore,
		"Edited " + conformanceFile + " (1 replacement(s))",
		"exit code: 0\nshell-output",
	}
	if !reflect.DeepEqual(toolResults, wantResults) {
		t.Fatalf("model-visible tool results = %#v, want %#v", toolResults, wantResults)
	}

	localFile := readConformanceFile(t, filepath.Join(child.cwd, conformanceFile))
	localMarker, markerExists := optionalConformanceFile(t, filepath.Join(child.cwd, conformanceMarker))
	if delegated {
		if localFile != conformanceBefore || markerExists {
			t.Fatalf("delegated workflow changed local state: file = %q, marker = %q", localFile, localMarker)
		}
		if clientFile != conformanceAfter || clientMarker != "shell-output" {
			t.Fatalf("delegated client state = file %q, marker %q", clientFile, clientMarker)
		}
	} else if localFile != conformanceAfter || !markerExists || localMarker != "shell-output" {
		t.Fatalf("local workflow state = file %q, marker %q", localFile, localMarker)
	}

	child.request("session/close", acp.CloseSessionRequest{SessionID: session})
	child.stop()
	records, capabilitySnapshots := readNormalizedSessionLog(t, dataDir, session, root)

	restarted := start(t, options...)
	initializeWithCapabilities(t, restarted, capabilities)
	receivedAt := len(restarted.received)
	loadSession(t, restarted, session, child.cwd)
	replay := normalizedReplay(t, restarted.received[receivedAt:], root)
	_ = updates(t, restarted, session)
	restarted.request("session/close", acp.CloseSessionRequest{SessionID: session})
	restarted.stop()
	if len(replay) == 0 {
		t.Fatal("session/load replayed no updates")
	}

	return executorSnapshot{
		toolResults:  toolResults,
		capabilities: capabilitySnapshots,
		records:      records,
		replay:       replay,
	}
}

func executorCapabilities(delegated bool) *acp.ClientCapabilities {
	if !delegated {
		return nil
	}
	return &acp.ClientCapabilities{
		FS:       &acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		Terminal: true,
	}
}

func readConformanceFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func optionalConformanceFile(t *testing.T, path string) (string, bool) {
	t.Helper()
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(content), true
}

func readNormalizedSessionLog(
	t *testing.T,
	dataDir string,
	session string,
	root string,
) ([]any, []executorCapabilitySnapshot) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dataDir, "ox", "sessions", session+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var records []any
	var capabilities []executorCapabilitySnapshot
	normalizer := newExecutorNormalizer(root)
	for _, line := range bytes.Split(bytes.TrimSpace(content), []byte{'\n'}) {
		var record any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode session record: %v", err)
		}
		capabilities = append(capabilities, removeExecutorCapabilities(t, record)...)
		records = append(records, normalizer.normalize(record))
	}
	return records, capabilities
}

func removeExecutorCapabilities(t *testing.T, record any) []executorCapabilitySnapshot {
	t.Helper()
	recordMap, ok := record.(map[string]any)
	if !ok {
		t.Fatalf("session record = %T, want object", record)
	}
	kind, _ := recordMap["type"].(string)
	data, _ := recordMap["data"].(map[string]any)
	var configurations []map[string]any
	switch kind {
	case "session_created", "request_configuration_changed", "session_config_option_changed", "user_message":
		configuration, _ := data["configuration"].(map[string]any)
		configurations = append(configurations, configuration)
	case "checkpoint":
		state, _ := data["state"].(map[string]any)
		configuration, _ := state["configuration"].(map[string]any)
		configurations = append(configurations, configuration)
		if open, ok := state["openTurnConfiguration"].(map[string]any); ok {
			configurations = append(configurations, open)
		}
	default:
		return nil
	}
	result := make([]executorCapabilitySnapshot, 0, len(configurations))
	for _, configuration := range configurations {
		if configuration == nil {
			t.Fatalf("%s record has no configuration", kind)
		}
		raw, exists := configuration["executorCapabilities"]
		if !exists {
			t.Fatalf("%s record has no executor capabilities", kind)
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var capabilities executorCapabilitySnapshot
		if err := json.Unmarshal(encoded, &capabilities); err != nil {
			t.Fatalf("decode %s executor capabilities: %v", kind, err)
		}
		delete(configuration, "executorCapabilities")
		result = append(result, capabilities)
	}
	return result
}

func assertExecutorCapabilitySnapshots(
	t *testing.T,
	values []executorCapabilitySnapshot,
	want executorCapabilitySnapshot,
) {
	t.Helper()
	if len(values) == 0 {
		t.Fatal("session log contains no executor capability snapshots")
	}
	for index, value := range values {
		if value != want {
			t.Fatalf("executor capability snapshot %d = %#v, want %#v", index, value, want)
		}
	}
}

func sessionRecordKinds(t *testing.T, records []any) []string {
	t.Helper()
	kinds := make([]string, len(records))
	for index, record := range records {
		recordMap, ok := record.(map[string]any)
		if !ok {
			t.Fatalf("session record %d = %T, want object", index, record)
		}
		kind, ok := recordMap["type"].(string)
		if !ok {
			t.Fatalf("session record %d has no type", index)
		}
		kinds[index] = kind
	}
	return kinds
}

func normalizedReplay(t *testing.T, received []message, root string) []any {
	t.Helper()
	var replay []any
	normalizer := newExecutorNormalizer(root)
	for _, current := range received {
		if current.Method != "session/update" || len(current.ID) != 0 {
			continue
		}
		var notification any
		if err := json.Unmarshal(current.raw, &notification); err != nil {
			t.Fatalf("decode replay notification: %v", err)
		}
		replay = append(replay, normalizer.normalize(notification))
	}
	return replay
}

func newExecutorNormalizer(root string) *executorNormalizer {
	return &executorNormalizer{
		root:       root,
		identities: make(map[string]map[string]string),
	}
}

func (n *executorNormalizer) normalize(value any) any {
	switch value := value.(type) {
	case []any:
		for index := range value {
			value[index] = n.normalize(value[index])
		}
		return value
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := value[key]
			switch key {
			case "sessionId":
				value[key] = n.normalizeIdentity("session", child)
			case "turnId":
				value[key] = n.normalizeIdentity("turn", child)
			case "messageId", "answerId", "thoughtId":
				value[key] = n.normalizeIdentity("message", child)
			case "messageIds":
				value[key] = n.normalizeIdentitySet("message", child)
			case "at", "createdAt", "updatedAt":
				value[key] = "<timestamp>"
			default:
				value[key] = n.normalize(child)
			}
		}
		return value
	case string:
		return strings.ReplaceAll(value, n.root, "<root>")
	default:
		return value
	}
}

func (n *executorNormalizer) normalizeIdentitySet(kind string, value any) any {
	values, ok := value.([]any)
	if !ok {
		return value
	}
	for index := range values {
		values[index] = n.normalizeIdentity(kind, values[index])
	}
	sort.Slice(values, func(left, right int) bool {
		return fmt.Sprint(values[left]) < fmt.Sprint(values[right])
	})
	return values
}

func (n *executorNormalizer) normalizeIdentity(kind string, value any) any {
	identity, ok := value.(string)
	if !ok {
		return value
	}
	values := n.identities[kind]
	if values == nil {
		values = make(map[string]string)
		n.identities[kind] = values
	}
	if normalized := values[identity]; normalized != "" {
		return normalized
	}
	normalized := fmt.Sprintf("<%s-%d>", kind, len(values)+1)
	values[identity] = normalized
	return normalized
}
