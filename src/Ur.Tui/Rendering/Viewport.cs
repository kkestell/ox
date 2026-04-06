namespace Ur.Tui.Rendering;

/// <summary>
/// Full-screen display engine that renders the conversation into the alternate
/// screen buffer.
///
/// Layout:
///   Rows 1 .. (height-1) — conversation viewport (shows the tail of all lines)
///   Row height            — input row (prompt + user typing area)
///
/// Rendering is decoupled from event arrival via a dirty flag. When any renderable
/// fires <see cref="IRenderable.Changed"/>, we set a flag rather than redrawing
/// immediately. A background loop calls <see cref="Redraw"/> at ~30 fps when
/// dirty. This prevents per-character redraws during fast streaming — the screen
/// updates at a readable rate regardless of how quickly chunks arrive.
///
/// The viewport must be stopped on every exit path (normal, Ctrl+C, unhandled
/// exception) to restore the primary screen buffer and cursor visibility. The
/// caller is responsible for registering cleanup handlers.
/// </summary>
internal sealed class Viewport : IDisposable
{
    private readonly EventList _root;

    // True when the event list has changed and the next timer tick should redraw.
    private volatile bool _dirty;

    // Current input row text (prompt + any typed characters). Redrawn every
    // Redraw() call so the input area always reflects the latest state.
    private string _inputPrompt = "> ";

    // Background timer that drives redraws at ~30 fps when dirty.
    private readonly Timer _redrawTimer;

    // Guards Redraw() against concurrent calls from the timer and an explicit
    // caller (e.g. after SetInputPrompt is called mid-turn).
    private readonly object _redrawLock = new();

    // True between Start() and Stop() — prevents stray redraws after shutdown.
    private bool _running;

    public EventList Root => _root;

    public Viewport(EventList root)
    {
        _root = root;

        // Subscribe to the root's Changed event so any descendant mutation marks
        // us dirty. The timer then picks it up within ~33ms.
        _root.Changed += () => _dirty = true;

        // ~30 fps. We use a period of 33ms with a dueTime of 33ms so the first
        // tick doesn't fire immediately on creation.
        _redrawTimer = new Timer(_ => TickRedraw(), null, 33, 33);
    }

    // --- Lifecycle ---

    /// <summary>
    /// Enters the alternate screen buffer, hides the cursor, and draws the
    /// initial empty frame. Must be called before the REPL loop starts.
    /// Idempotent — safe to call multiple times (second call is a no-op).
    /// </summary>
    public void Start()
    {
        if (_running)
            return;
        _running = true;
        Terminal.EnterAlternateBuffer();
        Terminal.HideCursor();
        Terminal.ClearScreen();
        Redraw();
    }

    /// <summary>
    /// Restores the terminal to its original state: shows the cursor and exits
    /// the alternate buffer. Safe to call multiple times (idempotent).
    ///
    /// Acquires <see cref="_redrawLock"/> so that terminal-restore writes do not
    /// interleave with an in-progress Redraw() call from the background timer.
    /// </summary>
    public void Stop()
    {
        lock (_redrawLock)
        {
            if (!_running)
                return;
            _running = false;
            Terminal.ShowCursor();
            Terminal.ExitAlternateBuffer();
        }
    }

    // --- Input row ---

    /// <summary>
    /// Updates the input row prompt text and triggers an immediate redraw.
    /// The prompt text includes both the fixed prefix (e.g. "> ") and any
    /// characters the user has already typed, so the caller owns the full string.
    /// </summary>
    public void SetInputPrompt(string prompt)
    {
        _inputPrompt = prompt;
        _dirty = true;
    }

    // --- Redraw ---

    /// <summary>Called by the timer on the ThreadPool. Redraws only when dirty.</summary>
    private void TickRedraw()
    {
        if (_dirty && _running)
            Redraw();
    }

    /// <summary>
    /// Redraws the full viewport: the conversation tail fills rows 1..(H-1),
    /// and the input prompt occupies row H.
    ///
    /// Auto-scroll: we always show the tail (most recent lines). There is no
    /// manual scrollback — the alternate buffer owns the display, and the
    /// conversation model is the scroll history.
    /// </summary>
    public void Redraw()
    {
        lock (_redrawLock)
        {
            if (!_running)
                return;

            _dirty = false;

            var (width, height) = Terminal.GetSize();
            var viewportHeight = height - 1; // reserve last row for input

            // Get all content lines from the conversation tree.
            var allLines = _root.Render(width);

            // Auto-scroll: take only the tail that fits on screen.
            var startIndex = Math.Max(0, allLines.Count - viewportHeight);

            for (var row = 1; row <= viewportHeight; row++)
            {
                var lineIndex = startIndex + (row - 1);
                var text = lineIndex < allLines.Count ? allLines[lineIndex] : "";
                Terminal.Write(row, 1, text);
            }

            // Input row: always at the bottom. ShowCursor / HideCursor bracket
            // the write so the cursor appears at the end of the input text.
            Terminal.Write(height, 1, _inputPrompt);
            Terminal.ShowCursor();
            // Position the cursor after the prompt text so it appears there.
            Terminal.MoveCursor(height, _inputPrompt.Length + 1);
        }
    }

    public void Dispose()
    {
        _redrawTimer.Dispose();
        Stop();
    }
}
