namespace Ur.Tui.Rendering;

/// <summary>
/// Full-screen display engine that renders the conversation into the alternate
/// screen buffer.
///
/// Layout (1-based terminal rows, 0-based buffer rows):
///   Row 1                 — header: session ID left-aligned (BrightBlack)
///   Row 2                 — header rule (━ heavy horizontal line)
///   Rows 3 .. (height-5) — conversation viewport (shows the tail of all rows)
///   Row height-4          — top rule (━ heavy horizontal line)
///   Row height-3          — input text row (❯ prompt, typed text in white)
///   Row height-2          — bottom rule (─ light horizontal line)
///   Row height-1          — status line (throbber left, model ID right)
///   Row height            — blank line
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

    // Header state: session ID displayed left-aligned in the header row.
    private string? _sessionId;

    // Status line state: the throbber animates while a turn is running, and the
    // model ID is displayed right-aligned. Both live below the bottom rule.
    private volatile bool _turnRunning;
    private string? _modelId;

    // Throbber animation: five identical glyphs that cycle through a
    // black-to-white grayscale gradient. Each frame shifts which color
    // maps to which position, creating a pulsing wave effect. The gradient
    // uses the xterm 256-color grayscale ramp (indices 232–255).
    private const char ThrobberGlyph = '■';
    private const int ThrobberCount = 5;
    private static readonly Color[] ThrobberColors =
    [
        Color.FromIndex(232), // near-black
        Color.FromIndex(238),
        Color.FromIndex(244), // mid-gray
        Color.FromIndex(249),
        Color.FromIndex(255)  // near-white
    ];
    private const int ThrobberFrameMs = 200;

    // Box-drawing characters for the input area rules.
    private const char TopRuleChar    = '━'; // U+2501 BOX DRAWINGS HEAVY HORIZONTAL
    private const char BottomRuleChar = '─'; // U+2500 BOX DRAWINGS LIGHT HORIZONTAL

    // Number of terminal rows reserved for the input area:
    // top rule + text row + bottom rule + status line + blank line.
    private const int InputAreaRows = 5;

    // Number of terminal rows reserved for the header area:
    // header content row + heavy rule.
    private const int HeaderRows = 2;

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

    // --- Header ---

    /// <summary>
    /// Sets the session ID displayed left-aligned in the header row.
    /// Call once before <see cref="Start()"/> so the header is visible on the
    /// first frame.
    /// </summary>
    public void SetSessionId(string id)
    {
        _sessionId = id;
        _dirty = true;
    }

    // --- Status line ---

    /// <summary>
    /// Shows or hides the throbber on the status line. While running, the
    /// timer forces continuous redraws so the throbber animation advances.
    /// </summary>
    public void SetTurnRunning(bool running)
    {
        _turnRunning = running;
        _dirty = true;
    }

    /// <summary>
    /// Sets the model identifier displayed right-aligned on the status line.
    /// </summary>
    public void SetModelId(string? modelId)
    {
        _modelId = modelId;
        _dirty = true;
    }

    // --- Redraw ---

    /// <summary>
    /// Called by the timer on the ThreadPool. Redraws only when dirty, but
    /// forces the dirty flag on while the throbber is animating so the
    /// rotation advances even when no new events arrive.
    /// </summary>
    private void TickRedraw()
    {
        if (_turnRunning)
            _dirty = true;

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
    private void Redraw()
    {
        lock (_redrawLock)
        {
            if (!_running)
                return;

            _dirty = false;

            var (width, height) = Terminal.GetSize();
            var buffer = BuildFrame(width, height);
            Terminal.Flush(buffer);
        }
    }

    /// <summary>
    /// Core layout engine: builds a frame buffer for the given terminal size.
    /// Called by both <see cref="Redraw"/> (which then flushes to the terminal)
    /// and <see cref="BuildFrame"/> (used in tests).
    ///
    /// Each region is rendered by a dedicated private method so the overall
    /// layout sequence is immediately visible here. The methods are kept as
    /// private helpers in Viewport (no new classes) — appropriate for the
    /// current 4-region layout.
    /// </summary>
    private ScreenBuffer BuildFrame(int width, int height)
    {
        var viewportHeight = height - InputAreaRows - HeaderRows;
        var buffer = new ScreenBuffer(width, height);

        RenderHeader(buffer, width);
        RenderConversation(buffer, width, viewportHeight);
        RenderInputArea(buffer, width, viewportHeight);
        RenderStatusBar(buffer, width, viewportHeight);

        return buffer;
    }

    /// <summary>
    /// Renders the header region: session ID (row 0) and a heavy horizontal rule (row 1).
    /// </summary>
    private void RenderHeader(ScreenBuffer buffer, int width)
    {
        // Session ID left-aligned in BrightBlack so it is visually subordinate
        // to the conversation content below.
        var headerRow = new CellRow();
        if (_sessionId is not null)
            headerRow.Append(_sessionId, Color.BrightBlack, Color.Default);
        buffer.WriteRow(0, headerRow);

        // Heavy horizontal line mirroring the input area top rule.
        var headerRule = CellRow.FromText(new string(TopRuleChar, width), Color.BrightBlack, Color.Default);
        buffer.WriteRow(1, headerRule);
    }

    /// <summary>
    /// Renders the conversation region: tail-clips the EventList rows to fit
    /// between the header and input area.
    /// </summary>
    private void RenderConversation(ScreenBuffer buffer, int width, int viewportHeight)
    {
        var allRows   = _root.Render(width);
        var startIndex = Math.Max(0, allRows.Count - viewportHeight);

        for (var bufRow = 0; bufRow < viewportHeight; bufRow++)
        {
            var rowIndex = startIndex + bufRow;
            if (rowIndex < allRows.Count)
                buffer.WriteRow(HeaderRows + bufRow, allRows[rowIndex]);
            // Rows beyond the content stay Cell.Empty (blank / default colors).
        }
    }

    /// <summary>
    /// Renders the input area: top rule, text row (prompt + cursor), and bottom rule.
    /// </summary>
    private void RenderInputArea(ScreenBuffer buffer, int width, int viewportHeight)
    {
        // Top rule: heavy horizontal line spanning the full width.
        var topRule = CellRow.FromText(new string(TopRuleChar, width), Color.BrightBlack, Color.Default);
        buffer.WriteRow(viewportHeight + HeaderRows, topRule);

        // Text row: chevron in bright black, typed text in white, no background.
        // The system cursor is hidden; we paint our own block cursor using
        // CellStyle.Reverse so it's visible against the default background.
        var textRow = new CellRow();
        if (_inputPrompt.StartsWith('❯'))
        {
            textRow.Append('❯', Color.White, Color.Default);
            textRow.Append(_inputPrompt[1..], Color.White, Color.Default);
        }
        else
        {
            textRow.Append(_inputPrompt, Color.White, Color.Default);
        }
        // Hide the block cursor while a turn is running — the throbber already
        // communicates activity and the input row isn't accepting input anyway.
        if (!_turnRunning)
            textRow.Append(' ', Color.Default, Color.Default, CellStyle.Reverse);
        buffer.WriteRow(viewportHeight + HeaderRows + 1, textRow);

        // Bottom rule: light horizontal line spanning the full width.
        var bottomRule = CellRow.FromText(new string(BottomRuleChar, width), Color.BrightBlack, Color.Default);
        buffer.WriteRow(viewportHeight + HeaderRows + 2, bottomRule);
    }

    /// <summary>
    /// Renders the status bar: animated throbber on the left (only while a turn
    /// is running) and model ID right-aligned.
    /// </summary>
    private void RenderStatusBar(ScreenBuffer buffer, int width, int viewportHeight)
    {
        var statusRow = new CellRow();

        if (_turnRunning)
        {
            // Cycle the color gradient across positions each frame,
            // so the bright "peak" appears to travel rightward.
            var frame = (int)(Environment.TickCount64 / ThrobberFrameMs) % ThrobberColors.Length;
            for (var i = 0; i < ThrobberCount; i++)
            {
                if (i > 0)
                    statusRow.Append(' ', Color.Default, Color.Default);
                var colorIdx = ((i - frame) % ThrobberColors.Length + ThrobberColors.Length) % ThrobberColors.Length;
                statusRow.Append(ThrobberGlyph, ThrobberColors[colorIdx], Color.Default);
            }
        }

        if (_modelId is not null)
        {
            // Pad with spaces so the model ID is flush against the right edge.
            var padNeeded = width - statusRow.Cells.Count - _modelId.Length;
            for (var i = 0; i < padNeeded; i++)
                statusRow.Append(' ', Color.Default, Color.Default);
            statusRow.Append(_modelId, Color.BrightBlack, Color.Default);
        }

        buffer.WriteRow(viewportHeight + HeaderRows + 3, statusRow);
    }

    public void Dispose()
    {
        _redrawTimer.Dispose();
        Stop();
    }
}
