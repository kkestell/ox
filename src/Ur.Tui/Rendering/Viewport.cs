namespace Ur.Tui.Rendering;

/// <summary>
/// Full-screen display engine that renders the conversation into the alternate
/// screen buffer.
///
/// Layout (1-based terminal rows, 0-based buffer rows):
///   Rows 1 .. (height-5) — conversation viewport (shows the tail of all rows)
///   Row height-4          — top margin row (blank/black, separates conversation from input box)
///   Row height-3          — input decoration row (▆ in blue-on-cyan, inset 1 cell each side)
///   Row height-2          — input text row (prompt + user typing on blue bg, inset 1 cell each side)
///   Row height-1          — input bottom padding row (solid blue, inset 1 cell each side)
///   Row height            — bottom margin row (blank/black)
///
/// Rendering is decoupled from event arrival via a dirty flag. When any renderable
/// fires <see cref="IRenderable.Changed"/>, we set a flag rather than redrawing
/// immediately. A background loop calls <see cref="Redraw"/> at ~30 fps when
/// dirty. This prevents per-character redraws during fast streaming — the screen
/// updates at a readable rate regardless of how quickly chunks arrive.
///
/// The viewport owns the ScreenBuffer → Terminal.Flush pipeline. No other code
/// writes to the terminal during a frame; all ANSI encoding happens in Terminal.Flush.
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
    private string _inputPrompt = "❯ ";

    // Background timer that drives redraws at ~30 fps when dirty.
    private readonly Timer _redrawTimer;

    // Guards Redraw() against concurrent calls from the timer and an explicit
    // caller (e.g. after SetInputPrompt is called mid-turn).
    private readonly object _redrawLock = new();

    // True between Start() and Stop() — prevents stray redraws after shutdown.
    private bool _running;

    // Colors for the input box chrome.
    // The decoration row uses a ▆ glyph (bottom 3/4 block) with blue-on-cyan:
    // the top 1/4 shows cyan and the bottom 3/4 shows blue, creating a visual
    // transition cap into the solid blue input text row below.
    private static readonly Color InputDecoFg = Color.Blue;  // ▆ glyph foreground
    private static readonly Color InputDecobg = Color.Cyan;  // decoration row background
    private static readonly Color InputTextBg = Color.Blue;  // text row and bottom row background
    private const char InputTopChar = '▆'; // U+2586 LOWER THREE QUARTERS BLOCK

    // Number of terminal rows reserved for the input box:
    // top margin + decoration + text + bottom padding + bottom margin.
    private const int InputAreaRows = 5;

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
    /// The prompt text includes both the fixed prefix (e.g. "❯ ") and any
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
    /// Redraws the full viewport into a <see cref="ScreenBuffer"/> then flushes it
    /// to the terminal in one shot via <see cref="Terminal.Flush(ScreenBuffer)"/>.
    ///
    /// Auto-scroll: we always show the tail (most recent rows). There is no
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

            // Reserve InputAreaRows rows for the input box; the rest is conversation.
            var viewportHeight = height - InputAreaRows;

            // Build a fresh buffer for this frame. All cells start as Cell.Empty.
            var buffer = new ScreenBuffer(width, height);

            // --- Conversation rows (0-based buffer rows 0..viewportHeight-1) ---

            var allRows   = _root.Render(width);
            var startIndex = Math.Max(0, allRows.Count - viewportHeight);

            for (var bufRow = 0; bufRow < viewportHeight; bufRow++)
            {
                var rowIndex = startIndex + bufRow;
                if (rowIndex < allRows.Count)
                    buffer.WriteRow(bufRow, allRows[rowIndex]);
                // Rows beyond the content stay Cell.Empty (blank / default colors).
            }

            // --- Input box (0-based buffer rows viewportHeight..height-1) ---

            // Margin row: one blank row separating the conversation from the input box.
            // Left as Cell.Empty (default colors) so it blends into the terminal background.
            // viewportHeight + 0 is left blank (no WriteRow call needed).

            // Decoration row: inset by 1 cell on each side so the blue box has black margins.
            // The ▆ character's bottom 3/4 shows the foreground color (blue) and the
            // top 1/4 shows the background (cyan), creating a stepped visual transition
            // into the solid blue text row immediately below.
            var decoRow = new CellRow();
            decoRow.Append(' ', Color.Default, Color.Default);                      // left margin (black)
            decoRow.Append(new string(InputTopChar, width - 2), InputDecoFg, InputDecobg); // inset deco
            decoRow.Append(' ', Color.Default, Color.Default);                      // right margin (black)
            buffer.WriteRow(viewportHeight + 1, decoRow);

            // Text row: 1 black margin cell, then blue content inset by 1 on each side.
            // The system cursor is hidden; we paint our own block cursor using CellStyle.Reverse,
            // which swaps fg and bg — producing a light block on the blue background.
            var textRow = new CellRow();
            textRow.Append(' ', Color.Default, Color.Default);                      // left margin (black)
            textRow.Append(' ', Color.Default, InputTextBg);                        // inner left pad
            textRow.Append(_inputPrompt, Color.Default, InputTextBg);
            textRow.Append(' ', Color.Default, InputTextBg, CellStyle.Reverse);     // block cursor
            textRow.PadRight(width - 1, InputTextBg);                               // fill blue to right edge
            textRow.Append(' ', Color.Default, Color.Default);                      // right margin (black)
            buffer.WriteRow(viewportHeight + 2, textRow);

            // Bottom padding row: solid blue, inset by 1 on each side, closes the box visually.
            var bottomRow = new CellRow();
            bottomRow.Append(' ', Color.Default, Color.Default);            // left margin (black)
            bottomRow.PadRight(width - 1, InputTextBg);                     // solid blue fill
            bottomRow.Append(' ', Color.Default, Color.Default);            // right margin (black)
            buffer.WriteRow(viewportHeight + 3, bottomRow);

            // Bottom margin row: left as Cell.Empty (default / black) — symmetric with top margin.

            // Flush the completed buffer to the terminal. This is the only call that
            // emits ANSI escape sequences for the entire frame.
            Terminal.Flush(buffer);
        }
    }

    public void Dispose()
    {
        _redrawTimer.Dispose();
        Stop();
    }
}
