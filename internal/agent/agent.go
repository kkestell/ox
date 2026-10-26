// Package agent runs one turn of a session: it runs the hooks that precede the
// turn, saves the user message or skill invocation, makes model requests, runs
// tools in call order and the hooks that follow them, saves each complete
// assistant batch, compacts the session when its context fills, and returns the
// answer when the turn finishes, its Stop hooks let it end, and its inbox is
// empty.
package agent

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ox/internal/catalog"
	"ox/internal/mcpclient"
	"ox/internal/provider"
	"ox/internal/settings"
	"ox/internal/shellproc"
	"ox/internal/store"
	"ox/internal/tools"
	"ox/internal/transcript"
)

// compactionPrompt asks the model for the summary that replaces the
// transcript in later requests.
const compactionPrompt = `The conversation above is the session so far between the user and you, Ox, a
coding agent working in the user's workspace. It will be replaced by your
summary followed by the user's last message above, word for word. A model that
sees only those and the messages that follow them must be able to continue the
work without asking the user to repeat anything.

Write the summary. Include:

- The user's requests and instructions, quoted where the exact words matter.
- What has been done: files read or changed, commands run, and their results.
- Decisions made and the reasons for them.
- The current state of the work and what remains to be done.
- Facts the work depends on: file paths, names, errors, and values.

Leave out anything the rest of the work does not need. Write plain text, not a
reply to the user. Do not call tools.`

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

// Context reports text added to the turn, as the model reads it.
type Context string

// CompactionStarted reports that the session is being compacted. ID names the
// compaction in its updates.
type CompactionStarted struct{ ID string }

// CompactionFinished reports how a compaction ended: completed with the
// summary as its text, failed, or cancelled.
type CompactionFinished struct {
	ID      string
	Outcome transcript.ToolOutcome
}

// Usage reports the context tokens, the context limit, and the session cost
// after each saved batch and compaction.
type Usage struct {
	Used uint64
	Size int
	Cost *float64
}

func (SessionInfo) event()        {}
func (TextDelta) event()          {}
func (ReasoningDelta) event()     {}
func (ToolPending) event()        {}
func (ToolStarted) event()        {}
func (ToolFinished) event()       {}
func (Context) event()            {}
func (CompactionStarted) event()  {}
func (CompactionFinished) event() {}
func (Usage) event()              {}

// Client receives a turn's events, reports the session's mode, and answers Ask
// mode permission requests.
type Client interface {
	Send(Event) error
	// Mode returns the session's current mode. The turn reads it when the
	// turn starts and before each batch of tool calls, so a change during the
	// turn applies from the next batch.
	Mode() transcript.Mode
	// Approve asks whether call may run. It returns ErrCancelled when the
	// client cancelled the request.
	Approve(ctx context.Context, call transcript.ToolCall, permission tools.Permission) (bool, error)
}

// ErrCancelled means the prompt was cancelled.
var ErrCancelled = errors.New("the prompt was cancelled")

// ErrImagesUnsupported means the session has an image and the selected model
// does not accept images.
var ErrImagesUnsupported = errors.New("the selected model does not accept images; choose a model that accepts images")

// Agent runs turns against a session store and the model providers.
type Agent struct {
	Store *store.Store
	// Providers holds a provider for each provider name in the catalog.
	Providers map[string]provider.Provider
	Catalog   catalog.Catalog
	// Hooks run during every turn, in Ask and Auto mode alike, without
	// approval: they come from the user's own global settings.
	Hooks settings.Hooks
	// RetryDelays are the waits before the second and third attempts of a
	// model request, since retrying a temporary failure at once tends to fail
	// the same way.
	RetryDelays [modelRequestAttempts - 1]time.Duration
}

// New returns an agent with the standard retry delays.
func New(s *store.Store, providers map[string]provider.Provider, cat catalog.Catalog) *Agent {
	return &Agent{Store: s, Providers: providers, Catalog: cat, RetryDelays: [2]time.Duration{2 * time.Second, 8 * time.Second}}
}

// Input is what starts a turn.
type Input struct {
	SessionID string
	Input     transcript.TurnInput
	// Model and Effort are the session's selections, used for every model
	// request in the turn.
	Model        string
	Effort       catalog.Effort
	SystemPrompt string
	// Processes are the session's background processes, which outlive the
	// turn.
	Processes *shellproc.Processes
	MCP       *mcpclient.Catalog
	// SessionStart runs the SessionStart hooks before the turn: set for the
	// session's first turn in this process.
	SessionStart bool
	// Inbox takes waiting messages, already formatted for the model. A nil
	// Inbox delivers nothing.
	Inbox func() []string
}

// Stop is why a turn ended without an error.
type Stop int

const (
	EndTurn Stop = iota
	Cancelled
	MaxTokens
	Refusal
)

// Result is how a turn ended, with the answer when it finished and the tokens
// its model requests used.
type Result struct {
	Stop   Stop
	Answer string
	Tokens Tokens
}

// Tokens counts the tokens of model requests. Input includes Cached, and
// Output includes Reasoning, as the providers report them.
type Tokens struct {
	Input, Cached, Output, Reasoning uint64
}

func (t *Tokens) add(usage *transcript.Usage) {
	if usage != nil {
		t.Input += usage.InputTokens
		t.Cached += usage.CachedTokens
		t.Output += usage.OutputTokens
		t.Reasoning += usage.ReasoningTokens
	}
}

// Turn is one turn whose turn start is saved.
type Turn struct {
	agent *Agent
	// provider serves every model request of the turn.
	provider provider.Provider
	client   Client
	request  provider.Request
	summary  store.Summary
	toolbox  *tools.Toolbox
	info     *SessionInfo
	inbox    func() []string
	tokens   Tokens
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

// Start reads the session, runs the hooks that precede the turn, and saves the
// turn start with the selected model and effort, the client's current mode,
// and added context before any model request. Nothing is saved when ctx
// is already done; the turn then runs as cancelled.
func (a *Agent) Start(ctx context.Context, in Input, client Client) (*Turn, error) {
	session, err := a.Store.Read(in.SessionID)
	if err != nil {
		return nil, err
	}
	model, err := ModelSelection(a.Catalog, in.Model, in.Effort)
	if err != nil {
		return nil, err
	}
	toolbox := &tools.Toolbox{Workspace: session.Summary.Workspace, Processes: in.Processes, MCP: in.MCP}
	t := &Turn{
		agent:    a,
		provider: a.Providers[model.Provider],
		client:   client,
		request: provider.Request{
			Model: model, Effort: in.Effort, SystemPrompt: in.SystemPrompt, Tools: toolbox.Schemas(), Transcript: session.Transcript,
			SessionID: in.SessionID,
		},
		summary: session.Summary,
		toolbox: toolbox,
		inbox:   in.Inbox,
	}
	if ctx.Err() != nil {
		return t, nil
	}
	// An earlier image fails every request to a model without image input.
	if !model.AcceptsImages && (in.Input.HasImages() || hasImages(transcript.RequestEntries(session.Transcript))) {
		return nil, ErrImagesUnsupported
	}
	// The context is saved even if the turn is cancelled meanwhile, since a
	// hook or inbox may hand over text only once.
	added := t.promptHooks(ctx, in.SessionStart, len(session.Transcript), in.Input)
	added = append(added, t.messages()...)
	start := &transcript.TurnStart{Model: model.QualifiedID(), Effort: in.Effort, Mode: t.client.Mode(), Input: in.Input, Context: added}
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
	if errors.Is(err, provider.ErrContextOverflow) {
		err = errors.New("the session exceeds the model context limit; start a new session")
	}
	return &failure{"the model request failed", err}
}

// updateFailed is the reason of a failure to send an update to the client.
const updateFailed = "sending an ACP update failed"

func updateFailure(err error) error { return &failure{updateFailed, err} }

func permissionFailure(err error) error {
	return &failure{"requesting tool permission failed", err}
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
		return Result{Stop: Cancelled, Tokens: t.tokens}, nil
	}
	if err != nil {
		if saveErr := t.agent.Store.AppendTurnError(t.summary.ID, err.Error()); saveErr != nil {
			return Result{}, storageFailure(fmt.Errorf("%v; a turn error was being saved because %v", saveErr, err))
		}
		t.request.Transcript = append(t.request.Transcript, transcript.TurnError(err.Error()))
		return Result{}, err
	}
	result.Tokens = t.tokens
	return result, nil
}

func (t *Turn) loop(ctx context.Context) (Result, error) {
	if err := t.client.Send(*t.info); err != nil {
		return Result{}, updateFailure(err)
	}
	if added := t.request.Transcript[len(t.request.Transcript)-1].(*transcript.TurnStart).Context; len(added) > 0 {
		if err := t.client.Send(Context(transcript.ContextMessage(added))); err != nil {
			return Result{}, updateFailure(err)
		}
	}
	stopHookActive := false
	for {
		if ctx.Err() != nil {
			return Result{}, ErrCancelled
		}
		if t.needsCompaction() {
			if err := t.compact(ctx); err != nil {
				return Result{}, err
			}
		}
		completion, err := t.requestWithRetries(ctx, t.request, true)
		if err != nil {
			return Result{}, err
		}
		var added []string
		if completion.Stop == provider.StopFinished {
			added = t.stop(ctx, stopHookActive)
			stopHookActive = stopHookActive || len(added) > 0
			added = append(added, t.messages()...)
		}
		if err := t.processBatch(ctx, completion.Message, added); err != nil {
			return Result{}, err
		}
		used, cost, _ := transcript.UsageSummary(t.request.Transcript)
		if err := t.client.Send(Usage{Used: used, Size: t.request.Model.ContextLimit, Cost: cost}); err != nil {
			return Result{}, updateFailure(err)
		}
		switch {
		case completion.Stop == provider.StopFinished && len(added) == 0:
			return Result{Stop: EndTurn, Answer: completion.Message.Text}, nil
		case completion.Stop == provider.StopTokenLimit:
			return Result{Stop: MaxTokens}, nil
		case completion.Stop == provider.StopRefused:
			return Result{Stop: Refusal}, nil
		}
	}
}

func (t *Turn) needsCompaction() bool {
	if t.request.Model.ContextLimit <= 0 {
		return false
	}
	limit := uint64(t.request.Model.ContextLimit)
	for i := len(t.request.Transcript) - 1; i >= 0; i-- {
		switch entry := t.request.Transcript[i].(type) {
		case *transcript.Compaction:
			return false
		case *transcript.AssistantBatch:
			usage := entry.Message.Usage
			return usage != nil && usage.InputTokens >= limit-limit/5
		}
	}
	return false
}

// compact replaces the transcript in later requests with a summary the model
// writes. Nothing is saved when it fails or is cancelled.
func (t *Turn) compact(ctx context.Context) error {
	id := "compaction-" + rand.Text()
	if err := t.client.Send(CompactionStarted{id}); err != nil {
		return updateFailure(err)
	}
	compaction, err := t.summarize(ctx)
	var outcome transcript.ToolOutcome
	switch {
	case errors.Is(err, ErrCancelled):
		outcome = transcript.Cancelled("Cancelled before the compaction finished.")
	case err != nil:
		outcome = transcript.Failed(err.Error())
	default:
		outcome = transcript.Completed(compaction.Summary)
	}
	// A compaction error ends the turn whether or not its update is sent.
	if sendErr := t.client.Send(CompactionFinished{id, outcome}); err != nil {
		return err
	} else if sendErr != nil {
		return updateFailure(sendErr)
	}
	used, cost, _ := transcript.UsageSummary(t.request.Transcript)
	if err := t.client.Send(Usage{Used: used, Size: t.request.Model.ContextLimit, Cost: cost}); err != nil {
		return updateFailure(err)
	}
	return nil
}

// summarize asks the model for a summary of the transcript and saves it as a
// compaction.
func (t *Turn) summarize(ctx context.Context) (*transcript.Compaction, error) {
	prompt := transcript.TextMessage(compactionPrompt)
	request := t.request
	// The prompt exists only in this request and is never saved.
	request.Transcript = slices.Concat(t.request.Transcript, []transcript.Entry{&transcript.TurnStart{Input: transcript.TurnInput{Message: &prompt}}})
	completion, err := t.requestWithRetries(ctx, request, false)
	if err != nil {
		return nil, err
	}
	var reason string
	switch {
	case completion.Stop == provider.StopTokenLimit:
		reason = "the summary reached the output token limit"
	case completion.Stop == provider.StopRefused:
		reason = "the model refused to write a summary"
	case strings.TrimSpace(completion.Message.Text) == "":
		reason = "the model returned an empty summary"
	}
	if reason != "" {
		return nil, &failure{"compacting the session failed", errors.New(reason)}
	}
	compaction := &transcript.Compaction{Summary: completion.Message.Text, Usage: completion.Message.Usage}
	if err := t.agent.Store.AppendCompaction(t.summary.ID, compaction); err != nil {
		return nil, storageFailure(err)
	}
	t.request.Transcript = append(t.request.Transcript, compaction)
	return compaction, nil
}

// requestWithRetries makes one model request, retrying a temporary failure.
// Provisional output of a failed attempt stays on screen.
func (t *Turn) requestWithRetries(ctx context.Context, request provider.Request, forward bool) (*provider.Completion, error) {
	for attempt := 1; ; attempt++ {
		completion, err := t.complete(ctx, request, forward)
		var f *failure
		if attempt == modelRequestAttempts || !errors.As(err, &f) || !provider.IsTemporary(f.err) {
			return completion, err
		}
		select {
		case <-ctx.Done():
			return nil, ErrCancelled
		case <-time.After(t.agent.RetryDelays[attempt-1]):
		}
	}
}

// complete makes one model request, forwards provisional output when forward
// is set, and returns the validated completion.
func (t *Turn) complete(ctx context.Context, request provider.Request, forward bool) (*provider.Completion, error) {
	stream, err := t.provider.Stream(ctx, request)
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
			t.tokens.add(item.Completion.Message.Usage)
			return item.Completion, nil
		case !forward:
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

func (t *Turn) messages() []string {
	if t.inbox == nil {
		return nil
	}
	return t.inbox()
}

// processBatch announces, runs, and saves the tool calls of one validated
// assistant message, followed by context added at stop.
// Every exit gives each call a final outcome and attempts to save the batch,
// so a stopped turn keeps the effects already observed.
func (t *Turn) processBatch(ctx context.Context, message transcript.AssistantMessage, added []string) error {
	batch := &transcript.AssistantBatch{Message: message}
	if err := t.execute(ctx, batch); err != nil {
		return t.completeInterrupted(batch, err)
	}
	if len(added) > 0 {
		batch.Context = append(batch.Context, added...)
		if err := t.client.Send(Context(transcript.ContextMessage(added))); err != nil {
			return t.completeInterrupted(batch, updateFailure(err))
		}
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
	mode := t.client.Mode()
	for _, call := range batch.Message.ToolCalls {
		if ctx.Err() != nil {
			return ErrCancelled
		}
		denial, err := t.approve(ctx, call, mode)
		if err != nil {
			return err
		}
		outcome := transcript.Failed(denial)
		var added []string
		if denial == "" {
			if err := t.client.Send(ToolStarted{call}); err != nil {
				return updateFailure(err)
			}
			if ctx.Err() != nil {
				return ErrCancelled
			}
			outcome = t.toolbox.Execute(ctx, call)
			added = t.postToolUse(ctx, call, outcome)
		}
		batch.Outcomes = append(batch.Outcomes, outcome)
		if err := t.client.Send(ToolFinished{call, outcome}); err != nil {
			return updateFailure(err)
		}
		added = append(added, t.messages()...)
		if len(added) > 0 {
			batch.Context = append(batch.Context, added...)
			if err := t.client.Send(Context(transcript.ContextMessage(added))); err != nil {
				return updateFailure(err)
			}
		}
	}
	return nil
}

// approve requests Ask mode permission when the call needs it, and returns the
// denial message when the user denies it.
func (t *Turn) approve(ctx context.Context, call transcript.ToolCall, mode transcript.Mode) (string, error) {
	permission := t.toolbox.Permission(call)
	if permission.Kind == tools.PermissionNone || mode == transcript.ModeAuto {
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
	case permission.Kind == tools.PermissionMCP:
		return "User denied permission to call this MCP tool.", nil
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
			placeholder = transcript.Failed("Not started: requesting tool permission failed: " + f.err.Error())
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
