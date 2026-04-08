using System.Text;

namespace Ox;

/// <summary>
/// Centralizes all Console.ReadKey access into a single class that serializes
/// keyboard reads between the line reader and the escape key monitor.
///
/// Two ReadLine overloads (viewport-mode and direct-echo) share a single
/// <see cref="_readingLine"/> flag that MonitorEscapeKeyAsync checks before
/// touching Console.ReadKey, preventing the two from racing over keystrokes.
///
/// The optional <see cref="AutocompleteEngine"/> enables Tab completion in
/// <see cref="ReadLineInViewport"/>: after each buffer change the engine is
/// queried and the result is broadcast to the viewport via onCompletionChanged.
/// </summary>
internal sealed class InputReader(AutocompleteEngine? autocomplete = null)
{
    // True while a ReadLine call is actively polling Console.ReadKey. The
    // escape monitor skips key reads when this is set, preventing the two
    // from racing over the same keystroke. This replaces the old
    // _pauseKeyMonitor volatile bool that lived in Program.cs.
    private volatile bool _readingLine;

    /// <summary>
    /// Reads a line of user input through the viewport's input row.
    /// Characters typed by the user are reflected in real time via
    /// <paramref name="onPromptChanged"/>. Tab accepts an active autocomplete
    /// suggestion if one is present. Returns the typed string on Enter,
    /// or null on EOF (Ctrl+D on empty buffer) or cancellation.
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

        _readingLine = true;
        try
        {
            while (!ct.IsCancellationRequested)
            {
                if (!Console.KeyAvailable)
                {
                    Thread.Sleep(20);
                    continue;
                }

                var key = Console.ReadKey(intercept: true);

                switch (key.Key)
                {
                    case ConsoleKey.Enter:
                        // Clear ghost text immediately so the input area is clean
                        // before the caller processes the submitted value.
                        onCompletionChanged?.Invoke(null);
                        return buffer.ToString();

                    case ConsoleKey.Backspace:
                        if (buffer.Length > 0)
                            buffer.Remove(buffer.Length - 1, 1);
                        // Completion is recalculated below; pass null as a transient
                        // clear so the viewport never shows stale ghost text.
                        onCompletionChanged?.Invoke(null);
                        break;

                    case ConsoleKey.Tab:
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
                        continue;

                    case ConsoleKey.D when key.Modifiers.HasFlag(ConsoleModifiers.Control)
                                           && buffer.Length == 0:
                        return null; // EOF

                    default:
                        if (!char.IsControl(key.KeyChar))
                            buffer.Append(key.KeyChar);
                        break;
                }

                // Update the viewport so the user sees what they have typed, then
                // recompute the completion suffix for the new buffer state.
                onPromptChanged(promptPrefix + buffer);
                onCompletionChanged?.Invoke(autocomplete?.GetCompletion(buffer.ToString()));
            }

            return null; // Cancellation
        }
        finally
        {
            _readingLine = false;
        }
    }

    /// <summary>
    /// Reads a line from stdin with cancellation support and direct console echo.
    /// Used only during the pre-viewport configuration phase where the viewport
    /// is not yet active. Polls <see cref="Console.KeyAvailable"/> rather than
    /// blocking so it remains responsive to cancellation.
    /// </summary>
    public string? ReadLine(CancellationToken ct)
    {
        var buffer = new StringBuilder();

        _readingLine = true;
        try
        {
            while (!ct.IsCancellationRequested)
            {
                if (!Console.KeyAvailable)
                {
                    Thread.Sleep(50);
                    continue;
                }

                var key = Console.ReadKey(intercept: true);

                switch (key.Key)
                {
                    case ConsoleKey.Enter:
                        Console.WriteLine();
                        return buffer.ToString();

                    case ConsoleKey.Backspace:
                        if (buffer.Length > 0)
                        {
                            buffer.Remove(buffer.Length - 1, 1);
                            Console.Write("\b \b");
                        }
                        continue;

                    case ConsoleKey.D when key.Modifiers.HasFlag(ConsoleModifiers.Control)
                                           && buffer.Length == 0:
                        return null; // EOF

                    default:
                        break;
                }

                if (char.IsControl(key.KeyChar))
                    continue;

                buffer.Append(key.KeyChar);
                Console.Write(key.KeyChar);
            }

            Console.WriteLine();
            return null;
        }
        finally
        {
            _readingLine = false;
        }
    }

    /// <summary>
    /// Polls for Escape key presses in the background and cancels
    /// <paramref name="turnCts"/> when detected. Automatically yields to
    /// any active <see cref="ReadLineInViewport"/> or <see cref="ReadLine"/>
    /// call — no external pause flag is needed.
    /// </summary>
    public Task MonitorEscapeKeyAsync(CancellationTokenSource turnCts)
    {
        return Task.Run(async () =>
        {
            while (!turnCts.Token.IsCancellationRequested)
            {
                await Task.Delay(50, CancellationToken.None);

                // Yield to the active line reader — it owns Console.ReadKey
                // while _readingLine is true. Without this check, we would
                // race over keystrokes and silently eat input.
                if (_readingLine)
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
}
