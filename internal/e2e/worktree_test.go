package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestClientOwnedGitWorktreesRemainIndependentAndPreserved(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}

	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repository, "init", "--initial-branch=main")
	runGit(t, git, repository, "config", "user.name", "Ox Test")
	runGit(t, git, repository, "config", "user.email", "ox@example.test")
	writeWorktreeFile(t, repository, "AGENTS.md", "primary instructions\n")
	writeWorktreeFile(t, repository, "shared.txt", "primary\n")
	runGit(t, git, repository, "add", "AGENTS.md", "shared.txt")
	runGit(t, git, repository, "commit", "-m", "initial")

	worktreeParent := t.TempDir()
	worktrees := []worktreeFixture{
		{
			name: "one", branch: "worktree-one",
			path:         filepath.Join(worktreeParent, "one"),
			instructions: "instructions for worktree one",
		},
		{
			name: "two", branch: "worktree-two",
			path:         filepath.Join(worktreeParent, "two"),
			instructions: "instructions for worktree two",
		},
	}
	for index := range worktrees {
		fixture := &worktrees[index]
		runGit(t, git, repository, "worktree", "add", "-b", fixture.branch, fixture.path, "main")
		fixture.path = canonicalPath(t, fixture.path)
		writeWorktreeFile(t, fixture.path, "AGENTS.md", fixture.instructions+"\n")
		writeWorktreeFile(t, fixture.path, "large.txt", largeWorktreeContent(fixture.name))
	}

	dataDir := t.TempDir()
	model := startModel(t)
	child := start(t, withModel(model), withEnvironment("XDG_DATA_HOME", dataDir))
	initialize(t, child)
	for index := range worktrees {
		worktrees[index].session = newSession(t, child, worktrees[index].path)
	}
	assertListedWorktreeSessions(t, child, worktrees)

	for index := range worktrees {
		fixture := &worktrees[index]
		queueWorktreeMutation(model, fixture.name)
		turn := child.begin("session/prompt", acp.PromptRequest{
			SessionID: fixture.session,
			Prompt:    textPrompt("mutate files in worktree " + fixture.name),
		})
		allowFilePermission(
			t, child, "write-shared-"+fixture.name,
			filepath.Join(fixture.path, "shared.txt"), true,
		)
		allowFilePermission(
			t, child, "write-untracked-"+fixture.name,
			filepath.Join(fixture.path, "notes.txt"), true,
		)
		assertEndTurn(t, child.result(child.await(turn)))
	}
	assertWorktreeFile(t, repository, "shared.txt", "primary\n")
	assertWorktreeFile(t, worktrees[0].path, "shared.txt", "worktree one\n")
	assertWorktreeFile(t, worktrees[1].path, "shared.txt", "worktree two\n")

	model.queue(toolResponse(
		"memory-write-one", "memory_write",
		`{"type":"finding","content":"isolated-fact one"}`,
	))
	model.queue(toolResponse(
		"memory-search-one", "memory_search", `{"query":"isolated-fact"}`,
	))
	model.queue(sse(evText("memory one done"), evFinishReason("stop")))
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: worktrees[0].session,
		Prompt:    textPrompt("store isolated memory"),
	})
	allowPermission(t, child, "memory-write-one", true)
	assertEndTurn(t, child.result(child.await(turn)))

	model.queue(toolResponse(
		"memory-search-before-two", "memory_search", `{"query":"isolated-fact"}`,
	))
	model.queue(toolResponse(
		"memory-write-two", "memory_write",
		`{"type":"finding","content":"isolated-fact two"}`,
	))
	model.queue(toolResponse(
		"memory-search-two", "memory_search", `{"query":"isolated-fact"}`,
	))
	model.queue(sse(evText("memory two done"), evFinishReason("stop")))
	turn = child.begin("session/prompt", acp.PromptRequest{
		SessionID: worktrees[1].session,
		Prompt:    textPrompt("check and store isolated memory"),
	})
	allowPermission(t, child, "memory-write-two", true)
	assertEndTurn(t, child.result(child.await(turn)))

	sessionRoot := filepath.Join(dataDir, "ox", "sessions")
	spillOne := filepath.Join(
		sessionRoot, worktrees[0].session+".spill", "grep-spill-one.out",
	)
	queueSpillRead(model, "one", spillOne, "")
	prompt(t, child, worktrees[0].session, "spill and read worktree one output")
	spillTwo := filepath.Join(
		sessionRoot, worktrees[1].session+".spill", "grep-spill-two.out",
	)
	queueSpillRead(model, "two", spillTwo, spillOne)
	prompt(t, child, worktrees[1].session, "reject foreign spill and read worktree two output")

	assertModelWorktreeContext(t, model.requests(), worktrees)
	failedForeignSpill := false
	for _, update := range updates(t, child, worktrees[1].session) {
		if update.Update.ToolCallID == "foreign-spill" &&
			update.Update.Status == acp.ToolCallStatusFailed {
			failedForeignSpill = true
		}
	}
	if !failedForeignSpill {
		t.Fatal("the second session did not reject the first session's spill")
	}
	_ = updates(t, child, worktrees[0].session)

	for _, fixture := range worktrees {
		child.request("session/close", acp.CloseSessionRequest{SessionID: fixture.session})
	}
	for index, fixture := range worktrees {
		assertPreservedWorktree(t, git, repository, fixture)
		assertExists(t, filepath.Join(sessionRoot, fixture.session+".jsonl"))
		if index == 0 {
			assertExists(t, spillOne)
		} else {
			assertExists(t, spillTwo)
		}
	}

	for _, fixture := range worktrees {
		child.request("session/delete", acp.DeleteSessionRequest{SessionID: fixture.session})
	}
	assertListedWorktreeSessions(t, child, nil)
	for _, fixture := range worktrees {
		assertPreservedWorktree(t, git, repository, fixture)
		assertNotExists(t, filepath.Join(sessionRoot, fixture.session+".jsonl"))
		assertNotExists(t, filepath.Join(sessionRoot, fixture.session+".spill"))
	}
	_ = updates(t, child, worktrees[1].session)
	child.stop()
}

type worktreeFixture struct {
	name         string
	branch       string
	path         string
	instructions string
	session      string
}

func queueWorktreeMutation(model *mockModel, name string) {
	model.queue(toolResponse(
		"read-shared-"+name, "read_file", `{"path":"shared.txt"}`,
	))
	model.queue(toolResponse(
		"write-shared-"+name, "write_file",
		fmt.Sprintf(`{"path":"shared.txt","content":%s}`, jsonString("worktree "+name+"\n")),
	))
	model.queue(toolResponse(
		"write-untracked-"+name, "write_file",
		fmt.Sprintf(`{"path":"notes.txt","content":%s}`, jsonString("notes "+name+"\n")),
	))
	model.queue(sse(evText("files done"), evFinishReason("stop")))
}

func queueSpillRead(model *mockModel, name, ownSpill, foreignSpill string) {
	if foreignSpill != "" {
		model.queue(toolResponse(
			"foreign-spill", "read_file",
			fmt.Sprintf(`{"path":%s,"limit":1}`, jsonString(foreignSpill)),
		))
	}
	model.queue(toolResponse(
		"spill-"+name, "grep", `{"pattern":"worktree","path":"large.txt"}`,
	))
	model.queue(toolResponse(
		"read-spill-"+name, "read_file",
		fmt.Sprintf(`{"path":%s,"offset":201,"limit":1}`, jsonString(ownSpill)),
	))
	model.queue(sse(evText("spill done"), evFinishReason("stop")))
}

func assertListedWorktreeSessions(t *testing.T, child *process, want []worktreeFixture) {
	t.Helper()
	raw := child.request("session/list", acp.ListSessionsRequest{})
	var response acp.ListSessionsResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Sessions) != len(want) {
		t.Fatalf("listed sessions = %#v, want %d", response.Sessions, len(want))
	}
	listed := make(map[string]string, len(response.Sessions))
	for _, session := range response.Sessions {
		listed[session.SessionID] = session.CWD
	}
	for _, fixture := range want {
		if listed[fixture.session] != fixture.path {
			t.Errorf("session %s cwd = %q, want %q", fixture.session, listed[fixture.session], fixture.path)
		}
	}
}

func assertModelWorktreeContext(
	t *testing.T,
	requests []modelRequest,
	worktrees []worktreeFixture,
) {
	t.Helper()
	seen := make(map[string]bool, len(worktrees))
	for _, request := range requests {
		for index, fixture := range worktrees {
			if !requestContainsText(request, fixture.instructions) {
				continue
			}
			seen[fixture.name] = true
			if !requestContainsText(
				request, "<workspace-root>"+fixture.path+"</workspace-root>",
			) {
				t.Errorf("worktree %s model context omitted canonical root %q", fixture.name, fixture.path)
			}
			other := worktrees[1-index]
			if requestContainsText(request, other.instructions) {
				t.Errorf("worktree %s model context contains sibling instructions", fixture.name)
			}
			if requestContainsText(request, "isolated-fact "+other.name) {
				t.Errorf("worktree %s model context contains sibling memory", fixture.name)
			}
		}
	}
	for _, fixture := range worktrees {
		if !seen[fixture.name] {
			t.Errorf("model received no context for worktree %s", fixture.name)
		}
		lastLine := fmt.Sprintf("large.txt:201: worktree %s line 201", fixture.name)
		found := false
		for _, request := range requests {
			if requestContainsText(request, fixture.instructions) &&
				requestContainsText(request, lastLine) {
				found = true
			}
		}
		if !found {
			t.Errorf("worktree %s spill was not read back", fixture.name)
		}
	}
}

func assertPreservedWorktree(
	t *testing.T,
	git string,
	repository string,
	fixture worktreeFixture,
) {
	t.Helper()
	assertWorktreeFile(t, fixture.path, "AGENTS.md", fixture.instructions+"\n")
	assertWorktreeFile(t, fixture.path, "shared.txt", "worktree "+fixture.name+"\n")
	assertWorktreeFile(t, fixture.path, "notes.txt", "notes "+fixture.name+"\n")
	if branch := strings.TrimSpace(runGit(t, git, fixture.path, "branch", "--show-current")); branch != fixture.branch {
		t.Errorf("worktree %s branch = %q, want %q", fixture.name, branch, fixture.branch)
	}
	status := runGit(t, git, fixture.path, "status", "--short")
	for _, entry := range []string{" M AGENTS.md", " M shared.txt", "?? large.txt", "?? notes.txt"} {
		if !strings.Contains(status, entry) {
			t.Errorf("worktree %s status %q omitted %q", fixture.name, status, entry)
		}
	}
	registrations := runGit(t, git, repository, "worktree", "list", "--porcelain")
	if !strings.Contains(registrations, "worktree "+fixture.path+"\n") ||
		!strings.Contains(registrations, "branch refs/heads/"+fixture.branch+"\n") {
		t.Errorf("worktree %s registration was removed:\n%s", fixture.name, registrations)
	}
	runGit(t, git, repository, "show-ref", "--verify", "refs/heads/"+fixture.branch)
}

func largeWorktreeContent(name string) string {
	var content strings.Builder
	for line := 1; line <= 201; line++ {
		fmt.Fprintf(&content, "worktree %s line %03d\n", name, line)
	}
	return content.String()
}

func writeWorktreeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertWorktreeFile(t *testing.T, root, name, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || string(data) != want {
		t.Fatalf("%s = %q, %v; want %q", filepath.Join(root, name), data, err, want)
	}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func runGit(t *testing.T, git, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command(git, append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func assertEndTurn(t *testing.T, result json.RawMessage) {
	t.Helper()
	if response := promptResponse(t, result); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want %q", response.StopReason, acp.StopReasonEndTurn)
	}
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent: %v", path, err)
	}
}
