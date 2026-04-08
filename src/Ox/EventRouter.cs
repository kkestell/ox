using Te.Rendering;
using Ur.AgentLoop;
using Ox.Rendering;

namespace Ox;

/// <summary>
/// Routes <see cref="AgentLoopEvent"/>s to the appropriate renderables,
/// maintaining all the state needed to correlate started/completed pairs
/// and to lazily create renderables as events arrive.
///
/// The router is the only layer that knows about tool call IDs and subagent
/// IDs — everything above (the REPL loop) and below (the renderables) is
/// ignorant of these identifiers.
/// </summary>
internal sealed class EventRouter(EventList eventList)
{
    // Tool name for the run_subagent tool. Used to differentiate subagent
    // tool calls (which get a SubagentRenderable) from regular tool calls
    // (which get a ToolRenderable). Defined here to avoid a project reference
    // to Ur.Tools from within the routing logic.
    private const string SubagentToolName = "run_subagent";

    // ---- Main-stream state ----

    // Active streaming text block for the main agent. Null between turns.
    // Reset when a ToolCallStarted or TurnCompleted arrives so the next
    // ResponseChunk creates a fresh renderable.
    private TextRenderable? _currentText;

    // Maps CallId to ToolRenderable for in-flight main-stream tool calls.
    private readonly Dictionary<string, ToolRenderable> _toolCallMap = new();

    // CallIds of run_subagent invocations — these map to SubagentRenderables,
    // not ToolRenderables, so ToolCallCompleted must treat them differently.
    private readonly HashSet<string> _subagentCallIds = [];

    // Queues run_subagent calls with their formatted signatures. The SubagentId
    // is not known at ToolCallStarted time — it arrives later via SubagentEvent.
    // When RouteSubagentEvent creates a SubagentRenderable, it dequeues the oldest
    // pending call to get the formatted signature and to pair CallId → SubagentId
    // for defensive finalization in ToolCallCompleted.
    private readonly Queue<(string CallId, string FormattedCall)> _pendingSubagentCalls = new();
    private readonly Dictionary<string, string> _callIdToSubagentId = new();

    // ---- Subagent state (keyed by SubagentId, the 8-char hex from SubagentRunner) ----

    // Maps SubagentId to SubagentRenderable. Created lazily on first SubagentEvent.
    private readonly Dictionary<string, SubagentRenderable> _subagentById = new();

    // Per-subagent current streaming text block.
    private readonly Dictionary<string, TextRenderable?> _subagentCurrentText = new();

    // Per-subagent tool call maps for SetCompleted lookups.
    private readonly Dictionary<string, Dictionary<string, ToolRenderable>> _subagentToolCalls = new();

    // ---- Permission callback support (removed) ----
    // The old _lastStartedTool field assumed sequential execution. It has been
    // replaced by the ToolAwaitingApproval event, which carries the CallId of the
    // specific tool awaiting approval — no ambient "last started" tracking needed.

    /// <summary>
    /// Routes a main-stream event. Called for every event yielded by
    /// <c>session.RunTurnAsync</c>.
    /// </summary>
    public void RouteMainEvent(AgentLoopEvent evt)
    {
        switch (evt)
        {
            case ResponseChunk { Text: var text } when !string.IsNullOrEmpty(text):
                // Lazy creation: a new TextRenderable begins each run of response
                // text (after startup, after each tool call completes).
                // Guard on non-empty text so that empty chunks (which the model
                // sometimes emits before a tool call) don't produce empty bubbles.
                if (_currentText is null)
                {
                    _currentText = new TextRenderable();
                    eventList.Add(_currentText, BubbleStyle.Circle, () => Color.White);
                }
                _currentText.Append(text);
                break;

            case ToolCallStarted { ToolName: SubagentToolName } started:
                // run_subagent: mark this CallId as a subagent call so that
                // ToolCallCompleted knows not to look for a ToolRenderable.
                // The SubagentRenderable is created lazily in RouteSubagentEvent
                // when the first SubagentEvent with the subagent's ID arrives,
                // because SubagentId is not known until that first event.
                // Store the formatted call string now so it's available for the
                // SubagentRenderable's tool signature row when it's created.
                _subagentCallIds.Add(started.CallId);
                _pendingSubagentCalls.Enqueue((started.CallId, started.FormatCall()));
                _currentText = null;
                break;

            case ToolCallStarted started:
                var tool = new ToolRenderable(started.FormatCall());
                _toolCallMap[started.CallId] = tool;
                eventList.Add(tool, BubbleStyle.Circle, () => tool.CircleColor);
                _currentText = null;
                break;

            // Parallel dispatch emits this right before prompting the user for
            // permission. We look up the specific tool by CallId instead of relying
            // on "the last started tool" — correct even when multiple tools are in flight.
            case ToolAwaitingApproval { CallId: var awaitingCallId }:
                if (_toolCallMap.TryGetValue(awaitingCallId, out var awaitingTool))
                    awaitingTool.SetAwaitingApproval();
                break;

            case ToolCallCompleted completed when _subagentCallIds.Contains(completed.CallId):
                // Primary finalization happens via SubagentEvent { Inner: TurnCompleted }
                // (which arrives before ToolCallCompleted in the normal flow). This is a
                // defensive fallback: if event ordering changes or the subagent errors
                // before emitting TurnCompleted, we ensure the block is closed.
                if (_callIdToSubagentId.TryGetValue(completed.CallId, out var subagentId)
                    && _subagentById.TryGetValue(subagentId, out var subRendForCallId))
                {
                    subRendForCallId.SetCompleted(); // idempotent — no-op if already finalized
                    _callIdToSubagentId.Remove(completed.CallId);
                }
                _subagentCallIds.Remove(completed.CallId);
                break;

            case ToolCallCompleted completed:
                if (_toolCallMap.TryGetValue(completed.CallId, out var completedTool))
                {
                    completedTool.SetCompleted(completed.IsError);
                    _toolCallMap.Remove(completed.CallId);
                }
                break;

            case TurnCompleted:
                _currentText = null;
                break;

            case TurnError { Message: var msg }:
                var errorText = new TextRenderable(foreground: Color.Red);
                errorText.SetText($"[error] {msg}");
                eventList.Add(errorText, BubbleStyle.Circle, () => Color.Red);
                _currentText = null;
                break;
        }
    }

    /// <summary>
    /// Routes a <see cref="SubagentEvent"/> received via
    /// <c>TurnCallbacks.SubagentEventEmitted</c>. Creates a
    /// <see cref="SubagentRenderable"/> the first time a new SubagentId is seen.
    /// </summary>
    public void RouteSubagentEvent(SubagentEvent evt)
    {
        var subId = evt.SubagentId;

        // Lazily create the SubagentRenderable the first time an event arrives
        // for this subagent run. All subsequent events use the same renderable.
        // The SubagentId (from SubagentRunner) is not known at ToolCallStarted time,
        // so we defer creation until here. Dequeue the oldest pending call to get
        // the formatted signature and pair CallId → SubagentId for finalization.
        if (!_subagentById.TryGetValue(subId, out var subRenderable))
        {
            // Dequeue to get the formatted call string stored at ToolCallStarted time.
            var formattedCall = "";
            if (_pendingSubagentCalls.TryDequeue(out var pending))
            {
                _callIdToSubagentId[pending.CallId] = subId;
                formattedCall = pending.FormattedCall;
            }

            subRenderable = new SubagentRenderable(subId, formattedCall);
            _subagentById[subId] = subRenderable;
            _subagentCurrentText[subId] = null;
            _subagentToolCalls[subId] = new Dictionary<string, ToolRenderable>();

            // Circle child in the outer tree. The circle color tracks the subagent's
            // lifecycle: yellow while running, green on completion.
            var capturedSub = subRenderable;
            eventList.Add(subRenderable, BubbleStyle.Circle, () => capturedSub.CircleColor);
        }

        switch (evt.Inner)
        {
            case ResponseChunk { Text: var text } when !string.IsNullOrEmpty(text):
                // Same empty-chunk guard as RouteMainEvent — skip creation until
                // there is actual text to display.
                if (!_subagentCurrentText.TryGetValue(subId, out var subText) || subText is null)
                {
                    subText = new TextRenderable();
                    _subagentCurrentText[subId] = subText;
                    subRenderable.AddChild(subText, BubbleStyle.Circle, () => Color.White);
                }
                subText.Append(text);
                break;

            case ToolCallStarted subStarted:
                var subTool = new ToolRenderable(subStarted.FormatCall());
                _subagentToolCalls[subId][subStarted.CallId] = subTool;
                subRenderable.AddChild(subTool, BubbleStyle.Circle, () => subTool.CircleColor);
                _subagentCurrentText[subId] = null;
                break;

            // Subagent tools can also require approval (they share the parent's
            // callbacks). Look up by CallId within this subagent's tool map.
            case ToolAwaitingApproval { CallId: var subAwaitingId }:
                if (_subagentToolCalls[subId].TryGetValue(subAwaitingId, out var subAwaitingTool))
                    subAwaitingTool.SetAwaitingApproval();
                break;

            case ToolCallCompleted subCompleted:
                if (_subagentToolCalls[subId].TryGetValue(subCompleted.CallId, out var subCompletedTool))
                {
                    subCompletedTool.SetCompleted(subCompleted.IsError);
                    _subagentToolCalls[subId].Remove(subCompleted.CallId);
                }
                break;

            case TurnCompleted:
                subRenderable.SetCompleted();
                _subagentCurrentText[subId] = null;
                break;

            case TurnError { Message: var subErrMsg }:
                var subErrText = new TextRenderable(foreground: Color.Red);
                subErrText.SetText($"[error] {subErrMsg}");
                subRenderable.AddChild(subErrText, BubbleStyle.Circle, () => Color.Red);
                subRenderable.SetCompleted();
                _subagentCurrentText[subId] = null;
                break;
        }
    }

    /// <summary>
    /// Clears turn-local state after a cancelled or interrupted turn so the
    /// next turn starts fresh. Existing renderables remain in the EventList
    /// (they stay visible as history), but new events will not be appended to
    /// stale text blocks.
    /// </summary>
    public void ResetTurnState()
    {
        _currentText = null;
    }
}
