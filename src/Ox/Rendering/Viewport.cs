using Te.Rendering;

namespace Ox.Rendering;

/// <summary>
/// Full-screen display engine that renders the conversation into the alternate
/// screen buffer.
///
/// Layout (1-based terminal rows, 0-based buffer rows):
///   Rows 1 .. (height-5) — conversation viewport (shows the tail of all rows)
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
/// The viewport owns a persistent <see cref="ConsoleBuffer"/> (_buffer) that
/// dirty-tracks cells between frames. BuildFrame writes into _buffer on every
/// call; Render() then emits only the changed cells — much cheaper than a
/// full repaint. The buffer persists across frames so dirty-tracking works.
///
/// The viewport must be stopped on every exit path (normal, Ctrl+C, unhandled
/// exception) to restore the primary screen buffer and cursor visibility. The
/// caller is responsible for registering cleanup handlers.
/// </summary>
internal sealed class Viewport : IDisposable
{
    private readonly EventList _root;
    private readonly Sidebar? _sidebar;

    // Persistent double-buffer that dirty-tracks cells between frames.
    // Initialized in the constructor; resized when terminal dimensions change.
    // exposed as internal so tests can inspect rendered cells directly.
    internal readonly ConsoleBuffer _buffer;

    // Last known terminal dimensions, used to detect resize.
    private int _lastWidth;
    private int _lastHeight;

    // True when the event list has changed and the next timer tick should redraw.
    private volatile bool _dirty;

    // Current input row text (prompt + any typed characters). Redrawn every
    // Redraw() call so the input area always reflects the latest state.
    private string _inputPrompt = "❯ ";

    // Ghost-text completion suffix currently visible in the input row.
    // Null when no completion is active. Set by InputReader via the
    // onCompletionChanged callback, rendered by RenderInputArea.
    private string? _completionSuffix;

    // Background timer that drives redraws at ~30 fps when dirty.
    private readonly Timer _redrawTimer;

    // Guards Redraw() against concurrent calls from the timer and an explicit
    // caller (e.g. after SetInputPrompt is called mid-turn).
    private readonly object _redrawLock = new();

    // True between Start() and Stop() — prevents stray redraws after shutdown.
    private bool _running;

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

    // ASCII art displayed centered in the conversation area before the user
    // sends their first message. Each line is 12 columns wide.
    private static readonly string[] SplashLines =
    [
        "▒█▀▀▀█ ▀▄▒▄▀",
        "▒█░░▒█ ░▒█░░",
        "▒█▄▄▄█ ▄▀▒▀▄"
    ];

    // Box-drawing characters for the input area rules.
    private const char TopRuleChar    = '━'; // U+2501 BOX DRAWINGS HEAVY HORIZONTAL
    private const char BottomRuleChar = '─'; // U+2500 BOX DRAWINGS LIGHT HORIZONTAL

    // Number of terminal rows reserved for the input area:
    // top rule + text row + bottom rule + status line + blank line.
    private const int InputAreaRows = 5;

    // No header rows — session ID and context usage have been moved to the sidebar.
    private const int HeaderRows = 0;

    // Maximum sidebar width in columns. Capped to prevent the sidebar from
    // dominating narrow terminals.
    private const int MaxSidebarWidth = 36;

    // Box-drawing character for the vertical separator between the left column
    // and the sidebar.
    private const char SeparatorChar = '│'; // U+2502 BOX DRAWINGS LIGHT VERTICAL

    public Viewport(EventList root, Sidebar? sidebar = null)
    {
        _root    = root;
        _sidebar = sidebar;

        // Bootstrap the buffer at the current terminal size. Resize() is called
        // at the start of each BuildFrame when dimensions change, so this initial
        // size only needs to be a reasonable default, not exact.
        var (initialWidth, initialHeight) = Terminal.GetSize();
        _buffer      = new ConsoleBuffer(initialWidth, initialHeight);
        _lastWidth   = initialWidth;
        _lastHeight  = initialHeight;

        // Subscribe to the root's Changed event so any descendant mutation marks
        // us dirty. The timer then picks it up within ~33ms.
        _root.Changed += () => _dirty = true;

        // Subscribe to sidebar changes so todo updates trigger a redraw.
        if (_sidebar is not null)
            _sidebar.Changed += () => _dirty = true;

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

    /// <summary>
    /// Sets the ghost-text completion suffix shown in the input row and triggers
    /// an immediate redraw. Pass null to clear the ghost text (e.g. after Tab
    /// accepts the completion or the user presses Enter or Backspace).
    /// </summary>
    public void SetCompletion(string? suffix)
    {
        _completionSuffix = suffix;
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
    /// Builds a complete frame into <see cref="_buffer"/> then calls
    /// <see cref="ConsoleBuffer.Render"/> to emit only the cells that changed
    /// since the previous frame.
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
            BuildFrame(width, height);
            _buffer.Render(Console.Out);
        }
    }

    /// <summary>
    /// Core layout engine: writes a frame for the given terminal size into
    /// <see cref="_buffer"/>. Called by <see cref="Redraw"/>, which then calls
    /// <see cref="ConsoleBuffer.Render"/> to flush changed cells to the terminal.
    ///
    /// When the sidebar is visible, the terminal is split into two full-height
    /// columns: left (conversation + input + status) and right (sidebar).
    /// A thin │ separator in BrightBlack divides them. When the sidebar is hidden,
    /// the left column uses the full terminal width.
    ///
    /// If the terminal dimensions changed since the last frame, the buffer is
    /// resized first (which resets the front buffer, causing a full repaint).
    ///
    /// Each region is rendered by a dedicated private method so the overall
    /// layout sequence is immediately visible here.
    /// </summary>
    // internal for testability — Ur.Tests has InternalsVisibleTo access.
    internal void BuildFrame(int width, int height)
    {
        // Resize the buffer when terminal dimensions change. Resize also resets
        // both front and back buffers, which forces a full repaint — correct
        // behaviour after a resize since the layout has shifted.
        if (width != _lastWidth || height != _lastHeight)
        {
            _buffer.Resize(width, height);
            _lastWidth  = width;
            _lastHeight = height;
        }

        // Clear the back buffer for this frame so stale content from the
        // previous frame does not bleed through on rows we do not write.
        _buffer.Clear();

        var viewportHeight = height - InputAreaRows - HeaderRows;

        // Compute sidebar allocation. The sidebar gets up to 1/3 of the terminal
        // width (capped at MaxSidebarWidth), plus one column for the separator.
        int leftWidth;
        int sidebarWidth;
        if (_sidebar is not null && _sidebar.IsVisible)
        {
            sidebarWidth = Math.Min(MaxSidebarWidth, width / 3);
            var separatorWidth = 1;
            leftWidth = width - sidebarWidth - separatorWidth;
        }
        else
        {
            leftWidth    = width;
            sidebarWidth = 0;
        }

        RenderConversation(leftWidth, viewportHeight);
        RenderInputArea(leftWidth, viewportHeight);
        RenderStatusBar(leftWidth, viewportHeight);

        if (sidebarWidth > 0)
            RenderSidebar(width, height, leftWidth, sidebarWidth);
    }

    // --- Private helpers ---

    /// <summary>
    /// Writes a CellRow into the buffer at the given 0-based row index.
    /// ConsoleBuffer uses (x=col, y=row) — the opposite of the old ScreenBuffer's
    /// (row, col) order. Cells beyond the buffer width are truncated; columns not
    /// covered by the row are left as Cell.Empty (already set by Clear() above).
    /// </summary>
    private void WriteRow(int row, CellRow cellRow)
    {
        var cells = cellRow.Cells;
        for (var col = 0; col < cells.Count; col++)
            _buffer.SetCell(col, row, cells[col]);
    }

    /// <summary>
    /// Renders the conversation region: tail-clips the EventList rows to fit
    /// between the header and input area. When the conversation is empty (before
    /// the first user message), renders the splash art centered instead.
    /// </summary>
    private void RenderConversation(int width, int viewportHeight)
    {
        var allRows = _root.Render(width);

        if (allRows.Count == 0)
        {
            RenderSplash(width, viewportHeight);
            return;
        }

        var startIndex = Math.Max(0, allRows.Count - viewportHeight);

        for (var bufRow = 0; bufRow < viewportHeight; bufRow++)
        {
            var rowIndex = startIndex + bufRow;
            if (rowIndex < allRows.Count)
                WriteRow(HeaderRows + bufRow, allRows[rowIndex]);
            // Rows beyond the content stay Cell.Empty (blank / default colors).
        }
    }

    /// <summary>
    /// Renders the splash art horizontally and vertically centered within the
    /// conversation region. Shown only when the EventList is empty.
    /// </summary>
    private void RenderSplash(int width, int viewportHeight)
    {
        var artWidth  = SplashLines.Max(l => l.Length);
        var startRow  = HeaderRows + Math.Max(0, (viewportHeight - SplashLines.Length) / 2);
        var startCol  = Math.Max(0, (width - artWidth) / 2);

        for (var i = 0; i < SplashLines.Length; i++)
        {
            var row = new CellRow();
            for (var p = 0; p < startCol; p++)
                row.Append(' ', Color.Default, Color.Default);
            row.Append(SplashLines[i], Color.BrightBlack, Color.Default);
            WriteRow(startRow + i, row);
        }
    }

    /// <summary>
    /// Renders the input area: top rule, text row (prompt + cursor), and bottom rule.
    /// </summary>
    private void RenderInputArea(int width, int viewportHeight)
    {
        // Top rule: heavy horizontal line spanning the full width.
        var topRule = CellRow.FromText(new string(TopRuleChar, width), Color.BrightBlack, Color.Default);
        WriteRow(viewportHeight + HeaderRows, topRule);

        // Text row: chevron in bright black, typed text in white, no background.
        // The system cursor is hidden; we paint our own block cursor using
        // TextDecoration.Reverse so it's visible against the default background.
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

        // Ghost-text rendering: when a completion suffix is active and the
        // input is accepting keystrokes, show the suggestion in gray. The first
        // ghost character is rendered with TextDecoration.Reverse so the block
        // cursor appears to "sit on" it — this makes the suggestion feel interactive.
        // Remaining ghost characters are plain BrightBlack (dim gray).
        // When no ghost text is active, fall back to the standard blank
        // reverse-video cursor cell.
        if (!_turnRunning)
        {
            if (!string.IsNullOrEmpty(_completionSuffix))
            {
                textRow.Append(_completionSuffix[0], Color.BrightBlack, Color.Default, TextDecoration.Reverse);
                if (_completionSuffix.Length > 1)
                    textRow.Append(_completionSuffix[1..], Color.BrightBlack, Color.Default);
            }
            else
            {
                textRow.Append(' ', Color.Default, Color.Default, TextDecoration.Reverse);
            }
        }

        WriteRow(viewportHeight + HeaderRows + 1, textRow);

        // Bottom rule: light horizontal line spanning the full width.
        var bottomRule = CellRow.FromText(new string(BottomRuleChar, width), Color.BrightBlack, Color.Default);
        WriteRow(viewportHeight + HeaderRows + 2, bottomRule);
    }

    /// <summary>
    /// Renders the status bar: animated throbber on the left (only while a turn
    /// is running) and model ID right-aligned.
    /// </summary>
    private void RenderStatusBar(int width, int viewportHeight)
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

        WriteRow(viewportHeight + HeaderRows + 3, statusRow);
    }

    /// <summary>
    /// Renders the sidebar into the right columns of the buffer. Draws a thin
    /// vertical separator (│ in BrightBlack) in the separator column for every row,
    /// then renders the sidebar's content top-aligned.
    /// </summary>
    private void RenderSidebar(int totalWidth, int height, int leftWidth, int sidebarWidth)
    {
        var separatorCol   = leftWidth;
        // One column for the separator, one for padding — content starts 2 cols in.
        var sidebarStartCol = leftWidth + 2;
        var contentWidth   = sidebarWidth - 1; // account for the padding column

        // Draw the vertical separator for every row of the terminal.
        for (var row = 0; row < height; row++)
            _buffer.SetCell(separatorCol, row, new Cell(SeparatorChar, Color.BrightBlack, Color.Default));

        // Render sidebar content top-aligned with a 1-column pad after the separator.
        var sidebarRows = _sidebar!.Render(contentWidth);
        for (var ri = 0; ri < sidebarRows.Count && ri < height; ri++)
        {
            var cells = sidebarRows[ri].Cells;
            for (var ci = 0; ci < cells.Count && sidebarStartCol + ci < totalWidth; ci++)
                _buffer.SetCell(sidebarStartCol + ci, ri, cells[ci]);
        }
    }

    public void Dispose()
    {
        _redrawTimer.Dispose();
        Stop();
    }
}
