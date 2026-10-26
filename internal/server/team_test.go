package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ox/internal/agent"
	"ox/internal/catalog"
	fake "ox/internal/openroutertest"
	"ox/internal/provider"
	"ox/internal/settings"
	"ox/internal/store"
	"ox/internal/tools"
	"ox/internal/transcript"
)

func teamServer(t *testing.T, workspace string, replies ...fake.Reply) (*Server, *team) {
	t.Helper()
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	remote := fake.Start(t, replies...)
	defaults := settings.Settings{Model: fake.DefaultModel, Effort: catalog.EffortHigh, Mode: transcript.ModeAsk, WorkerModels: "Use Plain for small tasks."}
	a := &agent.Agent{Store: db, Catalog: fake.ParsedCatalog(), Providers: map[string]provider.Provider{catalog.OpenRouter: remote.Client()}}
	s := New(nil, a, defaults, t.TempDir())
	parent, err := db.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	session, acpErr := s.newActiveSession(workspace, nil)
	if acpErr != nil {
		t.Fatal(acpErr)
	}
	session.selections = defaults
	s.active[parent.ID] = session
	t.Cleanup(func() {
		s.Shutdown()
		db.Close()
	})
	return s, &team{server: s, id: parent.ID, parent: parent.ID, name: "orchestrator", selections: defaults}
}

func teamGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func teamRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	teamGit(t, root, "init", "-b", "main")
	teamGit(t, root, "config", "user.name", "Test")
	teamGit(t, root, "config", "user.email", "test@example.com")
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "file"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	teamGit(t, root, "add", ".")
	teamGit(t, root, "commit", "-m", "Initial")
	return root
}

func teamWait(t *testing.T, tm *team) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	text, err := tm.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func teamEventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for worker")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkerWorktreeTurnReportAndRemoval(t *testing.T) {
	root := teamRepo(t)
	alias := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	bodies := make(chan map[string]any, 1)
	s, tm := teamServer(t, filepath.Join(alias, "src"),
		func(w http.ResponseWriter, r *http.Request, body map[string]any) {
			bodies <- body
			fake.Shell("printf auto > made")(w, r, body)
		}, fake.Text("Implemented."))
	ctx := context.Background()
	text, err := tm.CreateAgent(ctx, tools.CreateAgentArgs{Name: "builder", Message: "Build it."})
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, ".ox", "worktrees", "builder", "src")
	if !strings.Contains(text, workspace) || !strings.Contains(text, "ox/builder") {
		t.Fatalf("creation = %q", text)
	}
	body := <-bodies
	prompt := body["messages"].([]any)[0].(map[string]any)["content"].(string)
	for _, want := range []string{"You are worker builder", workspace, "Branch: ox/builder", "Commit your work on your branch", "orchestrator", "builder"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("worker prompt missing %q", want)
		}
	}
	for _, schema := range body["tools"].([]any) {
		name := schema.(map[string]any)["function"].(map[string]any)["name"]
		if name == "create_agent" || name == "remove_agent" || name == "wait" {
			t.Errorf("worker has tool %s", name)
		}
	}
	if report := teamWait(t, tm); !strings.Contains(report, "Message from builder:\nWorker builder finished its turn:\nImplemented.") {
		t.Fatalf("report = %q", report)
	}
	member, err := tm.member("builder")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.agent.Store.Read(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := saved.Transcript[0].(*transcript.TurnStart)
	if start.Input.Message.Text() != "Message from orchestrator:\nBuild it." || start.Mode != transcript.ModeAuto || start.Model != fake.DefaultModel || start.Effort != catalog.EffortHigh {
		t.Fatalf("worker start = %+v", start)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "made")); err != nil || string(data) != "auto" {
		t.Fatalf("Auto shell: %q, %v", data, err)
	}
	if _, err := tm.RemoveAgent(ctx, "builder"); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty removal = %v", err)
	}
	teamGit(t, workspace, "add", "made")
	teamGit(t, workspace, "commit", "-m", "Worker change")
	if _, err := tm.RemoveAgent(ctx, "builder"); err == nil || !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("unmerged removal = %v", err)
	}
	teamGit(t, root, "merge", "ox/builder")
	if _, err := tm.RemoveAgent(ctx, "builder"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.agent.Store.Member(member.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removed member = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(workspace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed worktree = %v", err)
	}
	if branches := teamGit(t, root, "branch", "--list", "ox/builder"); branches != "" {
		t.Fatalf("remaining branch = %q", branches)
	}
}

func TestBusyWorkerReceivesMessageAfterToolCall(t *testing.T) {
	gate := make(chan struct{})
	requested := make(chan struct{})
	bodies := make(chan map[string]any, 1)
	s, tm := teamServer(t, t.TempDir(),
		func(w http.ResponseWriter, r *http.Request, body map[string]any) {
			close(requested)
			fake.Gated(gate, fake.Shell("printf next"))(w, r, body)
		}, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
			bodies <- body
			fake.Text("Followed up.")(w, r, body)
		})
	if _, err := tm.CreateAgent(context.Background(), tools.CreateAgentArgs{Name: "reader", Message: "Read.", Dir: "."}); err != nil {
		t.Fatal(err)
	}
	<-requested
	if _, err := tm.SendMessage(context.Background(), "reader", "Check this too."); err != nil {
		t.Fatal(err)
	}
	listed, err := tm.ListAgents(context.Background())
	if err != nil || !strings.Contains(listed, "Name: reader") || !strings.Contains(listed, "Working: true") {
		t.Fatalf("list = %q, %v", listed, err)
	}
	close(gate)
	<-bodies
	if report := teamWait(t, tm); !strings.Contains(report, "Followed up.") {
		t.Fatalf("report = %q", report)
	}
	member, _ := tm.member("reader")
	saved, err := s.agent.Store.Read(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch := saved.Transcript[1].(*transcript.AssistantBatch)
	if strings.Join(batch.Context, "\n") != "Message from orchestrator:\nCheck this too." {
		t.Fatalf("after-call context = %v", batch.Context)
	}
	if len(saved.Transcript) != 3 {
		t.Fatalf("busy message started another turn: %v", saved.Transcript)
	}
}

func TestMessageAtWorkerTurnEndStartsAnotherTurn(t *testing.T) {
	requested := make(chan struct{})
	gate := make(chan struct{})
	second := make(chan struct{}, 1)
	s, tm := teamServer(t, t.TempDir(), func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		close(requested)
		fake.Gated(gate, fake.Text("First."))(w, r, body)
	}, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		second <- struct{}{}
		fake.Text("Second.")(w, r, body)
	})
	if _, err := tm.CreateAgent(context.Background(), tools.CreateAgentArgs{Name: "reader", Message: "Read.", Dir: "."}); err != nil {
		t.Fatal(err)
	}
	<-requested
	member, _ := tm.member("reader")
	// Hold completion reporting until the final inbox check and batch save.
	var sendErr error
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		close(gate)
		teamEventually(t, func() bool {
			saved, err := s.agent.Store.Read(member.ID)
			return err == nil && len(saved.Transcript) == 2
		})
		sendErr = s.agent.Store.Send(member.ID, "orchestrator", "Late message.")
	}()
	if sendErr != nil {
		t.Fatal(sendErr)
	}
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("late message did not start another turn")
	}
	teamEventually(t, func() bool {
		saved, err := s.agent.Store.Read(member.ID)
		return err == nil && len(saved.Transcript) == 4
	})
	saved, _ := s.agent.Store.Read(member.ID)
	start := saved.Transcript[2].(*transcript.TurnStart)
	if start.Input.Message.Text() != "Message from orchestrator:\nLate message." {
		t.Fatalf("second input = %+v", start)
	}
}

func TestWorkerDirectoriesSelectionsAndIdleWait(t *testing.T) {
	ceiling, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", ceiling)
	root := filepath.Join(ceiling, "workspace")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s, tm := teamServer(t, root, fake.Text("Done."), fake.Text("Done."), fake.Text("Done."))
	ctx := context.Background()
	for _, test := range []struct {
		args tools.CreateAgentArgs
		want string
	}{
		{tools.CreateAgentArgs{Name: "no-dir", Message: "Read."}, "dir is required outside a Git checkout"},
		{tools.CreateAgentArgs{Name: "bad-model", Message: "Read.", Dir: ".", Model: "missing"}, "model missing is not in the model catalog"},
		{tools.CreateAgentArgs{Name: "bad-effort", Message: "Read.", Dir: ".", Effort: "loud"}, "unknown effort loud"},
		{tools.CreateAgentArgs{Name: "unsupported", Message: "Read.", Dir: ".", Model: "openrouter:acme/plain", Effort: "high"}, "does not accept effort high; it accepts default"},
	} {
		if _, err := tm.CreateAgent(ctx, test.args); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("creation %+v: %v, want %q", test.args, err, test.want)
		}
	}
	if tm.WorkerModels() != "Use Plain for small tasks." {
		t.Fatalf("workerModels = %q", tm.WorkerModels())
	}
	if report := teamWait(t, tm); report != "No messages and no workers are working." {
		t.Fatalf("idle wait = %q", report)
	}
	for i, dir := range []string{".", root, "~/"} {
		name := []string{"relative", "absolute", "home"}[i]
		// The orchestrator's effort, high, falls back to Plain's default.
		created, err := tm.CreateAgent(ctx, tools.CreateAgentArgs{Name: name, Message: "Read.", Dir: dir, Model: "openrouter:acme/plain"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(created, "Branch:") {
			t.Errorf("a worker without a worktree was given a branch: %q", created)
		}
		teamWait(t, tm)
		member, err := tm.member(name)
		if err != nil || member.Branch != "" {
			t.Fatalf("directory member = %+v, %v", member, err)
		}
		selected, err := s.selectionsFor(member.ID)
		if err != nil || selected.Model != "openrouter:acme/plain" || selected.Effort != catalog.EffortDefault {
			t.Fatalf("worker selections = %+v, %v", selected, err)
		}
		if _, err := tm.CreateAgent(ctx, tools.CreateAgentArgs{Name: name, Message: "Read.", Dir: "."}); err == nil {
			t.Fatal("accepted duplicate worker")
		}
		if _, err := tm.RemoveAgent(ctx, name); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(member.Workspace); err != nil {
			t.Fatalf("removed existing directory: %v", err)
		}
	}
}

func TestWaitCancellationRemovalSuppressesReportAndShutdownSavesReport(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "shutdown", true: "remove"}[remove], func(t *testing.T) {
			requested := make(chan struct{})
			s, tm := teamServer(t, t.TempDir(), func(w http.ResponseWriter, r *http.Request, body map[string]any) {
				close(requested)
				fake.Hang("")(w, r, body)
			})
			if _, err := tm.CreateAgent(context.Background(), tools.CreateAgentArgs{Name: "reader", Message: "Read.", Dir: "."}); err != nil {
				t.Fatal(err)
			}
			<-requested
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if _, err := tm.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cancelled wait = %v", err)
			}
			if remove {
				if _, err := tm.RemoveAgent(context.Background(), "reader"); err != nil {
					t.Fatal(err)
				}
				if text := teamWait(t, tm); text != "No messages and no workers are working." {
					t.Fatalf("removal sent a report: %s", text)
				}
			} else {
				s.Shutdown()
				messages, err := s.takeMessages(tm.id)
				if err != nil || len(messages) != 1 || !strings.Contains(messages[0], "was cancelled") {
					t.Fatalf("shutdown report = %v, %v", messages, err)
				}
			}
		})
	}
}

func TestWorkerReportsLimitsRefusalsAndFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		reply fake.Reply
		want  string
	}{
		{"limit", fake.Chunks(fake.Delta(map[string]any{"content": "Cut"}, "length")), "stopped at the output limit."},
		{"refusal", fake.Chunks(fake.Delta(map[string]any{}, "content_filter")), "refused."},
		{"failure", fake.Status(400, `{"error":{"message":"broken"}}`), "failed:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, tm := teamServer(t, t.TempDir(), test.reply)
			if _, err := tm.CreateAgent(context.Background(), tools.CreateAgentArgs{Name: "reader", Message: "Read.", Dir: "."}); err != nil {
				t.Fatal(err)
			}
			if got := teamWait(t, tm); !strings.Contains(got, "Worker reader "+test.want) {
				t.Fatalf("report = %q", got)
			}
		})
	}
}

func TestWorkerCancelledDuringPreparationKeepsMessageAndSelectionAcrossRestart(t *testing.T) {
	workspace := t.TempDir()
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]settings.MCPServer{
		"slow": {Command: "sh", Args: []string{"-c", "touch preparing; cat >/dev/null"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, ".ox"), 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(workspace, ".ox", "settings.json")
	if err := os.WriteFile(settingsPath, config, 0o644); err != nil {
		t.Fatal(err)
	}
	s, tm := teamServer(t, workspace, fake.Text("Resumed."))
	if _, err := tm.CreateAgent(context.Background(), tools.CreateAgentArgs{
		Name: "reader", Message: "Keep this task.", Dir: ".", Model: "openrouter:acme/plain", Effort: "default",
	}); err != nil {
		t.Fatal(err)
	}
	member, err := tm.member("reader")
	if err != nil {
		t.Fatal(err)
	}
	teamEventually(t, func() bool {
		_, err := os.Stat(filepath.Join(workspace, "preparing"))
		return err == nil
	})
	s.Shutdown()
	saved, err := s.agent.Store.Read(member.ID)
	if err != nil || len(saved.Transcript) != 0 {
		t.Fatalf("unstarted worker transcript = %+v, %v", saved, err)
	}
	if has, err := s.agent.Store.HasMessages(member.ID); err != nil || !has {
		t.Fatalf("cancelled preparation lost the task: %t, %v", has, err)
	}
	if err := os.Remove(settingsPath); err != nil {
		t.Fatal(err)
	}
	restarted := New(nil, s.agent, s.defaults, s.home)
	t.Cleanup(restarted.Shutdown)
	before, err := restarted.workerPrompt("Base", member)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.wake(member.ID); err != nil {
		t.Fatal(err)
	}
	teamEventually(t, func() bool {
		saved, err := s.agent.Store.Read(member.ID)
		return err == nil && len(saved.Transcript) == 2
	})
	saved, err = s.agent.Store.Read(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := saved.Transcript[0].(*transcript.TurnStart)
	if start.Model != "openrouter:acme/plain" || start.Effort != catalog.EffortDefault || start.Mode != transcript.ModeAuto || start.Input.Message.Text() != "Message from orchestrator:\nKeep this task." {
		t.Fatalf("restored worker turn = %+v", start)
	}
	teamEventually(t, func() bool { return !restarted.ops.working(member.ID) })
	_, release, ok := restarted.ops.tryPrompt(member.ID)
	if !ok {
		t.Fatal("could not reserve worker turn")
	}
	after, err := restarted.workerPrompt("Base", member)
	release()
	if err != nil || after != before || strings.Contains(after, "Working:") || strings.Contains(after, "Team when you joined") || !strings.Contains(after, "\nTeam:\n") {
		t.Fatalf("worker prompt changed with activity: %q, %v", after, err)
	}
}

func TestFailedRemovalWakesMessagesSentWhileClosing(t *testing.T) {
	root := teamRepo(t)
	s, tm := teamServer(t, root, fake.Text("First."), fake.Text("Followed up."))
	if _, err := tm.CreateAgent(context.Background(), tools.CreateAgentArgs{Name: "builder", Message: "Build."}); err != nil {
		t.Fatal(err)
	}
	teamWait(t, tm)
	member, err := tm.member("builder")
	if err != nil {
		t.Fatal(err)
	}
	teamEventually(t, func() bool { return !s.ops.working(member.ID) })
	if err := os.WriteFile(filepath.Join(member.Workspace, "change"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	teamGit(t, member.Workspace, "add", "change")
	teamGit(t, member.Workspace, "commit", "-m", "Unmerged change")
	// Keep the cancelled operation alive long enough to send work while removal waits.
	ctx, release, ok := s.ops.tryPrompt(member.ID)
	if !ok {
		t.Fatal("could not reserve worker turn")
	}
	done := make(chan error, 1)
	go func() {
		_, err := tm.RemoveAgent(context.Background(), "builder")
		done <- err
	}()
	<-ctx.Done()
	if _, err := tm.SendMessage(context.Background(), "builder", "Continue after removal fails."); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("removal = %v", err)
	}
	if report := teamWait(t, tm); !strings.Contains(report, "Followed up.") {
		t.Fatalf("pending message was stranded: %q", report)
	}
	saved, err := s.agent.Store.Read(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := saved.Transcript[2].(*transcript.TurnStart)
	if start.Input.Message.Text() != "Message from orchestrator:\nContinue after removal fails." {
		t.Fatalf("follow-up input = %+v", start)
	}
}
