using Terminal.Gui.App;
using Terminal.Gui.Drawing;
using Ur.AgentLoop;
using Ox.Views;

namespace Ox;

/// <summary>
/// Routes <see cref="AgentLoopEvent"/>s to <see cref="ConversationEntry"/> data models
/// that the <see cref="ConversationView"/> draws.
///
/// The router is the only layer that knows about tool call IDs and subagent
/// IDs — everything above (the REPL loop) and below (the view) is ignorant
/// of these identifiers.
///
/// All mutations to entries must happen on the UI thread via Application.Invoke
/// since agent events arrive on background threads.
/// </summary>
internal sealed class EventRouter(ConversationView conversationView)
{
    // Tool name for the run_subagent tool. Used to differentiate subagent
    // tool calls (which get nested entries) from regular tool calls.
    private const string SubagentToolName = "run_subagent";
    private const string TodoToolName = "todo_write";

    // Cap how many result lines we show beneath a tool signature.
    private const int MaxResultLines = 5;

    // Maximum inner rows for subagent blocks before tail-clipping.
    private const int MaxSubagentInnerRows = 20;

    // ---- Main-stream state ----

    // Active streaming text entry for the main agent. Null between turns.
    private ConversationEntry? _currentText;

    // Maps CallId to (entry, state) for in-flight main-stream tool calls.
    private readonly Dictionary<string, ToolEntryState> _toolCallMap = new();

    // CallIds of run_subagent invocations.
    private readonly HashSet<string> _subagentCallIds = [];

    // Queues run_subagent calls with their formatted signatures.
    private readonly Queue<(string CallId, string FormattedCall)> _pendingSubagentCalls = new();
    private readonly Dictionary<string, string> _callIdToSubagentId = new();

    // ---- Subagent state (keyed by SubagentId) ----

    private readonly Dictionary<string, SubagentState> _subagentById = new();

    /// <summary>
    /// Routes a main-stream event. Called for every event yielded by
    /// <c>session.RunTurnAsync</c>. Must be called via Application.Invoke
    /// when on a background thread.
    /// </summary>
    public void RouteMainEvent(AgentLoopEvent evt)
    {
        switch (evt)
        {
            case ResponseChunk { Text: var text } when !string.IsNullOrEmpty(text):
                // Lazy creation: a new entry begins each run of response text.
                if (_currentText is null)
                {
                    _currentText = new ConversationEntry(EntryStyle.Circle,
                        () => new Color(ColorName16.White));
                    _currentText.AppendSegment("", new Color(ColorName16.White));
                    conversationView.AddEntry(_currentText);
                }
                // Append to the last segment's text.
                AppendToLastSegment(_currentText, text, new Color(ColorName16.White));
                break;

            case ToolCallStarted { ToolName: SubagentToolName } started:
                // run_subagent: mark this CallId and queue the formatted call.
                _subagentCallIds.Add(started.CallId);
                _pendingSubagentCalls.Enqueue((started.CallId, started.FormatCall()));
                _currentText = null;
                break;

            case ToolCallStarted started:
                // Create the tool state first so the dynamic circle color closure
                // can capture it. The entry is constructed directly with the color
                // supplier — no intermediate copy needed.
                var toolState = new ToolEntryState
                {
                    SuppressCompletionResult = started.ToolName == TodoToolName
                };
                _toolCallMap[started.CallId] = toolState;

                var capturedState = toolState;
                var toolEntry = new ConversationEntry(EntryStyle.Circle,
                    () => capturedState.CircleColor);
                toolEntry.AppendSegment(started.FormatCall(), new Color(ColorName16.DarkGray));
                toolState.DisplayEntry = toolEntry;

                conversationView.AddEntry(toolEntry);
                _currentText = null;
                break;

            case ToolAwaitingApproval { CallId: var awaitingCallId }:
                if (_toolCallMap.TryGetValue(awaitingCallId, out var awaitingState))
                {
                    awaitingState.State = ToolLifecycle.AwaitingApproval;
                    awaitingState.DisplayEntry?.NotifyChanged();
                }
                break;

            case ToolCallCompleted completed when _subagentCallIds.Contains(completed.CallId):
                // Defensive finalization for subagent calls.
                if (_callIdToSubagentId.TryGetValue(completed.CallId, out var subId)
                    && _subagentById.TryGetValue(subId, out var subState))
                {
                    subState.SetCompleted();
                    _callIdToSubagentId.Remove(completed.CallId);
                }
                _subagentCallIds.Remove(completed.CallId);
                break;

            case ToolCallCompleted completed:
                if (_toolCallMap.TryGetValue(completed.CallId, out var completedState))
                {
                    completedState.SetCompleted(completed.IsError, completed.Result);
                    _toolCallMap.Remove(completed.CallId);
                }
                break;

            case TurnCompleted:
                _currentText = null;
                break;

            case TurnError { Message: var msg }:
                var errorEntry = new ConversationEntry(EntryStyle.Circle,
                    () => new Color(ColorName16.Red));
                errorEntry.AppendSegment($"[error] {msg}", new Color(ColorName16.Red));
                conversationView.AddEntry(errorEntry);
                _currentText = null;
                break;
        }
    }

    /// <summary>
    /// Routes a <see cref="SubagentEvent"/> received via TurnCallbacks.
    /// Creates a subagent entry on first event for a new SubagentId.
    /// </summary>
    public void RouteSubagentEvent(SubagentEvent evt)
    {
        var subIdValue = evt.SubagentId;

        // Lazily create the subagent entry on first event.
        if (!_subagentById.TryGetValue(subIdValue, out var subState))
        {
            var formattedCall = "";
            if (_pendingSubagentCalls.TryDequeue(out var pending))
            {
                _callIdToSubagentId[pending.CallId] = subIdValue;
                formattedCall = pending.FormattedCall;
            }

            subState = new SubagentState(subIdValue, formattedCall, conversationView);
            _subagentById[subIdValue] = subState;
        }

        switch (evt.Inner)
        {
            case ResponseChunk { Text: var text } when !string.IsNullOrEmpty(text):
                subState.AppendText(text);
                break;

            case ToolCallStarted subStarted:
                subState.AddToolCall(
                    subStarted.CallId,
                    subStarted.FormatCall(),
                    suppressCompletionResult: subStarted.ToolName == TodoToolName);
                break;

            case ToolAwaitingApproval { CallId: var subAwaitingId }:
                subState.SetToolAwaitingApproval(subAwaitingId);
                break;

            case ToolCallCompleted subCompleted:
                subState.SetToolCompleted(subCompleted.CallId, subCompleted.IsError, subCompleted.Result);
                break;

            case TurnCompleted:
                subState.SetCompleted();
                break;

            case TurnError { Message: var subErrMsg }:
                subState.AddError(subErrMsg);
                subState.SetCompleted();
                break;
        }
    }

    /// <summary>
    /// Clears turn-local state after a cancelled or interrupted turn.
    /// </summary>
    public void ResetTurnState()
    {
        _currentText = null;
    }

    /// <summary>
    /// Appends text to the last segment of an entry, or creates one if needed.
    /// </summary>
    private static void AppendToLastSegment(ConversationEntry entry, string text, Color fg)
    {
        if (entry.Segments.Count == 0)
        {
            entry.AppendSegment(text, fg);
            return;
        }

        var last = entry.Segments[^1];
        entry.Segments[^1] = last with { Text = last.Text + text };
        entry.NotifyChanged();
    }

    // ---- Tool call state tracking ----

    private enum ToolLifecycle { Started, AwaitingApproval, Completed }

    /// <summary>
    /// Tracks the lifecycle state of a single tool call and its display entry.
    /// The CircleColor property is evaluated on every render pass so the circle
    /// updates in-place as the tool transitions through its lifecycle.
    /// </summary>
    private sealed class ToolEntryState
    {
        public ToolLifecycle State { get; set; } = ToolLifecycle.Started;
        private bool _isError;

        /// <summary>The entry added to the ConversationView for this tool call.</summary>
        public ConversationEntry? DisplayEntry { get; set; }
        public bool SuppressCompletionResult { get; init; }

        public Color CircleColor => State switch
        {
            ToolLifecycle.Started => new Color(ColorName16.Yellow),
            ToolLifecycle.AwaitingApproval => new Color(ColorName16.Yellow),
            ToolLifecycle.Completed => _isError
                ? new Color(ColorName16.Red)
                : new Color(ColorName16.Green),
            _ => new Color(ColorName16.DarkGray)
        };

        public void SetCompleted(bool isError, string? result)
        {
            State = ToolLifecycle.Completed;
            _isError = isError;

            if (DisplayEntry is null) return;

            if (SuppressCompletionResult && !isError)
            {
                DisplayEntry.NotifyChanged();
                return;
            }

            // Add result lines below the signature.
            if (!string.IsNullOrWhiteSpace(result))
            {
                var lines = result.Split('\n');
                var visibleCount = Math.Min(lines.Length, MaxResultLines);

                for (var i = 0; i < visibleCount; i++)
                {
                    var prefix = i == 0 ? "└─ " : "   ";
                    DisplayEntry.AppendSegment(
                        $"\n{prefix}{lines[i]}",
                        new Color(ColorName16.DarkGray));
                }

                if (lines.Length > MaxResultLines)
                {
                    DisplayEntry.AppendSegment(
                        $"\n   ({lines.Length - MaxResultLines} more lines)",
                        new Color(ColorName16.DarkGray));
                }
            }

            DisplayEntry.NotifyChanged();
        }
    }

    // ---- Subagent state tracking ----

    /// <summary>
    /// Tracks all state for a single subagent run: its display entry, inner text
    /// streaming, and nested tool calls.
    /// </summary>
    private sealed class SubagentState
    {
        private readonly ConversationEntry _entry;
        private ConversationEntry? _currentText;
        private readonly Dictionary<string, ToolEntryState> _toolCalls = new();
        private bool _completed;

        public SubagentState(string subagentId, string formattedCall, ConversationView view)
        {
            _ = subagentId; // Kept for diagnostics

            _entry = new ConversationEntry(EntryStyle.Circle, () => CircleColor);
            _entry.MaxChildRows = MaxSubagentInnerRows;
            _entry.AppendSegment(formattedCall, new Color(ColorName16.DarkGray));
            view.AddEntry(_entry);
        }

        private Color CircleColor => _completed
            ? new Color(ColorName16.Green)
            : new Color(ColorName16.Yellow);

        public void AppendText(string text)
        {
            if (_currentText is null)
            {
                _currentText = new ConversationEntry(EntryStyle.Circle,
                    () => new Color(ColorName16.White));
                _currentText.AppendSegment("", new Color(ColorName16.White));
                _entry.AddChild(_currentText);
            }
            AppendToLastSegment(_currentText, text, new Color(ColorName16.White));
        }

        public void AddToolCall(string callId, string formattedCall, bool suppressCompletionResult = false)
        {
            var toolState = new ToolEntryState
            {
                SuppressCompletionResult = suppressCompletionResult
            };
            _toolCalls[callId] = toolState;

            var capturedState = toolState;
            var toolEntry = new ConversationEntry(EntryStyle.Circle,
                () => capturedState.CircleColor);
            toolEntry.AppendSegment(formattedCall, new Color(ColorName16.DarkGray));
            toolState.DisplayEntry = toolEntry;

            _entry.AddChild(toolEntry);
            _currentText = null;
        }

        public void SetToolAwaitingApproval(string callId)
        {
            if (_toolCalls.TryGetValue(callId, out var state))
            {
                state.State = ToolLifecycle.AwaitingApproval;
                state.DisplayEntry?.NotifyChanged();
            }
        }

        public void SetToolCompleted(string callId, bool isError, string? result)
        {
            if (_toolCalls.TryGetValue(callId, out var state))
            {
                state.SetCompleted(isError, result);
                _toolCalls.Remove(callId);
            }
        }

        public void AddError(string message)
        {
            var errEntry = new ConversationEntry(EntryStyle.Circle,
                () => new Color(ColorName16.Red));
            errEntry.AppendSegment($"[error] {message}", new Color(ColorName16.Red));
            _entry.AddChild(errEntry);
            _currentText = null;
        }

        public void SetCompleted()
        {
            if (_completed) return;
            _completed = true;
            _currentText = null;
            _entry.NotifyChanged();
        }
    }
}
