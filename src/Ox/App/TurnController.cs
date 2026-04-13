using System.Collections.Concurrent;
using Ox.Agent.AgentLoop;
using Ox.Agent.Sessions;

namespace Ox.App;

/// <summary>
/// Owns the lifecycle of a single agent turn for the TUI.
///
/// Before this existed, <see cref="OxApp"/> held <c>_eventQueue</c>,
/// <c>_turnCts</c>, <c>_turnActive</c>, <c>_queuedInput</c>, the fire-and-forget
/// <c>Task.Run</c>, and the wake-semaphore all in the same class as input
/// handling and rendering. All of that is turn-lifecycle concerns — bundling
/// them here makes <c>OxApp</c> a coordinator instead of a mix of roles.
///
/// Threading model: the background task enqueues <see cref="AgentLoopEvent"/>
/// values and invokes <paramref name="wakeMainLoop"/> so the UI loop wakes up
/// and drains. This class never touches the semaphore type directly — the
/// wake action is the only coupling back to the main loop.
/// </summary>
internal sealed class TurnController(Action wakeMainLoop) : IDisposable
{
    private readonly ConcurrentQueue<AgentLoopEvent> _events = new();
    private CancellationTokenSource? _cts;
    private string? _queuedInput;

    /// <summary>Event queue drained by the main loop's <c>AgentEventApplier</c>.</summary>
    public ConcurrentQueue<AgentLoopEvent> Events => _events;

    /// <summary>Whether a turn is currently running.</summary>
    public bool IsActive { get; private set; }

    /// <summary>
    /// Fires a new turn on a background task. Events from
    /// <see cref="OxSession.RunTurnAsync"/> are enqueued on <see cref="Events"/>
    /// and the main loop is woken after each one so rendering can stay responsive.
    /// </summary>
    public void Start(OxSession session, string input)
    {
        IsActive = true;
        _cts = new CancellationTokenSource();
        var ct = _cts.Token;

        // Fire-and-forget: we intentionally don't await the task — its events
        // drive the UI via the queue. Exceptions become synthetic TurnErrors
        // so the main loop handles them through the same path as normal errors.
        _ = Task.Run(async () =>
        {
            try
            {
                await foreach (var evt in session.RunTurnAsync(input, ct))
                {
                    _events.Enqueue(evt);
                    wakeMainLoop();
                }
            }
            catch (OperationCanceledException)
            {
                // Cancellation is expected — the main loop handles the UI state
                // via CancelTurn / TurnError depending on how we got here.
            }
            catch (Exception ex)
            {
                _events.Enqueue(new TurnError
                {
                    Message = ex.Message,
                    IsFatal = true,
                });
                wakeMainLoop();
            }
        });
    }

    /// <summary>
    /// Cancels the in-flight turn (if any). The background task observes the
    /// cancellation via its token and exits without enqueueing further events;
    /// the main loop is responsible for updating visible UI (throbber reset, etc.).
    /// </summary>
    public void Cancel()
    {
        _cts?.Cancel();
        IsActive = false;
    }

    /// <summary>
    /// Marks the current turn as finished from the main loop's perspective.
    /// Called when the applier sees <c>TurnCompleted</c> or a fatal
    /// <c>TurnError</c>; separate from <see cref="Cancel"/> because it does not
    /// request cancellation — the turn already finished on its own.
    /// </summary>
    public void MarkEnded() => IsActive = false;

    /// <summary>
    /// Captures input submitted while a turn is active so it can be fired as
    /// the next turn when this one completes. Only the most recent queued
    /// input is kept — intentional, because a long turn can generate many
    /// interstitial prompts and only the latest one represents current intent.
    /// </summary>
    public void QueueWhileActive(string input) => _queuedInput = input;

    /// <summary>
    /// Returns and clears any input queued during the previous turn, or null
    /// if no input was queued. Called by the main loop after a turn ends.
    /// </summary>
    public string? TakeQueuedInput()
    {
        var queued = _queuedInput;
        _queuedInput = null;
        return queued;
    }

    public void Dispose() => _cts?.Dispose();
}
