using Terminal.Gui.App;
using Terminal.Gui.Drawing;
using Terminal.Gui.Drivers;
using Terminal.Gui.Input;
using Terminal.Gui.ViewBase;
using Terminal.Gui.Views;
using Attribute = Terminal.Gui.Drawing.Attribute;

namespace Ox.Views;

/// <summary>
/// The chat composer panel at the bottom of the screen. Draws a rounded border
/// containing a real TextField for input, a divider, and a status line (throbber + model ID).
///
/// The TextField handles all standard text editing (backspace, cursor movement,
/// word navigation, clipboard, etc.) natively. Special keys (Enter to submit,
/// Ctrl+C/Ctrl+D for EOF, Tab for autocomplete) are intercepted via KeyDown.
///
/// ReadLineAsync bridges the event-driven TextField to async callers (REPL loop,
/// permission prompts).
/// </summary>
internal sealed class InputAreaView : View
{
    // Rounded box-drawing characters for the composer panel border.
    private const char TopLeftCorner = '╭';
    private const char TopRightCorner = '╮';
    private const char BottomLeftCorner = '╰';
    private const char BottomRightCorner = '╯';
    private const char HorizontalBorder = '─';
    private const char VerticalBorder = '│';

    // Colors matching the original Viewport design.
    private static readonly Color Bg = new(ColorName16.Black);
    private static readonly Color BorderColor = new(244, 244, 244);     // Grey50-ish
    private static readonly Color DividerColor = new(68, 68, 68);       // Darker gray
    private static readonly Color ModelColor = new(178, 178, 178);      // Grey50

    // Throbber animation: 8-bit counter rendered as ● glyphs.
    private const int ThrobberCount = 8;
    private const int ThrobberFrameMs = 1000;
    private const char ThrobberRune = '●';

    private readonly IApplication _app;
    private readonly TextField _textField;

    // Prompt prefix displayed before the text field (e.g. for permission prompts).
    private string _promptPrefix = "";

    // Autocomplete engine for slash-command ghost text.
    private AutocompleteEngine? _autocomplete;

    // The TCS that ReadLineAsync awaits. Completed when the user presses Enter or EOF.
    private TaskCompletionSource<string?>? _inputTcs;

    // Callback for autocomplete ghost text updates.
    private Action<string?>? _onCompletionChanged;

    private bool _turnRunning;
    private string? _modelId;
    private long _turnStartedAtTickMs = -1;
    private int _lastAnimatedCounter = -1;
    private object? _throbberToken;

    public InputAreaView(IApplication app)
    {
        _app = app;

        // The InputAreaView itself is focusable so the TextField inside can receive focus.
        CanFocus = true;

        // Create the text field, positioned inside our custom border chrome.
        // Row 0 = top border, row 1 = text field row. Left border + padding = 2 cols.
        _textField = new TextField
        {
            X = 2,
            Y = 1,
            // Width fills the content area: total width minus border+padding on each side (4 cols).
            Width = Dim.Fill(Dim.Absolute(2)),
            Height = 1,
            BorderStyle = LineStyle.None,
            CanFocus = true,
        };


        // Intercept special keys before the TextField processes them.
        _textField.KeyDown += OnTextFieldKeyDown;

        // Track text changes for autocomplete ghost text.
        _textField.ValueChanged += (_, _) =>
        {
            var suffix = _autocomplete?.GetCompletion(_textField.Text ?? "");
            _onCompletionChanged?.Invoke(suffix);
            SetNeedsDraw();
        };

        Add(_textField);
    }

    // --- Public API ---

    /// <summary>
    /// Sets the autocomplete engine for slash-command ghost text.
    /// </summary>
    public void SetAutocomplete(AutocompleteEngine engine)
    {
        _autocomplete = engine;
    }

    /// <summary>
    /// Updates the prompt prefix text displayed before the text field.
    /// Used for permission prompts like "Allow 'bash' to run 'ls'? (y/n): ".
    /// </summary>
    public void SetPrompt(string prompt)
    {
        _promptPrefix = prompt;
        SetNeedsDraw();
    }

    /// <summary>
    /// Sets the ghost-text completion suffix shown after the cursor.
    /// Pass null to clear.
    /// </summary>
    public void SetCompletion(string? suffix)
    {
        // Ghost text rendering is no longer done here — the autocomplete suffix
        // is tracked for tab-completion but visual ghost text has been removed.
        SetNeedsDraw();
    }

    /// <summary>
    /// Reads a line of user input. Returns the submitted text on Enter,
    /// or null on EOF (Ctrl+C/Ctrl+D) or cancellation.
    ///
    /// The promptPrefix is displayed before the text field (used for permission
    /// prompts). The onCompletionChanged callback is invoked when autocomplete
    /// ghost text changes.
    /// </summary>
    public async Task<string?> ReadLineAsync(
        string promptPrefix,
        Action<string?>? onCompletionChanged = null,
        CancellationToken ct = default)
    {
        _promptPrefix = promptPrefix;
        _onCompletionChanged = onCompletionChanged;
        _inputTcs = new TaskCompletionSource<string?>();

        // Clear the text field for new input.
        _textField.Text = "";
        _textField.SetFocus();
        onCompletionChanged?.Invoke(null);
        SetNeedsDraw();

        // Register cancellation.
        await using var reg = ct.Register(() =>
        {
            _app.Invoke(() => _inputTcs?.TrySetResult(null));
        });

        return await _inputTcs.Task;
    }

    /// <summary>
    /// Shows or hides the throbber on the status line. Starts/stops the
    /// animation timer.
    /// </summary>
    public void SetTurnRunning(bool running)
    {
        if (running && !_turnRunning)
        {
            _turnStartedAtTickMs = Environment.TickCount64;
            _lastAnimatedCounter = -1;
            // Start the throbber animation timer at 1-second intervals.
            _throbberToken = _app.AddTimeout(TimeSpan.FromSeconds(1), () =>
            {
                if (!_turnRunning) return false;
                var counter = GetCurrentThrobberCounter();
                if (counter != _lastAnimatedCounter)
                {
                    _lastAnimatedCounter = counter;
                    SetNeedsDraw();
                }
                return true; // Keep repeating
            });
        }
        else if (!running && _turnRunning)
        {
            _turnStartedAtTickMs = -1;
            _lastAnimatedCounter = -1;
            if (_throbberToken is not null)
            {
                _app.RemoveTimeout(_throbberToken);
                _throbberToken = null;
            }
        }

        _turnRunning = running;
        SetNeedsDraw();
    }

    /// <summary>Sets the model identifier displayed right-aligned on the status line.</summary>
    public void SetModelId(string? modelId)
    {
        _modelId = modelId;
        SetNeedsDraw();
    }

    // --- Key handling ---

    /// <summary>
    /// Intercepts Enter, Tab, Ctrl+C, Ctrl+D, and Escape before the TextField
    /// processes them. All other keys (backspace, arrows, etc.) pass through
    /// to the TextField's native handling.
    /// </summary>
    private void OnTextFieldKeyDown(object? sender, Key key)
    {
        // If no input TCS is active, we're not reading input.
        if (_inputTcs is null || _inputTcs.Task.IsCompleted)
            return;

        var keyCode = key.KeyCode;

        // Enter: submit the current text.
        if (keyCode == KeyCode.Enter)
        {
            _onCompletionChanged?.Invoke(null);
            var result = _textField.Text ?? "";
            _inputTcs.TrySetResult(result);
            key.Handled = true;
            return;
        }

        // Tab: accept autocomplete suggestion.
        if (keyCode == KeyCode.Tab)
        {
            var suffix = _autocomplete?.GetCompletion(_textField.Text ?? "");
            if (suffix is not null)
            {
                _textField.Text = (_textField.Text ?? "") + suffix;
                _textField.MoveEnd();
                _onCompletionChanged?.Invoke(null);
            }
            key.Handled = true;
            return;
        }

        // Ctrl+C: EOF signal.
        if (keyCode == (KeyCode.C | KeyCode.CtrlMask))
        {
            _onCompletionChanged?.Invoke(null);
            _inputTcs.TrySetResult(null);
            key.Handled = true;
            return;
        }

        // Ctrl+D on empty buffer: EOF.
        if (keyCode == (KeyCode.D | KeyCode.CtrlMask) && string.IsNullOrEmpty(_textField.Text))
        {
            _inputTcs.TrySetResult(null);
            key.Handled = true;
            return;
        }

        // Escape: no-op during input (escape cancellation is turn-level).
        if (keyCode == KeyCode.Esc)
        {
            key.Handled = true;
            return;
        }
    }

    // --- Drawing ---

    /// <inheritdoc/>
    protected override bool OnDrawingContent(DrawContext? context)
    {
        var width = Frame.Width;
        var height = Frame.Height;
        if (width <= 0 || height < 5)
            return true;

        // Row 0: top border
        DrawBorderRow(0, width, TopLeftCorner, TopRightCorner, BorderColor);

        // Row 1: the TextField draws itself — we just draw the border edges.
        DrawTextFieldBorderEdges(1, width);

        // Row 2: divider
        DrawDividerRow(2, width);

        // Row 3: status line (throbber + model ID)
        DrawStatusRow(3, width);

        // Row 4: bottom border
        DrawBorderRow(4, width, BottomLeftCorner, BottomRightCorner, BorderColor);

        return true;
    }

    /// <summary>Draws a horizontal border row with corner characters.</summary>
    private void DrawBorderRow(int row, int width, char leftCorner, char rightCorner, Color color)
    {
        Move(0, row);
        var attr = new Attribute(color, Bg);
        SetAttribute(attr);

        AddRune(leftCorner);
        for (var i = 1; i < width - 1; i++)
            AddRune(HorizontalBorder);
        if (width > 1)
            AddRune(rightCorner);
    }

    /// <summary>
    /// Draws just the left and right border edges for the text field row.
    /// The TextField itself handles the content area.
    /// </summary>
    private void DrawTextFieldBorderEdges(int row, int width)
    {
        // Left border + padding
        Move(0, row);
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);
        AddRune(' ');

        // Right padding + border (drawn after the TextField's content area)
        Move(width - 2, row);
        AddRune(' ');
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);
    }

    /// <summary>Draws the internal divider between input and status rows.</summary>
    private void DrawDividerRow(int row, int width)
    {
        Move(0, row);
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);

        SetAttribute(new Attribute(DividerColor, Bg));
        for (var i = 1; i < width - 1; i++)
            AddRune(HorizontalBorder);

        SetAttribute(new Attribute(BorderColor, Bg));
        if (width > 1)
            AddRune(VerticalBorder);
    }

    /// <summary>
    /// Draws the status line: throbber on the left, model ID on the right.
    /// </summary>
    private void DrawStatusRow(int row, int width)
    {
        if (width <= 4) return;

        // Left border + padding
        Move(0, row);
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);
        AddRune(' ');

        var contentWidth = Math.Max(0, width - 4);
        var usedWidth = 0;

        // Throbber animation
        if (_turnRunning)
        {
            var counter = GetCurrentThrobberCounter();
            for (var i = 0; i < ThrobberCount && usedWidth < contentWidth; i++)
            {
                var bitIndex = ThrobberCount - 1 - i;
                var isOn = (counter & (1 << bitIndex)) != 0;
                var throbberColor = isOn
                    ? new Color(ColorName16.White)
                    : new Color(ColorName16.DarkGray);
                SetAttribute(new Attribute(throbberColor, Bg));
                AddRune(ThrobberRune);
                usedWidth++;

                // Space between bits (except after the last one)
                if (i < ThrobberCount - 1 && usedWidth < contentWidth)
                {
                    SetAttribute(new Attribute(Color.None, Bg));
                    AddRune(' ');
                    usedWidth++;
                }
            }
        }

        // Model ID right-aligned
        if (_modelId is not null)
        {
            var modelLen = Math.Min(_modelId.Length, contentWidth);
            var modelStart = Math.Max(usedWidth, contentWidth - modelLen);
            var spacing = modelStart - usedWidth;

            SetAttribute(new Attribute(Color.None, Bg));
            for (var i = 0; i < spacing; i++)
                AddRune(' ');

            SetAttribute(new Attribute(ModelColor, Bg));
            AddStr(_modelId[..modelLen]);
            usedWidth = modelStart + modelLen;
        }

        // Fill remaining space
        SetAttribute(new Attribute(Color.None, Bg));
        while (usedWidth < contentWidth)
        {
            AddRune(' ');
            usedWidth++;
        }

        // Right padding + border
        AddRune(' ');
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);
    }

    // --- Throbber helpers ---

    /// <summary>
    /// Converts turn-relative elapsed time into the visible 8-bit counter value.
    /// Starts at 1 so a new turn immediately looks active.
    /// </summary>
    internal static int ComputeThrobberCounter(long elapsedMs)
    {
        var normalized = Math.Max(0, elapsedMs);
        return (int)(((normalized / ThrobberFrameMs) + 1) % (1 << ThrobberCount));
    }

    private int GetCurrentThrobberCounter()
    {
        if (_turnStartedAtTickMs < 0)
            return ComputeThrobberCounter(0);

        var elapsed = Math.Max(0L, Environment.TickCount64 - _turnStartedAtTickMs);
        return ComputeThrobberCounter(elapsed);
    }
}
