using System.Text;
using Te.Input;

namespace Ox;

/// <summary>
/// Centralizes all keyboard input into a single class that reads from a shared
/// <see cref="InputCoordinator"/>.
///
/// Two ReadLine overloads (viewport-mode and direct-echo) share the same coordinator
/// so there is no race between concurrent key consumers — the coordinator serializes
/// events through a queue and dispatches them in ProcessPendingInput.
///
/// The optional <see cref="AutocompleteEngine"/> enables Tab completion in
/// <see cref="ReadLineInViewport"/>: after each buffer change the engine is
/// queried and the result is broadcast to the viewport via onCompletionChanged.
/// </summary>
internal sealed class InputReader(InputCoordinator coordinator, AutocompleteEngine? autocomplete = null)
{
    private readonly InputCoordinator _coordinator = coordinator;

    /// <summary>
    /// Reads a line of user input through the viewport's input row.
    /// Characters typed by the user are reflected in real time via
    /// <paramref name="onPromptChanged"/>. Tab accepts an active autocomplete
    /// suggestion if one is present. Returns the typed string on Enter,
    /// or null on EOF (Ctrl+D on empty buffer), Escape, or cancellation.
    /// </summary>
    /// <param name="promptPrefix">
    /// Fixed prefix shown before the user's typed text (e.g. "❯ ").
    /// </param>
    /// <param name="onPromptChanged">
    /// Called after every keystroke with the full prompt string (prefix + buffer)
    /// so the viewport can update the input row in real time.
    /// </param>
    /// <param name="onCompletionChanged">
    /// Called after every buffer change with the current completion suffix (or null
    /// when no completion applies). The viewport uses this to render ghost text.
    /// </param>
    /// <param name="ct">Cancellation token (linked to the app-level CTS).</param>
    public string? ReadLineInViewport(
        string promptPrefix,
        Action<string> onPromptChanged,
        Action<string?>? onCompletionChanged = null,
        CancellationToken ct = default)
    {
        var buffer = new StringBuilder();
        onPromptChanged(promptPrefix);
        onCompletionChanged?.Invoke(null);

        // The event handler captures these by-ref so the polling loop can see
        // the result after each ProcessPendingInput call.
        string? returnValue = null;
        bool done = false;

        // Handles each key event synchronously on the polling-loop thread.
        // ProcessPendingInput fires KeyReceived before returning, so mutations
        // to buffer/returnValue/done are visible immediately after the call.
        void OnKey(object? sender, KeyEventArgs e)
        {
            var baseCode = e.KeyCode.WithoutModifiers();

            switch (baseCode)
            {
                case KeyCode.Enter:
                    // Clear ghost text immediately so the input area is clean
                    // before the caller processes the submitted value.
                    onCompletionChanged?.Invoke(null);
                    returnValue = buffer.ToString();
                    done = true;
                    return;

                case KeyCode.Backspace:
                    if (buffer.Length > 0)
                        buffer.Remove(buffer.Length - 1, 1);
                    // Completion is recalculated below; pass null as a transient
                    // clear so the viewport never shows stale ghost text.
                    onCompletionChanged?.Invoke(null);
                    break;

                case KeyCode.Tab:
                    // Accept the current completion if one is available.
                    var suffix = autocomplete?.GetCompletion(buffer.ToString());
                    if (suffix is not null)
                    {
                        buffer.Append(suffix);
                        onPromptChanged(promptPrefix + buffer);
                        // Ghost text is gone — the buffer now contains the full name.
                        onCompletionChanged?.Invoke(null);
                    }
                    // No completion active — Tab is a no-op.
                    return;

                case KeyCode.D when e.KeyCode.HasCtrl() && buffer.Length == 0:
                    // Ctrl+D on empty buffer signals EOF.
                    returnValue = null;
                    done = true;
                    return;

                case KeyCode.Esc:
                    // Escape during input reading is a no-op: the escape monitor
                    // runs during turns (wired in Program.cs), not during line reading.
                    // Ignoring here matches the old behaviour where _readingLine blocked
                    // MonitorEscapeKeyAsync from consuming keystrokes.
                    return;

                default:
                    if (!char.IsControl(e.KeyChar) && e.KeyChar != '\0')
                        buffer.Append(e.KeyChar);
                    break;
            }

            // Update the viewport so the user sees what they have typed, then
            // recompute the completion suffix for the new buffer state.
            onPromptChanged(promptPrefix + buffer);
            onCompletionChanged?.Invoke(autocomplete?.GetCompletion(buffer.ToString()));
        }

        _coordinator.KeyReceived += OnKey;
        try
        {
            while (!ct.IsCancellationRequested && !done)
            {
                // ProcessPendingInput dispatches queued events from the
                // TerminalInputSource's background reader thread. The events
                // fire synchronously here — OnKey runs before this returns.
                _coordinator.ProcessPendingInput(ct);
                if (!done)
                    Thread.Sleep(20);
            }

            return done ? returnValue : null; // null on cancellation
        }
        finally
        {
            _coordinator.KeyReceived -= OnKey;
        }
    }

    /// <summary>
    /// Reads a line from stdin with cancellation support and manual character echo.
    /// Used only during the pre-viewport configuration phase where the viewport
    /// is not yet active.
    ///
    /// Because <see cref="TerminalInputSource"/> puts the terminal in raw mode
    /// immediately on construction, automatic echo is disabled. This method
    /// writes each printable character to Console.Out explicitly so the user
    /// sees what they type, matching the behaviour of the old cooked-mode readline.
    /// </summary>
    public string? ReadLine(CancellationToken ct)
    {
        var buffer = new StringBuilder();
        string? returnValue = null;
        bool done = false;

        void OnKey(object? sender, KeyEventArgs e)
        {
            var baseCode = e.KeyCode.WithoutModifiers();

            switch (baseCode)
            {
                case KeyCode.Enter:
                    Console.WriteLine();
                    returnValue = buffer.ToString();
                    done = true;
                    return;

                case KeyCode.Backspace:
                    if (buffer.Length > 0)
                    {
                        buffer.Remove(buffer.Length - 1, 1);
                        // Erase the echoed character: backspace + space + backspace.
                        Console.Write("\b \b");
                    }
                    return;

                case KeyCode.D when e.KeyCode.HasCtrl() && buffer.Length == 0:
                    // Ctrl+D on empty buffer signals EOF.
                    Console.WriteLine();
                    returnValue = null;
                    done = true;
                    return;

                default:
                    if (!char.IsControl(e.KeyChar) && e.KeyChar != '\0')
                    {
                        buffer.Append(e.KeyChar);
                        // Echo the character so the user can see what they typed.
                        Console.Write(e.KeyChar);
                    }
                    return;
            }
        }

        _coordinator.KeyReceived += OnKey;
        try
        {
            while (!ct.IsCancellationRequested && !done)
            {
                _coordinator.ProcessPendingInput(ct);
                if (!done)
                    Thread.Sleep(50);
            }

            if (!done)
                Console.WriteLine();
            return done ? returnValue : null;
        }
        finally
        {
            _coordinator.KeyReceived -= OnKey;
        }
    }
}
