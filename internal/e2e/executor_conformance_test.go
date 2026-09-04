package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	toolResults []string
	records     []any
	replay      []any
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
	records := readNormalizedSessionLog(t, dataDir, session, root)

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

	return executorSnapshot{toolResults: toolResults, records: records, replay: replay}
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

func readNormalizedSessionLog(t *testing.T, dataDir, session, root string) []any {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dataDir, "ox", "sessions", session+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var records []any
	normalizer := newExecutorNormalizer(root)
	for _, line := range bytes.Split(bytes.TrimSpace(content), []byte{'\n'}) {
		var record any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode session record: %v", err)
		}
		records = append(records, normalizer.normalize(record))
	}
	return records
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
			case "at":
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
