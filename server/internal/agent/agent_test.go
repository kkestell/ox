package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ox/internal/catalog"
	fake "ox/internal/openroutertest"
	"ox/internal/shellproc"
	"ox/internal/store"
	"ox/internal/tools"
	"ox/internal/transcript"
)

// recorder records events and answers permission requests in order.
type recorder struct {
	mu      sync.Mutex
	events  []Event
	answers []bool
	failAt  int
	asked   chan transcript.ToolCall
}

func (r *recorder) Send(event Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	if r.failAt > 0 && len(r.events) == r.failAt {
		return errors.New("the client went away")
	}
	return nil
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

func (f *fixture) input(text string, mode transcript.Mode) Input {
	message := transcript.TextMessage(text)
	return Input{SessionID: f.session, Input: transcript.TurnInput{Message: &message}, Model: fake.DefaultModel,
		Effort: catalog.EffortDefault, Mode: mode, SystemPrompt: "You are Ox.", Processes: f.processes}
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
	client := &recorder{}
	result, err := f.run(t, context.Background(), f.input("Hi", transcript.ModeAsk), client)
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

func TestToolCallsRunInOrderWithAskModePermission(t *testing.T) {
	f := newFixture(t, fake.Shell("printf one", "printf two", "printf three"), fake.Text("Done."))
	client := &recorder{answers: []bool{true, false, true}}
	result, err := f.run(t, context.Background(), f.input("Run them", transcript.ModeAsk), client)
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

func TestAutoModeRunsShellCallsWithoutAsking(t *testing.T) {
	f := newFixture(t, fake.Shell("printf ok"), fake.Text("Done."))
	if _, err := f.run(t, context.Background(), f.input("Run it", transcript.ModeAuto), &recorder{}); err != nil {
		t.Fatal(err)
	}
	if got := outcomes(f.saved(t)); len(got) != 1 || got[0] != "completed: Exit code: 0" {
		t.Errorf("outcomes = %q", got)
	}
}

func TestCancellationWhileAskingSavesEveryCallAsCancelled(t *testing.T) {
	f := newFixture(t, fake.Shell("printf done", "printf asking", "printf never"))
	ctx, cancel := context.WithCancel(context.Background())
	client := &recorder{asked: make(chan transcript.ToolCall)}
	in := f.input("Run them", transcript.ModeAsk)
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
	client := &recorder{}
	turn, err := f.agent.Start(ctx, f.input("Hi", transcript.ModeAsk), client)
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

func TestTemporaryFailuresAreRetriedUpToTheAttemptLimit(t *testing.T) {
	f := newFixture(t, fake.Status(503, `{}`), fake.Status(429, `{}`), fake.Text("Recovered."))
	if result, err := f.run(t, context.Background(), f.input("Hi", transcript.ModeAsk), &recorder{}); err != nil || result.Answer != "Recovered." {
		t.Errorf("result %+v, %v", result, err)
	}
	f = newFixture(t, fake.Status(503, `{}`), fake.Status(503, `{}`), fake.Status(503, `{}`), fake.Text("Never."))
	_, err := f.run(t, context.Background(), f.input("Hi", transcript.ModeAsk), &recorder{})
	if err == nil || !strings.HasPrefix(err.Error(), "the model request failed: OpenRouter returned 503") {
		t.Fatalf("error = %v", err)
	}
	entries := f.saved(t)
	if turnError, ok := entries[len(entries)-1].(transcript.TurnError); !ok || string(turnError) != err.Error() {
		t.Errorf("saved = %v", entries)
	}
	f = newFixture(t, fake.Status(400, `{}`))
	if _, err := f.run(t, context.Background(), f.input("Hi", transcript.ModeAsk), &recorder{}); len(f.server.Bodies()) != 1 || err == nil {
		t.Errorf("a permanent failure was retried: %v", err)
	}
}

func TestAnUpdateFailureStillCommitsTheBatch(t *testing.T) {
	f := newFixture(t, fake.Shell("printf one", "printf two"))
	// Event 1 is the session info and events 2 and 3 announce the calls.
	client := &recorder{failAt: 5, answers: []bool{true}}
	_, err := f.run(t, context.Background(), f.input("Run them", transcript.ModeAsk), client)
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
	in := f.input("", transcript.ModeAsk)
	in.Input = transcript.TurnInput{Message: &transcript.UserMessage{Parts: []transcript.UserMessagePart{
		{Image: &transcript.ImageAttachment{Data: "aGk=", MimeType: "image/png"}},
	}}}
	if _, err := f.agent.Start(context.Background(), in, &recorder{}); !errors.Is(err, ErrImagesUnsupported) {
		t.Errorf("error = %v", err)
	}
	if entries := f.saved(t); len(entries) != 0 {
		t.Errorf("a rejected prompt was saved: %v", entries)
	}
	f = newFixture(t, fake.Status(400, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`))
	_, err := f.run(t, context.Background(), f.input("Hi", transcript.ModeAsk), &recorder{})
	if err == nil || !strings.Contains(err.Error(), "the session exceeds the model context limit; start a new session") {
		t.Errorf("error = %v", err)
	}
}
