using System.Collections.Concurrent;
using Ox.Conversation;
using Ox.Input;
using Ox.Permission;
using Ox.Views;
using Te.Input;
using Ur.Hosting;
using Te.Rendering;
using Ur.AgentLoop;
using Ur.Permissions;

namespace Ox;

/// <summary>
/// Main application loop for the Ox TUI.
///
/// Runs a single-threaded render loop that wakes on three signals: stdin
/// input (via Te's InputCoordinator), Ur agent events (via ConcurrentQueue),
/// and a periodic tick (for the throbber animation). All state mutation
/// happens on the main thread when queues are drained — background threads
/// only enqueue.
///
/// The permission prompt bridge uses a TaskCompletionSource to block the Ur
/// turn task until the user responds, then completes the TCS from the main
/// thread.
/// </summary>
public sealed class OxApp : IDisposable
{
    private readonly InputCoordinator _coordinator;
    private readonly ConsoleBuffer _buffer;
    private readonly ConversationView _conversationView = new();
    private readonly InputAreaView _inputAreaView = new();
    private readonly PermissionPromptView _permissionPromptView = new();
    private readonly TextEditor _editor = new();
    private readonly Throbber _throbber = new();
    private readonly Autocomplete _autocomplete;

    // Ur event queue — background turn task enqueues, main loop drains.
    private readonly ConcurrentQueue<AgentLoopEvent> _eventQueue = new();
    private readonly SemaphoreSlim _wakeSignal = new(0);

    // Host reference — used for resolving context window sizes from the active provider.
    private readonly UrHost _host;

    // Context window cache — keyed on model ID so we only resolve once per model.
    // Populated lazily on the first TurnCompleted for each model. All access is on
    // the main thread (resolved synchronously during event drain).
    private readonly Dictionary<string, int?> _contextWindowCache = new(StringComparer.OrdinalIgnoreCase);

    // Turn state.
    private Ur.Sessions.UrSession? _session;
    private CancellationTokenSource? _turnCts;
    private bool _turnActive;
    private string? _queuedInput;
    private AssistantTextEntry? _currentAssistantEntry;
    private int? _contextPercent;
    private string _workspacePath;

    // Permission prompt bridge.
    private TaskCompletionSource<PermissionResponse>? _permissionTcs;

    // Exit flag.
    private bool _exit;

    public OxApp(UrHost host, InputCoordinator coordinator, int width, int height, string workspacePath)
    {
        _host = host;
        _coordinator = coordinator;
        _buffer = new ConsoleBuffer(width, height);
        _workspacePath = workspacePath;

        // Force an explicit black background for every cell that uses
        // Color.Default. Without this, empty cells emit SGR 49 ("terminal
        // default"), which is whatever color the user configured in their
        // terminal — usually not black.
        _buffer.DefaultBackgroundOverride = OxThemePalette.Ox.Background;

        // Build autocomplete from the host's command registry.
        var commandRegistry = new Ur.Skills.CommandRegistry(host.BuiltInCommands, host.Skills);
        var autocompleteEngine = new AutocompleteEngine(commandRegistry);
        _autocomplete = new Autocomplete(autocompleteEngine);

        // Create session with permission callback.
        var callbacks = new TurnCallbacks
        {
            RequestPermissionAsync = OnPermissionRequestAsync,
        };
        _session = host.CreateSession(callbacks);
    }

    /// <summary>Run the main application loop until exit is signalled.</summary>
    public async Task RunAsync()
    {
        while (!_exit)
        {
            // Check for terminal resize.
            CheckResize();

            // Drain input events from Te's channel.
            DrainInput();

            // Drain Ur agent events from the background turn task.
            DrainAgentEvents();

            // Render the frame.
            Render();

            // Break before blocking if exit was requested during this iteration
            // (e.g. by Ctrl+C or /quit). Without this check the loop would
            // hang on the await below, requiring an extra keypress to unblock.
            if (_exit) break;

            // Wait for the next signal: input, Ur event, or tick.
            var inputReady = _coordinator.Reader.WaitToReadAsync().AsTask();
            var wakeReady = _wakeSignal.WaitAsync();

            // 1-second tick when turn is active (for throbber animation).
            var tick = _turnActive
                ? Task.Delay(TimeSpan.FromSeconds(1))
                : Task.Delay(Timeout.Infinite);

            await Task.WhenAny(inputReady, wakeReady, tick);

            // Tick the throbber if a turn is active.
            if (_turnActive)
                _throbber.Tick();
        }
    }

    private void CheckResize()
    {
        var (width, height) = GetTerminalSize();
        if (width != _buffer.Width || height != _buffer.Height)
            _buffer.Resize(width, height);
    }

    private void DrainInput()
    {
        while (_coordinator.Reader.TryRead(out var inputEvent))
        {
            switch (inputEvent)
            {
                case KeyInputEvent keyEvent:
                    HandleKey(keyEvent.Key);
                    break;
                case MouseInputEvent mouseEvent:
                    HandleMouse(mouseEvent.Mouse);
                    break;
            }
        }
    }

    private void HandleKey(KeyEventArgs args)
    {
        var keyCode = args.KeyCode;
        var bare = keyCode.WithoutModifiers();

        // Global shortcuts — always active.
        if (bare == KeyCode.C && keyCode.HasCtrl())
        {
            _exit = true;
            return;
        }

        // Screen dump shortcuts.
        if (ScreenDumpWriter.IsDumpShortcut((int)keyCode))
        {
            CaptureScreenDump();
            return;
        }

        // If the permission prompt is active, route input there.
        if (_permissionPromptView.IsActive)
        {
            HandlePermissionInput(args);
            return;
        }

        // Escape during active turn → cancel.
        if (bare == KeyCode.Esc && _turnActive)
        {
            CancelTurn();
            return;
        }

        // Ctrl+D with empty buffer → exit.
        if (bare == KeyCode.D && keyCode.HasCtrl())
        {
            if (_editor.Text.Length == 0)
                _exit = true;
            return;
        }

        // Tab → autocomplete.
        if (bare == KeyCode.Tab)
        {
            _autocomplete.TryApply(_editor);
            return;
        }

        // Enter → submit.
        if (bare == KeyCode.Enter)
        {
            SubmitInput();
            return;
        }

        // Text editing keys.
        switch (bare)
        {
            case KeyCode.Backspace:
                _editor.Backspace();
                return;
            case KeyCode.Delete:
                _editor.Delete();
                return;
            case KeyCode.CursorLeft:
                _editor.MoveLeft();
                return;
            case KeyCode.CursorRight:
                _editor.MoveRight();
                return;
            case KeyCode.Home:
                _editor.Home();
                return;
            case KeyCode.End:
                _editor.End();
                return;
        }

        // Printable character insertion.
        if (args.KeyChar >= ' ' && args.KeyChar != '\0' && !keyCode.HasCtrl() && !keyCode.HasAlt())
        {
            _editor.InsertChar(args.KeyChar);
        }
    }

    private void HandleMouse(MouseEventArgs args)
    {
        // Scroll wheel → manual scroll.
        if (args.HasFlag(MouseFlags.WheeledUp))
            _conversationView.ScrollUp(3);
        else if (args.HasFlag(MouseFlags.WheeledDown))
            _conversationView.ScrollDown(3);
    }

    private void HandlePermissionInput(KeyEventArgs args)
    {
        var bare = args.KeyCode.WithoutModifiers();

        // Escape, Ctrl+C, or 'n' → deny.
        if (bare == KeyCode.Esc || (bare == KeyCode.C && args.KeyCode.HasCtrl()) || bare == KeyCode.N)
        {
            ResolvePermission(new PermissionResponse(Granted: false, Scope: null));
            return;
        }

        // Enter → approve (or submit typed scope).
        if (bare == KeyCode.Enter)
        {
            var input = _permissionPromptView.Editor.Text.Trim().ToLowerInvariant();
            var request = _permissionPromptView.ActiveRequest!;

            if (input is "n" or "no")
            {
                ResolvePermission(new PermissionResponse(Granted: false, Scope: null));
                return;
            }

            // Try to parse a scope name from the input.
            PermissionScope? scope = input switch
            {
                "once" => PermissionScope.Once,
                "session" => PermissionScope.Session,
                "workspace" => PermissionScope.Workspace,
                "always" => PermissionScope.Always,
                _ => null,
            };

            // Default: approve with the first available scope.
            scope ??= request.AllowedScopes.Count > 0 ? request.AllowedScopes[0] : PermissionScope.Once;

            ResolvePermission(new PermissionResponse(Granted: true, Scope: scope));
            return;
        }

        // Text editing in the permission prompt.
        if (bare == KeyCode.Backspace)
            _permissionPromptView.Editor.Backspace();
        else if (args.KeyChar >= ' ' && args.KeyChar != '\0' && !args.KeyCode.HasCtrl())
            _permissionPromptView.Editor.InsertChar(args.KeyChar);
    }

    private void SubmitInput()
    {
        var text = _editor.Text.Trim();
        if (text.Length == 0) return;

        _editor.Clear();

        // Check for slash commands.
        if (text.StartsWith('/'))
        {
            var parts = text[1..].Split(' ', 2);
            var command = parts[0].ToLowerInvariant();

            if (command == "quit")
            {
                _exit = true;
                return;
            }

            // Check for built-in commands that are stubs.
            if (command is "clear" or "model" or "set")
            {
                _conversationView.AddEntry(new ErrorEntry($"Command /{command} is not yet implemented."));
                return;
            }

            // Unknown command.
            _conversationView.AddEntry(new ErrorEntry($"Unknown command: {command}"));
            return;
        }

        // Add user message to the conversation.
        _conversationView.AddEntry(new UserMessageEntry(text));

        // If a turn is already active, queue the input.
        if (_turnActive)
        {
            _queuedInput = text;
            return;
        }

        StartTurn(text);
    }

    private void StartTurn(string input)
    {
        if (_session is null) return;

        _turnActive = true;
        _currentAssistantEntry = null;
        _throbber.Start();

        _turnCts = new CancellationTokenSource();
        var ct = _turnCts.Token;

        // Fire-and-forget: the background task enqueues events; we don't await it.
        _ = Task.Run(async () =>
        {
            try
            {
                await foreach (var evt in _session.RunTurnAsync(input, ct))
                {
                    _eventQueue.Enqueue(evt);
                    _wakeSignal.Release();
                }
            }
            catch (OperationCanceledException)
            {
                // Cancellation is expected — the main loop handles the UI state.
            }
            catch (Exception ex)
            {
                _eventQueue.Enqueue(new TurnError
                {
                    Message = ex.Message,
                    IsFatal = true,
                });
                _wakeSignal.Release();
            }
        });
    }

    private void CancelTurn()
    {
        _turnCts?.Cancel();
        _turnActive = false;
        _throbber.Reset();
        _currentAssistantEntry = null;
        _conversationView.AddEntry(new CancellationEntry());
    }

    private void DrainAgentEvents()
    {
        while (_eventQueue.TryDequeue(out var evt))
        {
            switch (evt)
            {
                case ResponseChunk chunk:
                    // Skip empty chunks — they'd create blank conversation bubbles.
                    if (chunk.Text.Length == 0)
                        break;

                    if (_currentAssistantEntry is null)
                    {
                        _currentAssistantEntry = new AssistantTextEntry();
                        _conversationView.AddEntry(_currentAssistantEntry);
                    }
                    _currentAssistantEntry.Append(chunk.Text);
                    break;

                case ToolCallStarted toolStart:
                    _currentAssistantEntry = null; // New tool call breaks assistant text.
                    _conversationView.AddEntry(new ToolCallEntry
                    {
                        CallId = toolStart.CallId,
                        ToolName = toolStart.ToolName,
                        FormattedSignature = toolStart.FormatCall(),
                        Status = ToolCallStatus.Started,
                    });
                    break;

                case ToolAwaitingApproval awaiting:
                    var awaitingEntry = _conversationView.FindToolCall(awaiting.CallId);
                    if (awaitingEntry is not null)
                        awaitingEntry.Status = ToolCallStatus.AwaitingApproval;
                    break;

                case ToolCallCompleted completed:
                    var completedEntry = _conversationView.FindToolCall(completed.CallId);
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
                    _currentAssistantEntry = null;
                    _conversationView.AddEntry(new PlanEntry
                    {
                        Items = todoUpdated.Items.Select(i => new PlanItem(
                            i.Content,
                            i.Status switch
                            {
                                Ur.Todo.TodoStatus.Completed => PlanItemStatus.Completed,
                                Ur.Todo.TodoStatus.InProgress => PlanItemStatus.InProgress,
                                _ => PlanItemStatus.Pending,
                            })).ToList(),
                    });
                    break;

                case SubagentEvent subEvt:
                    HandleSubagentEvent(subEvt);
                    break;

                case TurnCompleted turnCompleted:
                    _turnActive = false;
                    _throbber.Reset();
                    _currentAssistantEntry = null;
                    if (turnCompleted.InputTokens is { } tokens)
                    {
                        // Compute context fill % using the provider's reported context window.
                        // The cache avoids re-resolving every turn — each provider caches internally
                        // too, so this is a cheap dictionary lookup after the first call.
                        var modelId = _session?.ActiveModelId;
                        if (modelId is not null)
                        {
                            if (!_contextWindowCache.TryGetValue(modelId, out var contextWindow))
                            {
                                // First time seeing this model — resolve synchronously and cache.
                                // This blocks briefly on first use per model, but provider resolutions
                                // are fast: local dictionary lookups for OpenAI/OpenRouter/Fake, one
                                // cached-after-first API call for Google/Ollama. The turn itself took
                                // seconds, so a sub-second resolution delay is imperceptible.
                                try
                                {
                                    contextWindow = _host.ResolveContextWindowAsync(modelId)
                                        .GetAwaiter().GetResult();
                                }
                                catch
                                {
                                    contextWindow = null;
                                }
                                _contextWindowCache[modelId] = contextWindow;
                            }

                            _contextPercent = contextWindow is > 0
                                ? (int)Math.Max(1, tokens * 100 / contextWindow.Value)
                                : null;
                        }
                        else
                        {
                            _contextPercent = null;
                        }
                    }

                    // If input was queued during the turn, start a new turn.
                    if (_queuedInput is not null)
                    {
                        var queued = _queuedInput;
                        _queuedInput = null;
                        _conversationView.AddEntry(new UserMessageEntry(queued));
                        StartTurn(queued);
                    }
                    break;

                case TurnError error:
                    if (error.IsFatal)
                    {
                        _turnActive = false;
                        _throbber.Reset();
                        _currentAssistantEntry = null;
                    }
                    _conversationView.AddEntry(new ErrorEntry(error.Message));
                    break;
            }
        }
    }

    private void HandleSubagentEvent(SubagentEvent subEvt)
    {
        // Find or create a container for this sub-agent.
        var container = _conversationView.FindSubagentContainer(subEvt.SubagentId);
        if (container is null)
        {
            container = new SubagentContainerEntry
            {
                CallId = subEvt.SubagentId,
                FormattedSignature = $"Subagent({subEvt.SubagentId})",
            };
            _conversationView.AddEntry(container);
        }

        // Add the inner event as a child entry.
        switch (subEvt.Inner)
        {
            case ResponseChunk chunk:
                var lastChild = container.Children.LastOrDefault();
                if (lastChild is AssistantTextEntry childAssistant)
                {
                    childAssistant.Append(chunk.Text);
                }
                else
                {
                    var newEntry = new AssistantTextEntry();
                    newEntry.Append(chunk.Text);
                    container.Children.Add(newEntry);
                }
                break;

            case ToolCallStarted toolStart:
                container.Children.Add(new ToolCallEntry
                {
                    CallId = toolStart.CallId,
                    ToolName = toolStart.ToolName,
                    FormattedSignature = toolStart.FormatCall(),
                });
                break;

            case TurnCompleted:
                container.Status = ToolCallStatus.Succeeded;
                break;

            case TurnError error:
                container.Children.Add(new ErrorEntry(error.Message));
                break;
        }
    }

    private async ValueTask<PermissionResponse> OnPermissionRequestAsync(
        PermissionRequest request,
        CancellationToken ct)
    {
        // Create a TCS that the main loop will complete when the user responds.
        _permissionTcs = new TaskCompletionSource<PermissionResponse>();
        _permissionPromptView.ActiveRequest = request;
        _permissionPromptView.Editor.Clear();
        _wakeSignal.Release(); // Wake the main loop to show the prompt.

        // Block the Ur turn task until the user responds.
        using var reg = ct.Register(() =>
            _permissionTcs.TrySetResult(new PermissionResponse(Granted: false, Scope: null)));

        return await _permissionTcs.Task;
    }

    private void ResolvePermission(PermissionResponse response)
    {
        _permissionPromptView.ActiveRequest = null;
        _permissionPromptView.Editor.Clear();
        _permissionTcs?.TrySetResult(response);
        _permissionTcs = null;
    }

    private void Render()
    {
        _buffer.Clear();

        var width = _buffer.Width;
        var height = _buffer.Height;

        // Conversation area: from row 0 to (height - InputAreaView.Height - 1).
        var conversationHeight = height - InputAreaView.Height;
        _conversationView.Render(_buffer, 0, 0, width, conversationHeight);

        // Permission prompt: floats above the input area when active.
        if (_permissionPromptView.IsActive)
        {
            var promptY = conversationHeight - PermissionPromptView.Height;
            _permissionPromptView.Render(_buffer, 0, promptY, width);
        }

        // Input area: fixed at the bottom.
        var inputY = height - InputAreaView.Height;
        var ghostText = _autocomplete.GetGhostText(_editor.Text);
        var statusRight = InputStatusFormatter.Compose(_contextPercent, _session?.ActiveModelId);

        _inputAreaView.Render(
            _buffer,
            0, inputY, width,
            _editor,
            ghostText,
            statusRight,
            _turnActive ? _throbber : null,
            !_permissionPromptView.IsActive);

        _buffer.Render(Console.Out);
    }

    private void CaptureScreenDump()
    {
        // Build plain text from the buffer.
        var lines = new string[_buffer.Height];
        for (var row = 0; row < _buffer.Height; row++)
        {
            var chars = new char[_buffer.Width];
            for (var col = 0; col < _buffer.Width; col++)
                chars[col] = _buffer.GetRenderedCell(col, row).Rune;
            lines[row] = new string(chars).TrimEnd();
        }

        var screenText = string.Join('\n', lines);
        ScreenDumpWriter.Write(_workspacePath, screenText, DateTimeOffset.UtcNow);
    }

    public void Dispose()
    {
        _turnCts?.Dispose();
        _wakeSignal.Dispose();
    }

    private static (int Width, int Height) GetTerminalSize()
    {
        var width = Console.WindowWidth;
        var height = Console.WindowHeight;
        return (Math.Max(20, width), Math.Max(10, height));
    }
}
