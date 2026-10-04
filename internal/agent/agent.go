// Package agent runs one turn of a session: it saves the user message or skill
// invocation, makes model requests, runs tools in call order, saves each
// complete assistant batch, and returns the answer when the turn finishes.
package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ox/internal/catalog"
	"ox/internal/openrouter"
	"ox/internal/shellproc"
	"ox/internal/store"
	"ox/internal/tools"
	"ox/internal/transcript"
)

// modelRequestAttempts is how many times a model request that ends in a
// temporary failure is tried.
const modelRequestAttempts = 3

// Event is one thing a turn reports while it runs.
type Event interface{ event() }

// SessionInfo reports that the turn start was saved. Title is set when the
// turn gave the session its title.
type SessionInfo struct {
	UpdatedAt string
	Title     string
}

// TextDelta is provisional answer text.
type TextDelta string

// ReasoningDelta is provisional reasoning text.
type ReasoningDelta string

// ToolPending announces a call before anything runs.
type ToolPending struct{ Call transcript.ToolCall }

// ToolStarted reports that a call is running.
type ToolStarted struct{ Call transcript.ToolCall }

// ToolFinished reports a call's final outcome.
type ToolFinished struct {
	Call    transcript.ToolCall
	Outcome transcript.ToolOutcome
}

// Usage reports the context tokens, the context limit, and the session cost
// after each saved batch.
type Usage struct {
	Used uint64
	Size int
	Cost *float64
}

func (SessionInfo) event()    {}
func (TextDelta) event()      {}
func (ReasoningDelta) event() {}
func (ToolPending) event()    {}
func (ToolStarted) event()    {}
func (ToolFinished) event()   {}
func (Usage) event()          {}

// Client receives a turn's events and answers Ask mode permission requests.
type Client interface {
	Send(Event) error
	// Approve asks whether call may run. It returns ErrCancelled when the
	// client cancelled the request.
	Approve(ctx context.Context, call transcript.ToolCall, permission tools.Permission) (bool, error)
}

// ErrCancelled means the prompt was cancelled.
var ErrCancelled = errors.New("the prompt was cancelled")

// ErrImagesUnsupported means the session has an image and the selected model
// does not accept images.
var ErrImagesUnsupported = errors.New("the selected model does not accept images; choose a model that accepts images")

// Agent runs turns against a session store and OpenRouter.
type Agent struct {
	Store      *store.Store
	OpenRouter *openrouter.Client
	Catalog    catalog.Catalog
	// RetryDelays are the waits before the second and third attempts of a
	// model request, since retrying a temporary failure at once tends to fail
	// the same way.
	RetryDelays [modelRequestAttempts - 1]time.Duration
}

// New returns an agent with the standard retry delays.
func New(s *store.Store, client *openrouter.Client, cat catalog.Catalog) *Agent {
	return &Agent{Store: s, OpenRouter: client, Catalog: cat, RetryDelays: [2]time.Duration{2 * time.Second, 8 * time.Second}}
}

// Input is what starts a turn.
type Input struct {
	SessionID string
	Input     transcript.TurnInput
	// Model, Effort, and Mode are the session's selections, used for every
	// model request in the turn.
	Model        string
	Effort       catalog.Effort
	Mode         transcript.Mode
	SystemPrompt string
	// Processes are the session's background processes, which outlive the
	// turn.
	Processes *shellproc.Processes
}

// Stop is why a turn ended without an error.
type Stop int

const (
	EndTurn Stop = iota
	Cancelled
	MaxTokens
	Refusal
)

// Result is how a turn ended, with the answer when it finished.
type Result struct {
	Stop   Stop
	Answer string
}

// Turn is one turn whose turn start is saved.
type Turn struct {
	agent   *Agent
	client  Client
	request openrouter.Request
	mode    transcript.Mode
	summary store.Summary
	toolbox *tools.Toolbox
	info    *SessionInfo
}

// ModelSelection returns the catalog model for a session's selections,
// rejecting a model or effort the fetched catalog no longer accepts.
func ModelSelection(cat catalog.Catalog, model string, effort catalog.Effort) (*catalog.Model, error) {
	selected := cat.Lookup(model)
	if selected == nil {
		return nil, fmt.Errorf("session model %s is not in the model catalog", model)
	}
	if !selected.Supports(effort) {
		return nil, fmt.Errorf("session model %s does not accept effort %s", model, effort)
	}
	return selected, nil
}

// Start reads the session and saves the turn start with the selected model,
// effort, and mode before any model request. Nothing is saved when ctx is
// already done; the turn then runs as cancelled.
func (a *Agent) Start(ctx context.Context, in Input, client Client) (*Turn, error) {
	session, err := a.Store.Read(in.SessionID)
	if err != nil {
		return nil, err
	}
	model, err := ModelSelection(a.Catalog, in.Model, in.Effort)
	if err != nil {
		return nil, err
	}
	t := &Turn{
		agent:  a,
		client: client,
		request: openrouter.Request{
			Model: model, Effort: in.Effort, SystemPrompt: in.SystemPrompt, Tools: tools.Schemas(), Transcript: session.Transcript,
		},
		mode:    in.Mode,
		summary: session.Summary,
		toolbox: &tools.Toolbox{Workspace: session.Summary.Workspace, Processes: in.Processes},
	}
	if ctx.Err() != nil {
		return t, nil
	}
	start := &transcript.TurnStart{Model: model.QualifiedID(), Effort: in.Effort, Mode: in.Mode, Input: in.Input}
	// An earlier image fails every request to a model without image input.
	if !model.AcceptsImages && (start.Input.HasImages() || hasImages(session.Transcript)) {
		return nil, ErrImagesUnsupported
	}
	updated, err := a.Store.AppendTurnStart(in.SessionID, start)
	if err != nil {
		return nil, err
	}
	t.request.Transcript = append(t.request.Transcript, start)
	t.info = &SessionInfo{UpdatedAt: updated.UpdatedAt}
	if t.summary.Title == "" {
		t.info.Title = updated.Title
	}
	return t, nil
}

// Workspace returns the session's workspace.
func (t *Turn) Workspace() string { return t.summary.Workspace }

// failure is an error that ends a turn and is saved as its turn error.
type failure struct {
	reason string
	err    error
}

func (f *failure) Error() string { return f.reason + ": " + f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

func modelFailure(err error) error {
	if errors.Is(err, openrouter.ErrContextOverflow) {
		err = errors.New("the session exceeds the model context limit; start a new session")
	}
	return &failure{"the model request failed", err}
}

// updateFailed is the reason of a failure to send an update to the client.
const updateFailed = "sending an ACP update failed"

func updateFailure(err error) error { return &failure{updateFailed, err} }

func permissionFailure(err error) error {
	return &failure{"requesting shell permission failed", err}
}

func storageFailure(err error) error { return &failure{"saving the transcript failed", err} }

// Run runs the turn until it finishes, stops early, or fails. A failure is
// saved as the turn's error.
func (t *Turn) Run(ctx context.Context) (Result, error) {
	if t.info == nil {
		return Result{Stop: Cancelled}, nil
	}
	result, err := t.loop(ctx)
	if errors.Is(err, ErrCancelled) {
		return Result{Stop: Cancelled}, nil
	}
	if err != nil {
		if saveErr := t.agent.Store.AppendTurnError(t.summary.ID, err.Error()); saveErr != nil {
			return Result{}, storageFailure(fmt.Errorf("%v; a turn error was being saved because %v", saveErr, err))
		}
		t.request.Transcript = append(t.request.Transcript, transcript.TurnError(err.Error()))
		return Result{}, err
	}
	return result, nil
}

func (t *Turn) loop(ctx context.Context) (Result, error) {
	if err := t.client.Send(*t.info); err != nil {
		return Result{}, updateFailure(err)
	}
	for {
		if ctx.Err() != nil {
			return Result{}, ErrCancelled
		}
		completion, err := t.requestWithRetries(ctx)
		if err != nil {
			return Result{}, err
		}
		if err := t.processBatch(ctx, completion.Message); err != nil {
			return Result{}, err
		}
		used, cost, _ := transcript.UsageSummary(t.request.Transcript)
		if err := t.client.Send(Usage{Used: used, Size: t.request.Model.ContextLimit, Cost: cost}); err != nil {
			return Result{}, updateFailure(err)
		}
		switch completion.Stop {
		case openrouter.StopFinished:
			return Result{Stop: EndTurn, Answer: completion.Message.Text}, nil
		case openrouter.StopTokenLimit:
			return Result{Stop: MaxTokens}, nil
		case openrouter.StopRefused:
			return Result{Stop: Refusal}, nil
		}
	}
}

// requestWithRetries makes one model request, retrying a temporary failure.
// Provisional output of a failed attempt stays on screen.
func (t *Turn) requestWithRetries(ctx context.Context) (*openrouter.Completion, error) {
	for attempt := 1; ; attempt++ {
		completion, err := t.complete(ctx)
		var f *failure
		if attempt == modelRequestAttempts || !errors.As(err, &f) || !openrouter.IsTemporary(f.err) {
			return completion, err
		}
		select {
		case <-ctx.Done():
			return nil, ErrCancelled
		case <-time.After(t.agent.RetryDelays[attempt-1]):
		}
	}
}

// complete makes one model request, forwards provisional output, and returns
// the validated completion.
func (t *Turn) complete(ctx context.Context) (*openrouter.Completion, error) {
	stream, err := t.agent.OpenRouter.Stream(ctx, t.request)
	if ctx.Err() != nil {
		return nil, ErrCancelled
	}
	if err != nil {
		return nil, modelFailure(err)
	}
	defer stream.Close()
	for {
		item, err := stream.Next()
		if ctx.Err() != nil {
			return nil, ErrCancelled
		}
		if err != nil {
			return nil, modelFailure(err)
		}
		switch {
		case item.Completion != nil:
			return item.Completion, nil
		case item.Text != "":
			err = t.client.Send(TextDelta(item.Text))
		case item.Reasoning != "":
			err = t.client.Send(ReasoningDelta(item.Reasoning))
		}
		if err != nil {
			return nil, updateFailure(err)
		}
	}
}

// processBatch announces, runs, and saves the tool calls of one validated
// assistant message. Every exit gives each call a final outcome and attempts
// to save the batch, so a stopped turn keeps the effects already observed.
func (t *Turn) processBatch(ctx context.Context, message transcript.AssistantMessage) error {
	batch := &transcript.AssistantBatch{Message: message}
	if err := t.execute(ctx, batch); err != nil {
		return t.completeInterrupted(batch, err)
	}
	if err := t.commit(batch); err != nil {
		return storageFailure(err)
	}
	return nil
}

// execute announces the calls, then runs them in order. Each outcome enters
// the batch before its update is sent, so an update failure does not erase
// completed work.
func (t *Turn) execute(ctx context.Context, batch *transcript.AssistantBatch) error {
	for _, call := range batch.Message.ToolCalls {
		if err := t.client.Send(ToolPending{call}); err != nil {
			return updateFailure(err)
		}
	}
	for _, call := range batch.Message.ToolCalls {
		if ctx.Err() != nil {
			return ErrCancelled
		}
		denial, err := t.approve(ctx, call)
		if err != nil {
			return err
		}
		outcome := transcript.Failed(denial)
		if denial == "" {
			if err := t.client.Send(ToolStarted{call}); err != nil {
				return updateFailure(err)
			}
			if ctx.Err() != nil {
				return ErrCancelled
			}
			outcome = t.toolbox.Execute(ctx, call)
		}
		batch.Outcomes = append(batch.Outcomes, outcome)
		if err := t.client.Send(ToolFinished{call, outcome}); err != nil {
			return updateFailure(err)
		}
	}
	return nil
}

// approve requests Ask mode permission when the call needs it, and returns the
// denial message when the user denies it.
func (t *Turn) approve(ctx context.Context, call transcript.ToolCall) (string, error) {
	permission := t.toolbox.Permission(call)
	if permission.Kind == tools.PermissionNone || t.mode == transcript.ModeAuto {
		return "", nil
	}
	approved, err := t.client.Approve(ctx, call, permission)
	switch {
	case ctx.Err() != nil || errors.Is(err, ErrCancelled):
		return "", ErrCancelled
	case err != nil:
		return "", permissionFailure(err)
	case approved:
		return "", nil
	case permission.Kind == tools.PermissionInput:
		return "User denied permission to send this input.", nil
	}
	return "User denied permission to run this command.", nil
}

// commit saves a complete batch. The transcript changes only after the
// database transaction succeeds.
func (t *Turn) commit(batch *transcript.AssistantBatch) error {
	if err := t.agent.Store.AppendBatch(t.summary.ID, batch); err != nil {
		return err
	}
	t.request.Transcript = append(t.request.Transcript, batch)
	return nil
}

// completeInterrupted gives every unstarted call an outcome saying why it did
// not run, saves the batch, sends the remaining updates, and returns the error
// that ends the turn.
func (t *Turn) completeInterrupted(batch *transcript.AssistantBatch, err error) error {
	placeholder := transcript.Cancelled("Cancelled before this tool was started.")
	var f *failure
	connectionFailed := false
	if errors.As(err, &f) {
		if f.reason == updateFailed {
			placeholder = transcript.Failed("Not started: the client connection failed before this tool ran.")
			connectionFailed = true
		} else {
			placeholder = transcript.Failed("Not started: requesting shell permission failed: " + f.err.Error())
		}
	}
	remaining := batch.Message.ToolCalls[len(batch.Outcomes):]
	for range remaining {
		batch.Outcomes = append(batch.Outcomes, placeholder)
	}
	if commitErr := t.commit(batch); commitErr != nil {
		return storageFailure(fmt.Errorf("%v; the batch was being completed because %v", commitErr, err))
	}
	if !connectionFailed {
		for _, call := range remaining {
			if sendErr := t.client.Send(ToolFinished{call, placeholder}); sendErr != nil {
				return updateFailure(sendErr)
			}
		}
	}
	return err
}

func hasImages(entries []transcript.Entry) bool {
	for _, entry := range entries {
		if start, ok := entry.(*transcript.TurnStart); ok && start.Input.HasImages() {
			return true
		}
	}
	return false
}
