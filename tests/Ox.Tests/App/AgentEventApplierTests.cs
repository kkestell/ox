using Ox.Agent.AgentLoop;
using Ox.App;
using Ox.App.Configuration;
using Ox.App.Conversation;
using Ox.App.Views;
using Ox.Tests.TestSupport;

namespace Ox.Tests.App;

/// <summary>
/// Unit tests for <see cref="AgentEventApplier"/>. These lock down the event →
/// view/state transitions that used to live as the 25-complexity
/// <c>DrainAgentEvents</c> switch on OxApp. Each test pokes a single event at
/// the applier and asserts the observable effects on the view and turn state.
/// </summary>
public sealed class AgentEventApplierTests
{
    private static (AgentEventApplier applier, ConversationView view, TurnState state) Build(string? activeModelId = null)
    {
        var view = new ConversationView();
        var catalog = new ModelCatalog(TestProviderConfig.CreateDefault(), Array.Empty<Ox.Agent.Providers.IProvider>());
        var applier = new AgentEventApplier(view, catalog, () => activeModelId);
        return (applier, view, new TurnState());
    }

    [Fact]
    public void Apply_ResponseChunk_CreatesAssistantEntryAndAppends()
    {
        var (applier, view, state) = Build();

        applier.Apply(new ResponseChunk { Text = "hello " }, state);
        applier.Apply(new ResponseChunk { Text = "world" }, state);

        // Both chunks coalesce into one AssistantTextEntry.
        var entry = Assert.IsType<AssistantTextEntry>(Assert.Single(view.Entries));
        Assert.Equal("hello world", entry.Text);
        Assert.Same(entry, state.CurrentAssistantEntry);
    }

    [Fact]
    public void Apply_EmptyResponseChunk_IsSkipped()
    {
        var (applier, view, state) = Build();

        applier.Apply(new ResponseChunk { Text = "" }, state);

        // An empty chunk must not create a blank bubble.
        Assert.Empty(view.Entries);
    }

    [Fact]
    public void Apply_ThinkingChunk_CreatesThinkingEntry()
    {
        var (applier, view, state) = Build();

        applier.Apply(new ThinkingChunk { Text = "hmm " }, state);
        applier.Apply(new ThinkingChunk { Text = "let me think" }, state);

        var entry = Assert.IsType<ThinkingEntry>(Assert.Single(view.Entries));
        Assert.Equal("hmm let me think", entry.Text);
    }

    [Fact]
    public void Apply_ResponseChunk_AfterThinking_ClearsThinkingEntry()
    {
        // Response after thinking must not allow a later thinking chunk in the
        // same turn to accumulate into the closed entry. The applier
        // demonstrates this by nulling CurrentThinkingEntry on the first
        // non-empty ResponseChunk.
        var (applier, _, state) = Build();

        applier.Apply(new ThinkingChunk { Text = "hmm" }, state);
        applier.Apply(new ResponseChunk { Text = "answer" }, state);

        Assert.Null(state.CurrentThinkingEntry);
        Assert.NotNull(state.CurrentAssistantEntry);
    }

    [Fact]
    public void Apply_ToolCallStarted_AddsToolEntryAndClearsStreamingState()
    {
        var (applier, view, state) = Build();

        applier.Apply(new ResponseChunk { Text = "x" }, state);
        applier.Apply(new ToolCallStarted
        {
            CallId = "c1",
            ToolName = "read_file",
            Arguments = new Dictionary<string, object?>(),
        }, state);

        Assert.Null(state.CurrentAssistantEntry);
        Assert.Null(state.CurrentThinkingEntry);
        Assert.Equal(2, view.Entries.Count);
        var tool = Assert.IsType<ToolCallEntry>(view.Entries[1]);
        Assert.Equal(ToolCallStatus.Started, tool.Status);
    }

    [Fact]
    public void Apply_ToolAwaitingApproval_UpdatesMatchingEntryStatus()
    {
        var (applier, view, state) = Build();

        applier.Apply(new ToolCallStarted
        {
            CallId = "c1",
            ToolName = "write_file",
            Arguments = new Dictionary<string, object?>(),
        }, state);
        applier.Apply(new ToolAwaitingApproval { CallId = "c1" }, state);

        var tool = Assert.IsType<ToolCallEntry>(Assert.Single(view.Entries));
        Assert.Equal(ToolCallStatus.AwaitingApproval, tool.Status);
    }

    [Fact]
    public void Apply_ToolCallCompleted_Success_MarksSucceeded()
    {
        var (applier, view, state) = Build();

        applier.Apply(new ToolCallStarted
        {
            CallId = "c1",
            ToolName = "read_file",
            Arguments = new Dictionary<string, object?>(),
        }, state);
        applier.Apply(new ToolCallCompleted { CallId = "c1", ToolName = "read_file", Result = "ok", IsError = false }, state);

        var tool = Assert.IsType<ToolCallEntry>(Assert.Single(view.Entries));
        Assert.Equal(ToolCallStatus.Succeeded, tool.Status);
        Assert.Equal("ok", tool.Result);
        Assert.False(tool.IsError);
    }

    [Fact]
    public void Apply_ToolCallCompleted_Error_MarksFailed()
    {
        var (applier, view, state) = Build();

        applier.Apply(new ToolCallStarted
        {
            CallId = "c1",
            ToolName = "bash",
            Arguments = new Dictionary<string, object?>(),
        }, state);
        applier.Apply(new ToolCallCompleted { CallId = "c1", ToolName = "bash", Result = "boom", IsError = true }, state);

        var tool = Assert.IsType<ToolCallEntry>(Assert.Single(view.Entries));
        Assert.Equal(ToolCallStatus.Failed, tool.Status);
        Assert.True(tool.IsError);
    }

    [Fact]
    public void Apply_TurnCompleted_ReturnsTurnEnded()
    {
        var (applier, _, state) = Build();

        var outcome = applier.Apply(new TurnCompleted(), state);

        Assert.Equal(DrainOutcome.TurnEnded, outcome);
    }

    [Fact]
    public void Apply_FatalTurnError_ReturnsTurnEndedFatalAndAddsErrorEntry()
    {
        var (applier, view, state) = Build();

        var outcome = applier.Apply(new TurnError { Message = "boom", IsFatal = true }, state);

        Assert.Equal(DrainOutcome.TurnEndedFatal, outcome);
        Assert.IsType<ErrorEntry>(Assert.Single(view.Entries));
    }

    [Fact]
    public void Apply_NonFatalTurnError_ReturnsNoneButAddsErrorEntry()
    {
        // Non-fatal errors (e.g. unknown slash commands) surface in the
        // transcript without ending the turn.
        var (applier, view, state) = Build();

        var outcome = applier.Apply(new TurnError { Message = "bad command", IsFatal = false }, state);

        Assert.Equal(DrainOutcome.None, outcome);
        Assert.IsType<ErrorEntry>(Assert.Single(view.Entries));
    }

    [Fact]
    public void Apply_TodoUpdated_AddsPlanEntryAndResetsStreamingState()
    {
        var (applier, view, state) = Build();
        applier.Apply(new ResponseChunk { Text = "x" }, state);

        var update = new TodoUpdated
        {
            Items =
            [
                new Ox.Agent.Todo.TodoItem("first",  Ox.Agent.Todo.TodoStatus.Pending),
                new Ox.Agent.Todo.TodoItem("second", Ox.Agent.Todo.TodoStatus.InProgress),
                new Ox.Agent.Todo.TodoItem("third",  Ox.Agent.Todo.TodoStatus.Completed),
            ]
        };

        applier.Apply(update, state);

        Assert.Null(state.CurrentAssistantEntry);
        var plan = Assert.IsType<PlanEntry>(view.Entries[^1]);
        Assert.Collection(plan.Items,
            a => Assert.Equal(PlanItemStatus.Pending, a.Status),
            b => Assert.Equal(PlanItemStatus.InProgress, b.Status),
            c => Assert.Equal(PlanItemStatus.Completed, c.Status));
    }

    [Fact]
    public void Apply_Compacted_ReturnsNone_WithNoViewMutation()
    {
        // Compacted is a non-visible turn-internal signal: the TUI does not
        // render anything for it, so the applier must leave the view untouched
        // and report DrainOutcome.None. If someone adds a view mutation to the
        // Compacted case, this test will catch it.
        var (applier, view, state) = Build();
        var outcome = applier.Apply(new Compacted { Message = "ctx" }, state);

        Assert.Equal(DrainOutcome.None, outcome);
        Assert.Empty(view.Entries);
    }

    [Fact]
    public void Apply_FullTurnSequence_ProducesExpectedEntryOrderAndResetsState()
    {
        // End-to-end sweep: thinking → response → tool call → tool completed →
        // turn completed. Pins both the streaming-entry reset points (tool
        // call must null out CurrentAssistantEntry / CurrentThinkingEntry) and
        // the final DrainOutcome. Single-event tests would miss ordering bugs
        // in ResetStreamingEntries placement.
        var (applier, view, state) = Build();

        Assert.Equal(DrainOutcome.None, applier.Apply(new ThinkingChunk { Text = "hmm" }, state));
        Assert.Equal(DrainOutcome.None, applier.Apply(new ResponseChunk { Text = "here: " }, state));
        Assert.Equal(DrainOutcome.None, applier.Apply(new ToolCallStarted
        {
            CallId = "c1",
            ToolName = "read_file",
            Arguments = new Dictionary<string, object?>(),
        }, state));
        Assert.Equal(DrainOutcome.None, applier.Apply(new ToolCallCompleted
        {
            CallId = "c1",
            ToolName = "read_file",
            Result = "ok",
            IsError = false,
        }, state));
        var final = applier.Apply(new TurnCompleted(), state);

        // Order matters: thinking entry, assistant entry, tool entry.
        Assert.Collection(view.Entries,
            e => Assert.IsType<ThinkingEntry>(e),
            e => Assert.IsType<AssistantTextEntry>(e),
            e => Assert.IsType<ToolCallEntry>(e));

        // After the ToolCallStarted, streaming entries must have been reset so
        // a follow-on ResponseChunk would create a fresh bubble rather than
        // appending to the one that preceded the tool call.
        Assert.Null(state.CurrentAssistantEntry);
        Assert.Null(state.CurrentThinkingEntry);

        // Tool entry reached the Succeeded terminal state.
        var tool = view.Entries.OfType<ToolCallEntry>().Single();
        Assert.Equal(ToolCallStatus.Succeeded, tool.Status);

        Assert.Equal(DrainOutcome.TurnEnded, final);
    }

    [Fact]
    public void Apply_TurnCompleted_WithUnknownModel_LeavesContextPercentNull()
    {
        // No active model ID → no context fill computation is possible.
        var (applier, _, state) = Build(activeModelId: null);

        applier.Apply(new TurnCompleted { InputTokens = 1000 }, state);

        Assert.Null(state.ContextPercent);
    }
}
