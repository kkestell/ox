using System.Diagnostics;
using System.Text;
using dotenv.net;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Permissions;
using Ur.Tui.Rendering;

namespace Ur.Tui;

/// <summary>
/// A full-screen TUI for Ur built around a retained-mode rendering model.
///
/// Architecture (bottom to top):
///   Terminal   — raw ANSI escape operations (cursor, alternate buffer, etc.)
///   Viewport   — display engine; owns EventList, redraws at ~30 fps when dirty
///   Renderables — live objects (TextRenderable, ToolRenderable, etc.) whose
///                  content can change between redraws
///   EventRouter — maps AgentLoopEvents to renderables; encapsulates routing state
///   Program     — REPL loop, input reading, signal handlers, orchestration
///
/// The main agent loop runs in the "await foreach" body. Subagent events arrive
/// via TurnCallbacks.SubagentEventEmitted and are routed through the same router.
/// </summary>
internal static class Program
{
    // Pauses the escape key monitor while the permission callback is reading
    // from the keyboard. Without this, the escape monitor and the permission
    // reader would race over Console.ReadKey — one would silently eat keystrokes.
    private static volatile bool _pauseKeyMonitor;

    private static async Task<int> Main(string[] _)
    {
        // --- Boot ---
        DotEnv.Load(options: new DotEnvOptions(
            probeForEnv: true,
            probeLevelsToSearch: 8));

        // App-level CTS wired to Ctrl+C. Per-turn CTSs link to this so that
        // Ctrl+C cancels both the running turn and the outer REPL loop.
        var appCts = new CancellationTokenSource();

        // Build the rendering stack before registering signal handlers so
        // cleanup always has a viewport reference to call Stop() on.
        var eventList = new EventList();
        var viewport   = new Viewport(eventList);

        // Restore the terminal on both Ctrl+C and normal process exit.
        // We register both because Ctrl+C triggers CancelKeyPress AND ProcessExit
        // on macOS/Unix, while unhandled exceptions trigger only ProcessExit.
        // viewport.Stop() is idempotent, so double-calling is safe.
        Console.CancelKeyPress += (_, e) =>
        {
            e.Cancel = true;   // Prevent immediate termination; let the REPL loop exit.
            viewport.Stop();
            appCts.Cancel();
        };
        AppDomain.CurrentDomain.ProcessExit += (_, _) => viewport.Stop();

        var host = await UrHost.StartAsync(Environment.CurrentDirectory, ct: appCts.Token);

        // --- Configuration check (pre-viewport, plain console I/O) ---
        if (!await EnsureReadyAsync(host, appCts.Token))
            return 1;

        // --- REPL ---
        var router    = new EventRouter(eventList);
        var callbacks = BuildCallbacks(router, viewport);
        var session   = host.CreateSession(callbacks);

        viewport.Start();

        // Show a welcome message as the first item in the conversation list.
        var welcome = new TextRenderable();
        welcome.SetText($"Session: {session.Id}  (type a message · Esc = cancel turn · Ctrl+C = exit)");
        eventList.Add(welcome, BubbleStyle.System);

        while (!appCts.Token.IsCancellationRequested)
        {
            var input = ReadLineInViewport("❯ ", viewport, appCts.Token);

            // null = EOF (Ctrl+D) or cancellation.
            if (input is null)
                break;

            if (string.IsNullOrWhiteSpace(input))
                continue;

            // Show the user's message in the conversation with white text so it
            // stands out clearly against the black bubble background.
            var userMsg = new TextRenderable(foreground: Color.White);
            userMsg.SetText(input);
            eventList.Add(userMsg);

            // Switch input row to a running indicator; start the Escape monitor.
            viewport.SetInputPrompt("[running... Esc to cancel]");

            // Per-turn CTS linked to app token so Ctrl+C also cancels mid-turn.
            // ReSharper disable once AccessToDisposedClosure — monitor awaited before disposal.
            var turnCts = CancellationTokenSource.CreateLinkedTokenSource(appCts.Token);
            var keyMonitor = MonitorEscapeKeyAsync(turnCts);

            try
            {
                try
                {
                    await foreach (var evt in session.RunTurnAsync(input, turnCts.Token))
                    {
                        router.RouteMainEvent(evt);

                        // Fatal errors are unrecoverable — exit the process.
                        if (evt is Error { IsFatal: true })
                            return 1;
                    }
                }
                catch (OperationCanceledException) when (!appCts.Token.IsCancellationRequested)
                {
                    // Escape cancelled this turn; add a visual marker and reset state.
                    var cancelled = new TextRenderable();
                    cancelled.SetText("[cancelled]");
                    eventList.Add(cancelled);
                    router.ResetTurnState();
                }
                catch (OperationCanceledException) when (appCts.Token.IsCancellationRequested)
                {
                    // Ctrl+C during a turn — fall through; outer loop exits.
                }

                await turnCts.CancelAsync();
                await keyMonitor;
            }
            finally
            {
                turnCts.Dispose();
            }

            viewport.SetInputPrompt("❯ ");
        }

        viewport.Stop();
        return 0;
    }

    /// <summary>
    /// Checks <see cref="UrConfiguration.Readiness"/> and prompts the user to
    /// supply any missing values. Runs before the viewport starts, so it uses
    /// direct console I/O rather than the viewport's input area.
    /// </summary>
    private static async Task<bool> EnsureReadyAsync(UrHost host, CancellationToken ct)
    {
        while (true)
        {
            var readiness = host.Configuration.Readiness;
            if (readiness.CanRunTurns)
                return true;

            foreach (var issue in readiness.BlockingIssues)
            {
                switch (issue)
                {
                    case ChatBlockingIssue.MissingApiKey:
                        Console.Write("No API key configured. Enter your OpenRouter API key (or blank to exit): ");
                        var key = CancellableReadLine(ct)?.Trim();
                        if (string.IsNullOrEmpty(key))
                            return false;
                        await host.Configuration.SetApiKeyAsync(key, ct);
                        break;

                    case ChatBlockingIssue.MissingModelSelection:
                        Console.Write("No model selected. Enter a model ID (or blank to exit): ");
                        var model = CancellableReadLine(ct)?.Trim();
                        if (string.IsNullOrEmpty(model))
                            return false;
                        await host.Configuration.SetSelectedModelAsync(model, ct: ct);
                        break;

                    default:
                        throw new UnreachableException($"Unexpected {nameof(ChatBlockingIssue)}: {issue}");
                }
            }
        }
    }

    /// <summary>
    /// Builds <see cref="TurnCallbacks"/> that route events and permission requests
    /// through the rendering layer instead of writing directly to the console.
    /// </summary>
    private static TurnCallbacks BuildCallbacks(EventRouter router, Viewport viewport)
    {
        return new TurnCallbacks
        {
            // Relay subagent events to the router, which finds or creates the right
            // SubagentRenderable and routes the inner event to it.
            SubagentEventEmitted = evt =>
            {
                if (evt is SubagentEvent subagentEvt)
                    router.RouteSubagentEvent(subagentEvt);
                return ValueTask.CompletedTask;
            },

            RequestPermissionAsync = (req, ct) =>
            {
                // Transition the in-flight tool to the AwaitingApproval visual state
                // so the user sees which tool is requesting permission.
                router.SetLastToolAwaitingApproval();

                var scopeHints = req.AllowedScopes.Count > 1
                    ? $" [{string.Join(", ", req.AllowedScopes.Select(s => s.ToString().ToLowerInvariant()))}]"
                    : "";

                var promptText =
                    $"Allow {req.OperationType} on '{req.Target}' by '{req.RequestingExtension}'?"
                    + $" (y/n{scopeHints}): ";

                // Pause the Escape key monitor while reading. Without this, the monitor
                // would race with ReadLineInViewport over Console.ReadKey and eat input.
                _pauseKeyMonitor = true;
                string? input;
                try
                {
                    input = ReadLineInViewport(promptText, viewport, ct);
                }
                finally
                {
                    _pauseKeyMonitor = false;
                }

                input = input?.Trim().ToLowerInvariant();

                var candidate = input switch
                {
                    "y" or "yes" => new PermissionResponse(true, PermissionScope.Once),
                    "session"    => new PermissionResponse(true, PermissionScope.Session),
                    "workspace"  => new PermissionResponse(true, PermissionScope.Workspace),
                    "always"     => new PermissionResponse(true, PermissionScope.Always),
                    _            => new PermissionResponse(false, null)
                };

                // If the user chose a scope the operation does not support, deny rather
                // than silently granting more than permitted.
                var response = candidate is { Granted: true, Scope: not null }
                    && !req.AllowedScopes.Contains(candidate.Scope.Value)
                    ? new PermissionResponse(false, null)
                    : candidate;

                // Restore the running indicator after permission is resolved.
                viewport.SetInputPrompt("[running... Esc to cancel]");

                return ValueTask.FromResult(response);
            }
        };
    }

    /// <summary>
    /// Reads a line of user input through the viewport's input row.
    /// Characters typed by the user are reflected in the input row in real time
    /// via <see cref="Viewport.SetInputPrompt"/>. Returns the typed string on Enter,
    /// or null on EOF (Ctrl+D on empty buffer) or cancellation.
    /// </summary>
    private static string? ReadLineInViewport(string promptPrefix, Viewport viewport, CancellationToken ct)
    {
        var buffer = new StringBuilder();
        viewport.SetInputPrompt(promptPrefix);

        while (!ct.IsCancellationRequested)
        {
            if (!Console.KeyAvailable)
            {
                Thread.Sleep(20);
                continue;
            }

            var key = Console.ReadKey(intercept: true);

            if (key.Key == ConsoleKey.Enter)
                return buffer.ToString();

            if (key.Key == ConsoleKey.Backspace)
            {
                if (buffer.Length > 0)
                    buffer.Remove(buffer.Length - 1, 1);
            }
            else if (key.Key == ConsoleKey.D
                     && key.Modifiers.HasFlag(ConsoleModifiers.Control)
                     && buffer.Length == 0)
            {
                return null; // EOF
            }
            else if (!char.IsControl(key.KeyChar))
            {
                buffer.Append(key.KeyChar);
            }

            // Update the viewport so the user sees what they have typed so far.
            viewport.SetInputPrompt(promptPrefix + buffer);
        }

        return null; // Cancellation
    }

    /// <summary>
    /// Polls for Escape key presses in the background and cancels
    /// <paramref name="turnCts"/> when detected. Pauses while
    /// <see cref="_pauseKeyMonitor"/> is true (during permission prompts)
    /// to avoid stealing keystrokes from <see cref="ReadLineInViewport"/>.
    /// </summary>
    private static Task MonitorEscapeKeyAsync(CancellationTokenSource turnCts)
    {
        return Task.Run(async () =>
        {
            while (!turnCts.Token.IsCancellationRequested)
            {
                await Task.Delay(50, CancellationToken.None);

                if (_pauseKeyMonitor)
                    continue;

                if (!Console.KeyAvailable)
                    continue;

                var key = Console.ReadKey(intercept: true);
                if (key.Key != ConsoleKey.Escape)
                    continue;

                await turnCts.CancelAsync();
                return;
            }
        });
    }

    /// <summary>
    /// Reads a line from stdin with cancellation support. Used only during
    /// the pre-viewport configuration phase (<see cref="EnsureReadyAsync"/>).
    /// Polls <see cref="Console.KeyAvailable"/> rather than blocking so it
    /// remains responsive to cancellation. Characters are echoed inline since
    /// the viewport is not yet active.
    /// </summary>
    private static string? CancellableReadLine(CancellationToken ct)
    {
        var buffer = new StringBuilder();
        while (!ct.IsCancellationRequested)
        {
            if (!Console.KeyAvailable)
            {
                Thread.Sleep(50);
                continue;
            }

            var key = Console.ReadKey(intercept: true);

            if (key.Key == ConsoleKey.Enter)
            {
                Console.WriteLine();
                return buffer.ToString();
            }

            if (key.Key == ConsoleKey.Backspace)
            {
                if (buffer.Length > 0)
                {
                    buffer.Remove(buffer.Length - 1, 1);
                    Console.Write("\b \b");
                }
                continue;
            }

            if (key.Key == ConsoleKey.D
                && key.Modifiers.HasFlag(ConsoleModifiers.Control)
                && buffer.Length == 0)
            {
                return null; // EOF
            }

            if (char.IsControl(key.KeyChar))
                continue;

            buffer.Append(key.KeyChar);
            Console.Write(key.KeyChar);
        }

        Console.WriteLine();
        return null;
    }

    // -------------------------------------------------------------------------
    // Event routing
    // -------------------------------------------------------------------------

    /// <summary>
    /// Routes <see cref="AgentLoopEvent"/>s to the appropriate renderables,
    /// maintaining all the state needed to correlate started/completed pairs
    /// and to lazily create renderables as events arrive.
    ///
    /// The router is the only layer that knows about tool call IDs and subagent
    /// IDs — everything above (the REPL loop) and below (the renderables) is
    /// ignorant of these identifiers.
    /// </summary>
    private sealed class EventRouter(EventList eventList)
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

        // Maps run_subagent CallId to the SubagentId assigned by SubagentRunner.
        // Populated in RouteSubagentEvent when the first event for a new SubagentId
        // arrives. The SubagentId is not known at ToolCallStarted time — it's only
        // available from SubagentEvent.SubagentId. Used by ToolCallCompleted to
        // defensively finalize the SubagentRenderable if it wasn't finalized via
        // a SubagentEvent { Inner: TurnCompleted } callback (e.g. on error paths).
        private readonly Queue<(string CallId, string? SubagentId)> _pendingSubagentCalls = new();
        private readonly Dictionary<string, string> _callIdToSubagentId = new();

        // ---- Subagent state (keyed by SubagentId, the 8-char hex from SubagentRunner) ----

        // Maps SubagentId to SubagentRenderable. Created lazily on first SubagentEvent.
        private readonly Dictionary<string, SubagentRenderable> _subagentById = new();

        // Per-subagent current streaming text block.
        private readonly Dictionary<string, TextRenderable?> _subagentCurrentText = new();

        // Per-subagent tool call maps for SetCompleted lookups.
        private readonly Dictionary<string, Dictionary<string, ToolRenderable>> _subagentToolCalls = new();

        // ---- Permission callback support ----

        // The most recently started ToolRenderable in any context. Because tool calls
        // are sequential (the next tool starts after the previous completes), this is
        // always the tool currently waiting for permission when the callback fires.
        private ToolRenderable? _lastStartedTool;

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
                        eventList.Add(_currentText, BubbleStyle.Assistant);
                    }
                    _currentText.Append(text);
                    break;

                case ToolCallStarted started when started.ToolName == SubagentToolName:
                    // run_subagent: mark this CallId as a subagent call so that
                    // ToolCallCompleted knows not to look for a ToolRenderable.
                    // The SubagentRenderable is created lazily in RouteSubagentEvent
                    // when the first SubagentEvent with the subagent's ID arrives,
                    // because SubagentId is not known until that first event.
                    _subagentCallIds.Add(started.CallId);
                    _pendingSubagentCalls.Enqueue((started.CallId, null));
                    _currentText = null;
                    break;

                case ToolCallStarted started:
                    var tool = new ToolRenderable(started);
                    _toolCallMap[started.CallId] = tool;
                    _lastStartedTool = tool;
                    eventList.Add(tool, BubbleStyle.System);
                    _currentText = null;
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
                        if (ReferenceEquals(_lastStartedTool, completedTool))
                            _lastStartedTool = null;
                    }
                    break;

                case TurnCompleted:
                    _currentText = null;
                    break;

                case Error { Message: var msg }:
                    var errorText = new TextRenderable(foreground: Color.Red);
                    errorText.SetText($"[error] {msg}");
                    eventList.Add(errorText, BubbleStyle.System);
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
            // so we defer creation until here. Associate this SubagentId with the oldest
            // pending run_subagent CallId so ToolCallCompleted can defensively finalize.
            if (!_subagentById.TryGetValue(subId, out var subRenderable))
            {
                subRenderable = new SubagentRenderable(subId);
                _subagentById[subId] = subRenderable;
                _subagentCurrentText[subId] = null;
                _subagentToolCalls[subId] = new Dictionary<string, ToolRenderable>();
                // BubbleStyle.None: SubagentRenderable provides its own bordered frame;
                // applying outer bubble chrome would double-indent the inner content.
                eventList.Add(subRenderable, BubbleStyle.None);

                // Pair this SubagentId with the oldest unclaimed run_subagent CallId.
                // With sequential execution there is always exactly one pending call.
                if (_pendingSubagentCalls.TryDequeue(out var pending))
                    _callIdToSubagentId[pending.CallId] = subId;
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
                        subRenderable.AddChild(subText, BubbleStyle.Assistant);
                    }
                    subText.Append(text);
                    break;

                case ToolCallStarted subStarted:
                    var subTool = new ToolRenderable(subStarted);
                    _subagentToolCalls[subId][subStarted.CallId] = subTool;
                    _lastStartedTool = subTool;
                    subRenderable.AddChild(subTool, BubbleStyle.System);
                    _subagentCurrentText[subId] = null;
                    break;

                case ToolCallCompleted subCompleted:
                    if (_subagentToolCalls[subId].TryGetValue(subCompleted.CallId, out var subCompletedTool))
                    {
                        subCompletedTool.SetCompleted(subCompleted.IsError);
                        _subagentToolCalls[subId].Remove(subCompleted.CallId);
                        if (ReferenceEquals(_lastStartedTool, subCompletedTool))
                            _lastStartedTool = null;
                    }
                    break;

                case TurnCompleted:
                    subRenderable.SetCompleted();
                    _subagentCurrentText[subId] = null;
                    break;

                case Error { Message: var subErrMsg }:
                    var subErrText = new TextRenderable(foreground: Color.Red);
                    subErrText.SetText($"[error] {subErrMsg}");
                    subRenderable.AddChild(subErrText, BubbleStyle.System);
                    subRenderable.SetCompleted();
                    _subagentCurrentText[subId] = null;
                    break;
            }
        }

        /// <summary>
        /// Transitions the last-started tool to the AwaitingApproval state.
        /// Called by <see cref="TurnCallbacks.RequestPermissionAsync"/> before
        /// reading user input.
        /// </summary>
        public void SetLastToolAwaitingApproval() => _lastStartedTool?.SetAwaitingApproval();

        /// <summary>
        /// Clears turn-local state after a cancelled or interrupted turn so the
        /// next turn starts fresh. Existing renderables remain in the EventList
        /// (they stay visible as history), but new events will not be appended to
        /// stale text blocks.
        /// </summary>
        public void ResetTurnState()
        {
            _currentText = null;
            _lastStartedTool = null;
        }
    }
}
