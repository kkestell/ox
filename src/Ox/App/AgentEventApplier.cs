using Ox.Agent.AgentLoop;
using Ox.Agent.Todo;
using Ox.App.Configuration;
using Ox.App.Conversation;
using Ox.App.Views;

namespace Ox.App;

/// <summary>
/// The end state a single <see cref="AgentEventApplier.Apply"/> call reports
/// back to the main loop. Anything beyond conversation-view mutation —
/// throbber resets, starting a queued turn — stays in <see cref="OxApp"/> so
/// there's a single place that decides turn-lifecycle transitions.
/// </summary>
internal enum DrainOutcome
{
    /// <summary>Event processed without ending the turn.</summary>
    None,

    /// <summary>The turn ended normally (<see cref="TurnCompleted"/>).</summary>
    TurnEnded,

    /// <summary>The turn ended due to a fatal <see cref="TurnError"/>.</summary>
    TurnEndedFatal,
}

/// <summary>
/// Mutable state shared across event applications within a turn. Holds the
/// "currently open" entries that streaming chunks append to, plus the latest
/// context-fill percentage for the status line.
///
/// Before the extraction, these lived as private fields on <see cref="OxApp"/>
/// alongside unrelated concerns. Making them a discrete state bag means the
/// applier can be unit-tested by pointing it at a fresh <c>TurnState</c>
/// without having to stand up a full OxApp.
/// </summary>
internal sealed class TurnState
{
    public AssistantTextEntry? CurrentAssistantEntry { get; set; }

    // Tracks the active ThinkingEntry for the current turn. Nulled at every
    // boundary where CurrentAssistantEntry is also nulled so thinking from one
    // turn never bleeds into the next.
    public ThinkingEntry? CurrentThinkingEntry { get; set; }

    public int? ContextPercent { get; set; }

    /// <summary>Resets streaming-entry tracking between turns and on cancellation.</summary>
    public void ResetStreamingEntries()
    {
        CurrentAssistantEntry = null;
        CurrentThinkingEntry = null;
    }
}

/// <summary>
/// Applies <see cref="AgentLoopEvent"/> values produced by
/// <see cref="TurnController"/> to the <see cref="ConversationView"/> and the
/// shared <see cref="TurnState"/>.
///
/// The applier is a pure event → state transition. Turn-lifecycle decisions
/// (reset the throbber, start a queued turn) stay in the coordinator —
/// <see cref="Apply"/> merely signals them via <see cref="DrainOutcome"/>.
/// This keeps the class free of <see cref="TurnController"/> and throbber
/// references and makes it straightforward to unit-test with just a mock
/// view and a state bag.
/// </summary>
internal sealed class AgentEventApplier(ConversationView view, ModelCatalog catalog, Func<string?> activeModelIdSource)
{
    // Context-window cache — keyed on model ID so we only resolve each window
    // once per session. Populated lazily on the first TurnCompleted for each
    // model. All access is on the main thread (the applier only runs there).
    private readonly Dictionary<string, int?> _contextWindowCache = new(StringComparer.OrdinalIgnoreCase);

    /// <summary>
    /// Applies one event to the view/state and returns whether the turn has
    /// ended as a result. The context-window cache is cleared from outside
    /// (e.g. on /model switch) via <see cref="InvalidateContextWindowCache"/>.
    /// </summary>
    public DrainOutcome Apply(AgentLoopEvent evt, TurnState state)
    {
        switch (evt)
        {
            case ThinkingChunk thinkingChunk:
                // Skip empty chunks to avoid creating blank thinking entries.
                if (thinkingChunk.Text.Length == 0)
                    break;

                if (state.CurrentThinkingEntry is null)
                {
                    state.CurrentThinkingEntry = new ThinkingEntry();
                    view.AddEntry(state.CurrentThinkingEntry);
                }
                state.CurrentThinkingEntry.Append(thinkingChunk.Text);
                break;

            case ResponseChunk chunk:
                // Skip empty chunks — they'd create blank conversation bubbles.
                if (chunk.Text.Length == 0)
                    break;

                // Normal response text starts after thinking — a new ThinkingEntry
                // must not accumulate text from a subsequent turn's thinking phase.
                state.CurrentThinkingEntry = null;

                if (state.CurrentAssistantEntry is null)
                {
                    state.CurrentAssistantEntry = new AssistantTextEntry();
                    view.AddEntry(state.CurrentAssistantEntry);
                }
                state.CurrentAssistantEntry.Append(chunk.Text);
                break;

            case ToolCallStarted toolStart:
                // New tool call breaks the in-progress assistant/thinking text.
                state.ResetStreamingEntries();
                view.AddEntry(new ToolCallEntry
                {
                    CallId = toolStart.CallId,
                    ToolName = toolStart.ToolName,
                    FormattedSignature = toolStart.FormatCall(),
                    Status = ToolCallStatus.Started,
                });
                break;

            case ToolAwaitingApproval awaiting:
                var awaitingEntry = view.FindToolCall(awaiting.CallId);
                if (awaitingEntry is not null)
                    awaitingEntry.Status = ToolCallStatus.AwaitingApproval;
                break;

            case ToolCallCompleted completed:
                var completedEntry = view.FindToolCall(completed.CallId);
                if (completedEntry is not null)
                {
                    completedEntry.Status = completed.IsError
                        ? ToolCallStatus.Failed
                        : ToolCallStatus.Succeeded;
                    completedEntry.Result = completed.Result;
                    completedEntry.IsError = completed.IsError;
                }
                break;

            case TodoUpdated todoUpdated:
                state.ResetStreamingEntries();
                view.AddEntry(new PlanEntry
                {
                    Items = todoUpdated.Items.Select(i => new PlanItem(
                        i.Content,
                        i.Status switch
                        {
                            TodoStatus.Completed => PlanItemStatus.Completed,
                            TodoStatus.InProgress => PlanItemStatus.InProgress,
                            _ => PlanItemStatus.Pending,
                        })).ToList(),
                });
                break;

            case SubagentEvent subEvt:
                view.HandleSubagentEvent(subEvt);
                break;

            case TurnCompleted turnCompleted:
                if (turnCompleted.InputTokens is { } tokens)
                    state.ContextPercent = ComputeContextPercent(tokens);
                return DrainOutcome.TurnEnded;

            case TurnError error:
                view.AddEntry(new ErrorEntry(error.Message));
                if (error.IsFatal)
                    return DrainOutcome.TurnEndedFatal;
                break;
        }

        return DrainOutcome.None;
    }

    /// <summary>
    /// Invalidates the cached context windows. Called when the active model
    /// changes so the status line picks up the new model's window size on
    /// the next completed turn.
    /// </summary>
    public void InvalidateContextWindowCache() => _contextWindowCache.Clear();

    // Compute context fill % using the provider's reported context window.
    // The cache avoids re-resolving every turn — each provider also caches
    // internally, so this is a cheap dictionary lookup after the first call.
    private int? ComputeContextPercent(long inputTokens)
    {
        var modelId = activeModelIdSource();
        if (modelId is null)
            return null;

        if (!_contextWindowCache.TryGetValue(modelId, out var contextWindow))
        {
            // First time seeing this model — resolve and cache. ResolveContextWindow
            // returns null for models not declared in providers.json (e.g. FakeProvider).
            contextWindow = catalog.ResolveContextWindow(modelId);
            _contextWindowCache[modelId] = contextWindow;
        }

        return contextWindow is > 0
            ? (int)Math.Max(1, inputTokens * 100 / contextWindow.Value)
            : null;
    }
}
