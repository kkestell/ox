using Te.Rendering;

namespace Ox.Rendering;

/// <summary>
/// Full-screen display engine that renders the conversation into the alternate
/// screen buffer.
///
/// Layout (1-based terminal rows, 0-based buffer rows):
///   Rows 1 .. (height-6) — conversation viewport (shows the tail of all rows)
///   Row height-5          — blank spacer between the transcript and composer
///   Row height-4          — top border of the composer panel
///   Row height-3          — input text row
///   Row height-2          — spacer row between input and status
///   Row height-1          — status line (throbber left, model ID right)
///   Row height            — bottom border of the composer panel
///
/// Rendering is decoupled from event arrival via a dirty flag. When any renderable
/// fires <see cref="IRenderable.Changed"/>, we set a flag rather than redrawing
/// immediately. The main loop calls <see cref="RedrawIfDirty"/> at explicit points
/// (after processing each input key, after each streamed event). During turns a
/// per-turn Timer calls <see cref="ThrobberTick"/> at 1-second intervals to
/// advance the status-line animation, while a lightweight resize timer handles
/// terminal-size changes that arrive while input is blocked.
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

    // Current input row text. The input area no longer owns a built-in prompt
    // glyph, which keeps the editable column stable and lets the cursor sit at
    // the same x-position regardless of turn lifecycle.
    private string _inputPrompt = "";

    // Ghost-text completion suffix currently visible in the input row.
    // Null when no completion is active. Set by InputReader via the
    // onCompletionChanged callback, rendered by RenderInputArea.
    private string? _completionSuffix;

    private readonly Func<long> _tickCountProvider;

    // Guards Redraw() against concurrent calls from the timer and an explicit
    // caller (e.g. after SetInputPrompt is called mid-turn).
    private readonly object _redrawLock = new();

    // Background timer that polls terminal dimensions every 250ms and sets the
    // dirty flag when a resize is detected. This is the only way to notice a
    // resize while the main loop is blocked waiting for input (SIGWINCH is not
    // exposed by .NET's Console API on all platforms).
    private Timer? _resizeTimer;

    // True between Start() and Stop() — prevents stray redraws after shutdown.
    private bool _running;

    // Status line state: the throbber animates while a turn is running, and the
    // model ID is displayed right-aligned on the second content row of the
    // composer panel.
    private volatile bool _turnRunning;
    private string? _modelId;
    private long _turnStartedAtTickMs = -1;
    private int _lastAnimatedThrobberCounter = -1;

    // Throbber animation: eight fixed sun glyphs that visualize an 8-bit counter.
    // We leave a blank cell between bits so the binary pattern is easier to scan
    // in a monospace terminal status line.
    // Each second advances the counter by one step, which keeps the animation
    // deterministic and stable within that second so the diff-based buffer only
    // rewrites the row when the visible state actually changes.
    private const int ThrobberCount = 8;
    private const int ThrobberFrameMs = 1000;
    private const char ThrobberRune = '●';

    // ASCII art displayed centered in the conversation area before the user
    // sends their first message. Each line is 12 columns wide.
    private static readonly string[] SplashLines =
    [
        "▒█▀▀▀█ ▀▄▒▄▀",
        "▒█░░▒█ ░▒█░░",
        "▒█▄▄▄█ ▄▀▒▀▄"
    ];

    private static readonly Color ModelForeground = Color.FromIndex(244); // Grey50
    private static readonly Color FooterBorderForeground = Color.FromIndex(244); // Grey50
    // xterm grayscale index 238 reads darker than BrightBlack in most themes,
    // which keeps the divider subdued relative to the white border.
    private static readonly Color FooterDividerForeground = Color.FromIndex(238);

    // Rounded box-drawing characters make the footer read as a single
    // lightweight panel instead of a band of filled rows, which is closer to
    // the chat-composer look the UI is moving toward.
    private const char TopLeftCornerChar = '╭';
    private const char TopRightCornerChar = '╮';
    private const char BottomLeftCornerChar = '╰';
    private const char BottomRightCornerChar = '╯';
    private const char HorizontalBorderChar = '─';
    private const char VerticalBorderChar = '│';

    // Number of terminal rows reserved for the input area:
    // top border + text row + spacer row + status row + bottom border.
    private const int InputAreaRows = 5;

    // Reserve one blank row above the composer so the transcript and footer do
    // not visually collide when the event list reaches the bottom of the screen.
    private const int ComposerGapRows = 1;

    // No header rows — session ID and context usage have been moved to the sidebar.
    private const int HeaderRows = 0;

    // Maximum sidebar width in columns. Capped to prevent the sidebar from
    // dominating narrow terminals.
    private const int MaxSidebarWidth = 36;

    // Box-drawing character for the vertical separator between the left column
    // and the sidebar.
    private const char SeparatorChar = '│'; // U+2502 BOX DRAWINGS LIGHT VERTICAL

    public Viewport(EventList root, Sidebar? sidebar = null)
        : this(root, sidebar, static () => Environment.TickCount64)
    {
    }

    internal Viewport(EventList root, Sidebar? sidebar, Func<long> tickCountProvider)
    {
        _root    = root;
        _sidebar = sidebar;
        _tickCountProvider = tickCountProvider;

        // Bootstrap the buffer at the current terminal size. Resize() is called
        // at the start of each BuildFrame when dimensions change, so this initial
        // size only needs to be a reasonable default, not exact.
        var (initialWidth, initialHeight) = Terminal.GetSize();
        _buffer      = new ConsoleBuffer(initialWidth, initialHeight);
        _lastWidth   = initialWidth;
        _lastHeight  = initialHeight;

        // Subscribe to the root's Changed event so any descendant mutation marks
        // us dirty. The main loop calls RedrawIfDirty at explicit points to flush.
        _root.Changed += () => _dirty = true;

        // Subscribe to sidebar changes so todo updates trigger a redraw.
        if (_sidebar is not null)
            _sidebar.Changed += () => _dirty = true;
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

        // Start polling for terminal resize. The callback runs on a ThreadPool
        // thread, so it acquires _redrawLock inside Redraw() to stay safe.
        _resizeTimer = new Timer(_ => CheckForResize(), null, 250, 250);
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
        _resizeTimer?.Dispose();
        _resizeTimer = null;

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
    /// Updates the input row text and triggers an immediate redraw.
    /// The caller owns the entire visible string so permission prompts and the
    /// main chat composer can share the same footer row without separate chrome.
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
        if (running && !_turnRunning)
        {
            // The throbber is turn-scoped, not process-scoped. Capture the
            // start tick once so every new turn begins at 00000001 instead of
            // inheriting an arbitrary value from process uptime.
            Interlocked.Exchange(ref _turnStartedAtTickMs, _tickCountProvider());
            _lastAnimatedThrobberCounter = -1;
        }
        else if (!running)
        {
            Interlocked.Exchange(ref _turnStartedAtTickMs, -1);
            _lastAnimatedThrobberCounter = -1;
        }

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
    /// Polls the terminal dimensions and sets the dirty flag if they changed.
    /// Called by <see cref="_resizeTimer"/> on a ThreadPool thread every 250ms.
    /// This is how we detect SIGWINCH-style resize events — .NET doesn't
    /// expose the signal directly on all platforms, so we poll instead.
    /// </summary>
    private void CheckForResize()
    {
        var (w, h) = Terminal.GetSize();
        if (w != _lastWidth || h != _lastHeight)
        {
            _dirty = true;
            Redraw();
        }
    }

    /// <summary>
    /// Checks the dirty flag and redraws if needed. Called by the main loop
    /// after processing input keys and after routing each streamed event.
    /// </summary>
    public void RedrawIfDirty()
    {
        if (_dirty && _running)
            Redraw();
    }

    /// <summary>
    /// Advances the throbber animation by one tick. Called by the per-turn
    /// Timer (1-second period) on a ThreadPool thread. Only redraws if the
    /// visible counter value actually changed, so the diff-based buffer
    /// doesn't waste cycles on identical frames.
    /// </summary>
    public void ThrobberTick()
    {
        if (!_turnRunning) return;
        var counter = GetCurrentThrobberCounter();
        if (counter != _lastAnimatedThrobberCounter)
        {
            _lastAnimatedThrobberCounter = counter;
            Redraw(); // Acquires _redrawLock inside
        }
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
        // We clear the screen first so that old content outside the new
        // dimensions doesn't linger (the diff-based renderer only writes cells
        // within the new buffer bounds).
        if (width != _lastWidth || height != _lastHeight)
        {
            Terminal.ClearScreen();
            _buffer.Resize(width, height);
            _lastWidth  = width;
            _lastHeight = height;
        }

        // Clear the back buffer for this frame so stale content from the
        // previous frame does not bleed through on rows we do not write.
        _buffer.Clear();

        var viewportHeight = height - InputAreaRows - ComposerGapRows - HeaderRows;

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
        WriteRow(row, 0, cellRow);
    }

    /// <summary>
    /// Writes a CellRow into the buffer at the given 0-based row index and
    /// starting column. The composer panel uses this to center itself without
    /// leaking offset logic into unrelated renderers.
    /// </summary>
    private void WriteRow(int row, int startCol, CellRow cellRow)
    {
        var cells = cellRow.Cells;
        for (var col = 0; col < cells.Count; col++)
            _buffer.SetCell(startCol + col, row, cells[col]);
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
    /// Renders the chat composer shell: a rounded white border with the prompt
    /// and status rows separated by a thin divider.
    /// </summary>
    private void RenderInputArea(int width, int viewportHeight)
    {
        var panelWidth = width;
        var panelStartColumn = 0;
        var panelTopRowIndex = viewportHeight + HeaderRows + ComposerGapRows;
        var textRowIndex = panelTopRowIndex + 1;
        var spacerRowIndex = panelTopRowIndex + 2;
        var statusRowIndex = panelTopRowIndex + 3;
        var panelBottomRowIndex = panelTopRowIndex + 4;

        WritePanelBorderRow(panelTopRowIndex, panelStartColumn, panelWidth, TopLeftCornerChar, TopRightCornerChar);

        // Text row: white input text on the terminal's default background.
        // The system cursor is hidden; we still paint our own cursor so the
        // composer remains editable-looking even while the model is running.
        WritePanelInteriorRow(textRowIndex, panelStartColumn, panelWidth, (row, contentWidth) =>
        {
            AppendClipped(row, _inputPrompt, Color.White, Color.Default, contentWidth);
            var remainingWidth = Math.Max(0, contentWidth - _inputPrompt.Length);

            // Ghost-text rendering: show the suggestion in gray whenever one is
            // active. We intentionally keep the cursor path identical while a
            // turn is running so the footer never flips into a separate
            // "disabled" state; the input row remains a composer, not a status
            // indicator.
            if (!string.IsNullOrEmpty(_completionSuffix) && remainingWidth > 0)
            {
                row.Append(_completionSuffix[0], Color.BrightBlack, Color.Default, TextDecoration.Reverse);
                if (_completionSuffix.Length > 1 && remainingWidth > 1)
                    AppendClipped(row, _completionSuffix[1..], Color.BrightBlack, Color.Default, remainingWidth - 1);
            }
            else if (remainingWidth > 0)
            {
                row.Append(' ', Color.White, Color.Default, TextDecoration.Reverse);
            }
        });

        WritePanelDividerRow(spacerRowIndex, panelStartColumn, panelWidth);

        WritePanelInteriorRow(statusRowIndex, panelStartColumn, panelWidth, (row, contentWidth) =>
        {
            if (contentWidth <= 0)
                return;

            var usedWidth = 0;

            if (_turnRunning)
            {
                foreach (var cell in BuildThrobberCells(GetCurrentThrobberCounter()))
                {
                    if (usedWidth >= contentWidth)
                        break;

                    row.Append(cell.Rune, cell.Foreground, Color.Default, cell.Decorations);
                    usedWidth++;
                }
            }

            if (_modelId is not null)
            {
                var modelTextWidth = Math.Min(_modelId.Length, contentWidth);
                var modelStartColumn = Math.Max(usedWidth, contentWidth - modelTextWidth);
                var spacingWidth = modelStartColumn - usedWidth;

                for (var i = 0; i < spacingWidth; i++)
                    row.Append(' ', Color.Default, Color.Default);

                AppendClipped(row, _modelId, ModelForeground, Color.Default, modelTextWidth);
            }
        });

        WritePanelBorderRow(panelBottomRowIndex, panelStartColumn, panelWidth, BottomLeftCornerChar, BottomRightCornerChar);
    }

    /// <summary>
    /// Converts turn-relative elapsed time into the visible 8-bit counter value.
    /// We start at 1 on the first rendered frame so a newly-started turn
    /// immediately looks active instead of presenting an all-off row.
    /// </summary>
    internal static int ComputeThrobberCounter(long elapsedMs)
    {
        var normalizedElapsedMs = Math.Max(0, elapsedMs);
        return (int)(((normalizedElapsedMs / ThrobberFrameMs) + 1) % (1 << ThrobberCount));
    }

    /// <summary>
    /// Builds the status-line throbber cells for the supplied counter value.
    /// Keeping the cell mapping separate from the time math makes the display
    /// logic easier to verify in unit tests.
    /// </summary>
    internal static IReadOnlyList<Cell> BuildThrobberCells(int counter)
    {
        var cells = new Cell[(ThrobberCount * 2) - 1];

        // Render the most-significant bit first so the row reads like a normal
        // binary number instead of a reversed low-bit diagnostic pattern.
        for (var i = 0; i < ThrobberCount; i++)
        {
            var bitIndex = ThrobberCount - 1 - i;
            var isOn = (counter & (1 << bitIndex)) != 0;
            cells[i * 2] = new Cell(
                ThrobberRune,
                isOn ? Color.White : Color.BrightBlack,
                Color.Default);

            if (i < ThrobberCount - 1)
                cells[(i * 2) + 1] = Cell.Empty;
        }

        return cells;
    }

    /// <summary>
    /// Reads the current monotonic clock and converts it into the turn-local
    /// counter value used by the status-line throbber.
    /// </summary>
    private int GetCurrentThrobberCounter()
    {
        var startedAtTickMs = Interlocked.Read(ref _turnStartedAtTickMs);
        if (startedAtTickMs < 0)
            return ComputeThrobberCounter(0);

        var elapsedMs = Math.Max(0L, _tickCountProvider() - startedAtTickMs);
        return ComputeThrobberCounter(elapsedMs);
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

    /// <summary>
    /// Appends text while respecting the row's remaining content budget. The
    /// footer panel uses this to keep long prompts and model IDs inside the
    /// rounded border instead of letting them bleed into it.
    /// </summary>
    private static void AppendClipped(CellRow row, string text, Color foreground, Color background, int width)
    {
        if (width <= 0 || string.IsNullOrEmpty(text))
            return;

        var clippedLength = Math.Min(width, text.Length);
        row.Append(text[..clippedLength], foreground, background);
    }

    /// <summary>
    /// Writes the top or bottom border of the composer panel. The border is
    /// deliberately white and uses rounded corners so it stands apart from the
    /// conversation without reintroducing filled background bands.
    /// </summary>
    private void WritePanelBorderRow(int rowIndex, int startCol, int width, char leftCorner, char rightCorner)
    {
        if (width <= 0)
            return;

        var row = new CellRow();
        if (width == 1)
        {
            row.Append(leftCorner, FooterBorderForeground, Color.Default);
            WriteRow(rowIndex, startCol, row);
            return;
        }

        row.Append(leftCorner, FooterBorderForeground, Color.Default);
        for (var i = 0; i < width - 2; i++)
            row.Append(HorizontalBorderChar, FooterBorderForeground, Color.Default);
        row.Append(rightCorner, FooterBorderForeground, Color.Default);
        WriteRow(rowIndex, startCol, row);
    }

    /// <summary>
    /// Writes the internal divider between the prompt row and the status row.
    /// The ends remain plain border rails while the run of dashes is darker
    /// gray so the separator reads as structure, not chrome.
    /// </summary>
    private void WritePanelDividerRow(int rowIndex, int startCol, int width)
    {
        if (width <= 0)
            return;

        var row = new CellRow();
        if (width == 1)
        {
            row.Append(VerticalBorderChar, FooterBorderForeground, Color.Default);
            WriteRow(rowIndex, startCol, row);
            return;
        }

        row.Append(VerticalBorderChar, FooterBorderForeground, Color.Default);
        for (var i = 0; i < width - 2; i++)
            row.Append(HorizontalBorderChar, FooterDividerForeground, Color.Default);
        row.Append(VerticalBorderChar, FooterBorderForeground, Color.Default);
        WriteRow(rowIndex, startCol, row);
    }

    /// <summary>
    /// Writes one row inside the composer panel, including the vertical border
    /// and the fixed one-cell inner padding on both sides.
    /// </summary>
    private void WritePanelInteriorRow(int rowIndex, int startCol, int width, Action<CellRow, int> writeContent)
    {
        if (width <= 0)
            return;

        var row = new CellRow();
        if (width == 1)
        {
            row.Append(VerticalBorderChar, FooterBorderForeground, Color.Default);
            WriteRow(rowIndex, startCol, row);
            return;
        }

        if (width == 2)
        {
            row.Append(VerticalBorderChar, FooterBorderForeground, Color.Default);
            row.Append(VerticalBorderChar, FooterBorderForeground, Color.Default);
            WriteRow(rowIndex, startCol, row);
            return;
        }

        row.Append(VerticalBorderChar, FooterBorderForeground, Color.Default);
        row.Append(' ', Color.Default, Color.Default);

        var contentWidth = Math.Max(0, width - 4);
        writeContent(row, contentWidth);

        while (row.Cells.Count < width - 1)
            row.Append(' ', Color.Default, Color.Default);

        row.Append(VerticalBorderChar, FooterBorderForeground, Color.Default);
        WriteRow(rowIndex, startCol, row);
    }

    public void Dispose() => Stop();
}
