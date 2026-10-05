package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ox/internal/catalog"
	"ox/internal/openrouter"
	fake "ox/internal/openroutertest"
	"ox/internal/shellproc"
	"ox/internal/store"
	"ox/internal/tools"
	"ox/internal/transcript"
)

// recorder records events, reports its mode, and answers permission requests
// in order.
type recorder struct {
	mu              sync.Mutex
	events          []Event
	mode            transcript.Mode
	modeAfterCall   transcript.Mode
	answers         []bool
	failAt          int
	asked           chan transcript.ToolCall
	cancelAfterCall context.CancelFunc
}

func (r *recorder) Send(event Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	if _, ok := event.(ToolFinished); ok && r.cancelAfterCall != nil {
		r.cancelAfterCall()
	}
	if _, ok := event.(ToolFinished); ok && r.modeAfterCall != "" {
		r.mode, r.modeAfterCall = r.modeAfterCall, ""
	}
	if r.failAt > 0 && len(r.events) == r.failAt {
		return errors.New("the client went away")
	}
	return nil
}

func (r *recorder) Mode() transcript.Mode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mode
}

func (r *recorder) Approve(ctx context.Context, call transcript.ToolCall, _ tools.Permission) (bool, error) {
	if r.asked != nil {
		r.asked <- call
		<-ctx.Done()
		return false, ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	answer := r.answers[0]
	r.answers = r.answers[1:]
	return answer, nil
}

type fixture struct {
	agent     *Agent
	server    *fake.Server
	session   string
	workspace string
	processes *shellproc.Processes
}

func newFixture(t *testing.T, replies ...fake.Reply) *fixture {
	t.Helper()
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	server := fake.Start(t, replies...)
	workspace := t.TempDir()
	summary, err := s.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	processes := &shellproc.Processes{}
	t.Cleanup(processes.Shutdown)
	return &fixture{agent: &Agent{Store: s, OpenRouter: server.Client(), Catalog: fake.ParsedCatalog()},
		server: server, session: summary.ID, workspace: workspace, processes: processes}
}

func (f *fixture) input(text string) Input {
	message := transcript.TextMessage(text)
	return Input{SessionID: f.session, Input: transcript.TurnInput{Message: &message}, Model: fake.DefaultModel,
		Effort: catalog.EffortDefault, SystemPrompt: "You are Ox.", Processes: f.processes}
}

func (f *fixture) run(t *testing.T, ctx context.Context, in Input, client Client) (Result, error) {
	t.Helper()
	turn, err := f.agent.Start(ctx, in, client)
	if err != nil {
		t.Fatal(err)
	}
	return turn.Run(ctx)
}

func (f *fixture) saved(t *testing.T) []transcript.Entry {
	t.Helper()
	session, err := f.agent.Store.Read(f.session)
	if err != nil {
		t.Fatal(err)
	}
	return session.Transcript
}

func outcomes(entries []transcript.Entry) []string {
	var texts []string
	for _, entry := range entries {
		if batch, ok := entry.(*transcript.AssistantBatch); ok {
			for _, outcome := range batch.Outcomes {
				texts = append(texts, string(outcome.Status)+": "+strings.SplitN(outcome.Text, "\n", 2)[0])
			}
		}
	}
	return texts
}

func TestAnAnswerIsSavedAndReported(t *testing.T) {
	f := newFixture(t, fake.Chunks(
		fake.Delta(map[string]any{"role": "assistant", "reasoning": "Hmm."}, ""),
		fake.Delta(map[string]any{"content": "Hello."}, "stop"),
		fake.Usage(10, 5, 0.25),
	))
	client := &recorder{mode: transcript.ModeAsk}
	result, err := f.run(t, context.Background(), f.input("Hi"), client)
	if err != nil || result != (Result{Stop: EndTurn, Answer: "Hello."}) {
		t.Fatalf("result %+v, %v", result, err)
	}
	entries := f.saved(t)
	if len(entries) != 2 || entries[1].(*transcript.AssistantBatch).Message.Text != "Hello." {
		t.Errorf("saved = %v", entries)
	}
	cost := 0.25
	info := client.events[0].(SessionInfo)
	if info.Title != "Hi" || client.events[1] != ReasoningDelta("Hmm.") || client.events[2] != TextDelta("Hello.") {
		t.Errorf("events = %v", client.events)
	}
	if usage := client.events[3].(Usage); usage.Used != 15 || usage.Size != 1048576 || *usage.Cost != cost {
		t.Errorf("usage = %+v", usage)
	}
}

func TestCompactionDecisionUsesReportedInputTokens(t *testing.T) {
	response := func(input uint64) *transcript.AssistantBatch {
		return &transcript.AssistantBatch{Message: transcript.AssistantMessage{Usage: &transcript.Usage{InputTokens: input}}}
	}
	maxLimit := int(^uint(0) >> 1)
	tests := []struct {
		name    string
		limit   int
		entries []transcript.Entry
		want    bool
	}{
		{"empty transcript", 1000, nil, false},
		{"no assistant response", 1000, []transcript.Entry{&transcript.TurnStart{}}, false},
		{"below threshold", 1000, []transcript.Entry{response(799)}, false},
		{"at threshold", 1000, []transcript.Entry{response(800)}, true},
		{"above threshold", 1000, []transcript.Entry{response(801)}, true},
		{"above context limit", 1000, []transcript.Entry{response(1500)}, true},
		{"fraction below threshold", 101, []transcript.Entry{response(80)}, false},
		{"fraction at threshold", 101, []transcript.Entry{response(81)}, true},
		{"zero context limit", 0, []transcript.Entry{response(800)}, false},
		{"negative context limit", -1, []transcript.Entry{response(800)}, false},
		{"large context limit", maxLimit, []transcript.Entry{response(uint64(maxLimit) / 2)}, false},
		{"latest response is below threshold", 1000, []transcript.Entry{response(900), response(100)}, false},
		{"latest response is above threshold", 1000, []transcript.Entry{response(100), response(900)}, true},
		{"latest response has no usage", 1000, []transcript.Entry{response(900), &transcript.AssistantBatch{}}, false},
		{"trailing turn start and error", 1000, []transcript.Entry{response(800), &transcript.TurnStart{}, transcript.TurnError("failed")}, true},
		{"other token counts are not added", 1000, []transcript.Entry{&transcript.AssistantBatch{
			Message: transcript.AssistantMessage{Usage: &transcript.Usage{
				InputTokens: 799, CachedTokens: 799, OutputTokens: 1000, ReasoningTokens: 999,
			}},
		}}, false},
		{"cached input is not subtracted", 1000, []transcript.Entry{&transcript.AssistantBatch{
			Message: transcript.AssistantMessage{Usage: &transcript.Usage{InputTokens: 800, CachedTokens: 800}},
		}}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turn := &Turn{request: openrouter.Request{Model: &catalog.Model{ContextLimit: test.limit}, Transcript: test.entries}}
			if got := turn.needsCompaction(); got != test.want {
				t.Fatalf("needsCompaction() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCompactionDecisionUsesSavedResponseOnNewTurn(t *testing.T) {
	f := newFixture(t, fake.Chunks(
		fake.Delta(map[string]any{"role": "assistant", "content": "Hello."}, "stop"),
		fake.Usage(800, 50, 0.25),
	))
	f.agent.Catalog.Lookup(fake.DefaultModel).ContextLimit = 1000
	if _, err := f.run(t, context.Background(), f.input("Hi"), &recorder{mode: transcript.ModeAsk}); err != nil {
		t.Fatal(err)
	}
	turn, err := f.agent.Start(context.Background(), f.input("Continue"), &recorder{mode: transcript.ModeAsk})
	if err != nil {
		t.Fatal(err)
	}
	if !turn.needsCompaction() {
		t.Fatal("the new turn did not use the saved response's input tokens")
	}
	in := f.input("Use a larger context")
	in.Model = "openrouter:acme/plain"
	f.agent.Catalog.Lookup(in.Model).ContextLimit = 2000
	turn, err = f.agent.Start(context.Background(), in, &recorder{mode: transcript.ModeAsk})
	if err != nil {
		t.Fatal(err)
	}
	if turn.needsCompaction() {
		t.Fatal("the decision did not use the newly selected model's context limit")
	}
}

func TestCompactionDecisionUsesSavedToolResponse(t *testing.T) {
	f := newFixture(t)
	f.agent.Catalog.Lookup(fake.DefaultModel).ContextLimit = 1000
	turn, err := f.agent.Start(context.Background(), f.input("Run a command"), &recorder{mode: transcript.ModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	if turn.needsCompaction() {
		t.Fatal("a turn without a response needs compaction")
	}
	message := transcript.AssistantMessage{
		ToolCalls: []transcript.ToolCall{{CallID: "shell-0", Name: tools.Shell, Arguments: `{"command":"printf ready"}`}},
		Usage:     &transcript.Usage{InputTokens: 800},
	}
	if err := turn.processBatch(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if !turn.needsCompaction() {
		t.Fatal("the saved tool response did not supply the next compaction decision")
	}
	if got := outcomes(f.saved(t)); fmt.Sprint(got) != "[completed: Exit code: 0]" {
		t.Fatalf("outcomes = %q", got)
	}
}

func TestToolCallsRunInOrderWithAskModePermission(t *testing.T) {
	f := newFixture(t, fake.Shell("printf one", "printf two", "printf three"), fake.Text("Done."))
	client := &recorder{mode: transcript.ModeAsk, answers: []bool{true, false, true}}
	result, err := f.run(t, context.Background(), f.input("Run them"), client)
	if err != nil || result.Answer != "Done." {
		t.Fatalf("result %+v, %v", result, err)
	}
	want := []string{"completed: Exit code: 0", "failed: User denied permission to run this command.", "completed: Exit code: 0"}
	if got := outcomes(f.saved(t)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("outcomes = %q", got)
	}
	bodies := f.server.Bodies()
	messages := bodies[1]["messages"].([]any)
	if len(messages) != 6 || messages[3].(map[string]any)["tool_call_id"] != "shell-0" {
		t.Errorf("the next request carries the results in call order: %v", messages)
	}
	var kinds []string
	for _, event := range client.events[1:] {
		kinds = append(kinds, fmt.Sprintf("%T", event))
	}
	want = []string{"agent.ToolPending", "agent.ToolPending", "agent.ToolPending", "agent.ToolStarted", "agent.ToolFinished",
		"agent.ToolFinished", "agent.ToolStarted", "agent.ToolFinished", "agent.Usage", "agent.TextDelta", "agent.Usage"}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Errorf("events = %v", kinds)
	}
}

func TestAModeChangeAppliesFromTheNextBatch(t *testing.T) {
	f := newFixture(t, fake.Shell("printf one", "printf two"), fake.Shell("printf three"), fake.Text("Done."))
	// The recorder has no third answer, so a third permission request fails
	// the test.
	client := &recorder{mode: transcript.ModeAsk, modeAfterCall: transcript.ModeAuto, answers: []bool{true, true}}
	if _, err := f.run(t, context.Background(), f.input("Run them"), client); err != nil {
		t.Fatal(err)
	}
	want := []string{"completed: Exit code: 0", "completed: Exit code: 0", "completed: Exit code: 0"}
	saved := f.saved(t)
	if got := outcomes(saved); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("outcomes = %q", got)
	}
	if len(client.answers) != 0 {
		t.Errorf("the first batch did not ask for both calls: %v answers left", len(client.answers))
	}
	if mode := saved[0].(*transcript.TurnStart).Mode; mode != transcript.ModeAsk {
		t.Errorf("turn start mode = %s", mode)
	}
}

func TestAutoModeRunsShellCallsWithoutAsking(t *testing.T) {
	f := newFixture(t, fake.Shell("printf ok"), fake.Text("Done."))
	if _, err := f.run(t, context.Background(), f.input("Run it"), &recorder{mode: transcript.ModeAuto}); err != nil {
		t.Fatal(err)
	}
	if got := outcomes(f.saved(t)); len(got) != 1 || got[0] != "completed: Exit code: 0" {
		t.Errorf("outcomes = %q", got)
	}
}

func TestCancellationWhileAskingSavesEveryCallAsCancelled(t *testing.T) {
	f := newFixture(t, fake.Shell("printf done", "printf asking", "printf never"))
	ctx, cancel := context.WithCancel(context.Background())
	client := &recorder{mode: transcript.ModeAsk, asked: make(chan transcript.ToolCall)}
	in := f.input("Run them")
	turn, err := f.agent.Start(ctx, in, client)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan Result)
	go func() {
		result, _ := turn.Run(ctx)
		results <- result
	}()
	<-client.asked
	cancel()
	if result := <-results; result.Stop != Cancelled {
		t.Errorf("result = %+v", result)
	}
	got := outcomes(f.saved(t))
	if len(got) != 3 || got[0] != "cancelled: Cancelled before this tool was started." || got[2] != got[1] {
		t.Errorf("outcomes = %q", got)
	}
}

func TestCancellationDuringTheStreamDiscardsProvisionalOutput(t *testing.T) {
	f := newFixture(t, fake.Hang(fake.SSE(fake.Delta(map[string]any{"content": "partial"}, ""))[len(": OPENROUTER PROCESSING\n\n"):]))
	ctx, cancel := context.WithCancel(context.Background())
	client := &recorder{mode: transcript.ModeAsk}
	turn, err := f.agent.Start(ctx, f.input("Hi"), client)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(200*time.Millisecond, cancel)
	if result, err := turn.Run(ctx); err != nil || result.Stop != Cancelled {
		t.Errorf("result %+v, %v", result, err)
	}
	if entries := f.saved(t); len(entries) != 1 {
		t.Errorf("saved = %v", entries)
	}
}

func TestCancellationKeepsThePatchOutcomeAndSkipsLaterCalls(t *testing.T) {
	for _, test := range []struct {
		name   string
		patch  string
		status transcript.ToolStatus
	}{
		{"completed", "*** Add File: first\n+content\n*** Add File: second\n+second\n", transcript.ToolCompleted},
		{"failed", "*** Add File: first\n+content\n*** Add File: first/child\n+child\n*** Add File: second\n+second\n", transcript.ToolFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, fake.Calls(
				fake.Call{ID: "patch", Name: tools.ApplyPatch, Arguments: map[string]any{"patch": "*** Begin Patch\n" + test.patch + "*** End Patch\n"}},
				fake.Call{ID: "later", Name: tools.ApplyPatch, Arguments: map[string]any{"patch": "*** Begin Patch\n*** Add File: never\n+never\n*** End Patch\n"}},
			))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &recorder{mode: transcript.ModeAuto, cancelAfterCall: cancel}
			result, err := f.run(t, ctx, f.input("Apply patches"), client)
			if err != nil || result.Stop != Cancelled {
				t.Fatalf("result %+v, %v", result, err)
			}
			entries := f.saved(t)
			if len(entries) != 2 {
				t.Fatalf("saved %d entries", len(entries))
			}
			batch := entries[1].(*transcript.AssistantBatch)
			if len(batch.Outcomes) != 2 || batch.Outcomes[0].Status != test.status ||
				batch.Outcomes[1].Status != transcript.ToolCancelled || batch.Outcomes[1].Text != "Cancelled before this tool was started." {
				t.Fatalf("outcomes = %+v", batch.Outcomes)
			}
			outcome := batch.Outcomes[0]
			if test.status == transcript.ToolCompleted {
				if outcome.Text != "Applied patch.\nAdded first\nAdded second" || len(outcome.Content) != 4 {
					t.Fatalf("completed patch = %+v", outcome)
				}
			} else if !strings.Contains(outcome.Text, "Completed:\nAdded first\nNot attempted:\nAdded second") {
				t.Fatalf("failed patch = %+v", outcome)
			}
			data, err := os.ReadFile(filepath.Join(f.workspace, "first"))
			if err != nil || string(data) != "content\n" {
				t.Fatalf("completed file = %q, %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(f.workspace, "never")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("later patch ran: %v", err)
			}
			if len(f.server.Bodies()) != 1 {
				t.Fatal("cancelled turn made another model request")
			}
		})
	}
}

func TestTemporaryFailuresAreRetriedUpToTheAttemptLimit(t *testing.T) {
	f := newFixture(t, fake.Status(503, `{}`), fake.Status(429, `{}`), fake.Text("Recovered."))
	if result, err := f.run(t, context.Background(), f.input("Hi"), &recorder{mode: transcript.ModeAsk}); err != nil || result.Answer != "Recovered." {
		t.Errorf("result %+v, %v", result, err)
	}
	f = newFixture(t, fake.Status(503, `{}`), fake.Status(503, `{}`), fake.Status(503, `{}`), fake.Text("Never."))
	_, err := f.run(t, context.Background(), f.input("Hi"), &recorder{mode: transcript.ModeAsk})
	if err == nil || !strings.HasPrefix(err.Error(), "the model request failed: OpenRouter returned 503") {
		t.Fatalf("error = %v", err)
	}
	entries := f.saved(t)
	if turnError, ok := entries[len(entries)-1].(transcript.TurnError); !ok || string(turnError) != err.Error() {
		t.Errorf("saved = %v", entries)
	}
	f = newFixture(t, fake.Status(400, `{}`))
	if _, err := f.run(t, context.Background(), f.input("Hi"), &recorder{mode: transcript.ModeAsk}); len(f.server.Bodies()) != 1 || err == nil {
		t.Errorf("a permanent failure was retried: %v", err)
	}
}

func TestAnUpdateFailureStillCommitsTheBatch(t *testing.T) {
	f := newFixture(t, fake.Shell("printf one", "printf two"))
	// Event 1 is the session info and events 2 and 3 announce the calls.
	client := &recorder{mode: transcript.ModeAsk, failAt: 5, answers: []bool{true}}
	_, err := f.run(t, context.Background(), f.input("Run them"), client)
	if err == nil || !strings.HasPrefix(err.Error(), "sending an ACP update failed") {
		t.Fatalf("error = %v", err)
	}
	got := outcomes(f.saved(t))
	if len(got) != 2 || got[0] != "completed: Exit code: 0" || got[1] != "failed: Not started: the client connection failed before this tool ran." {
		t.Errorf("outcomes = %q", got)
	}
}

func TestStartRejectsImagesForATextModelAndContextOverflowEndsTheTurn(t *testing.T) {
	f := newFixture(t)
	in := f.input("")
	in.Input = transcript.TurnInput{Message: &transcript.UserMessage{Parts: []transcript.UserMessagePart{
		{Image: &transcript.ImageAttachment{Data: "aGk=", MimeType: "image/png"}},
	}}}
	if _, err := f.agent.Start(context.Background(), in, &recorder{mode: transcript.ModeAsk}); !errors.Is(err, ErrImagesUnsupported) {
		t.Errorf("error = %v", err)
	}
	if entries := f.saved(t); len(entries) != 0 {
		t.Errorf("a rejected prompt was saved: %v", entries)
	}
	f = newFixture(t, fake.Status(400, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`))
	_, err := f.run(t, context.Background(), f.input("Hi"), &recorder{mode: transcript.ModeAsk})
	if err == nil || !strings.Contains(err.Error(), "the session exceeds the model context limit; start a new session") {
		t.Errorf("error = %v", err)
	}
}
