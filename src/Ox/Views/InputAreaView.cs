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
/// containing a real TextField for input, a divider, and a status line
/// (throbber + context/model summary).
///
/// The view is a pure widget: it stays alive all the time, emits user intents
/// upward through the ComposerController, and never owns async read lifecycles.
/// Permission prompts are handled by the separate PermissionPromptView — this
/// view is always in "chat mode".
///
/// Enter is handled via the Terminal.Gui accept path (Accepting event, triggered
/// by the framework's Enter → Command.Accept key binding). All other special keys
/// (Tab, Ctrl+C, Ctrl+D, Escape) are intercepted in KeyDown.
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

    // Autocomplete engine for slash-command ghost text.
    private AutocompleteEngine? _autocomplete;

    // The controller is bound after construction so OxApp can wire everything
    // together before the event loop starts. Null-safe throughout: the view
    // silently no-ops on submission if no controller is bound yet.
    private ComposerController? _controller;

    private bool _turnRunning;
    private string? _modelId;
    private int? _contextUsagePercent;
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

        // Enter submission is handled via the framework's accept path: View binds
        // Enter → Command.Accept by default, and TextField does not override it.
        // Accepting fires after the keystroke is fully processed, which keeps the
        // submission logic decoupled from TextField's internal editing state.
        _textField.Accepting += OnTextFieldAccepting;

        // Intercept special shortcuts (Tab, Ctrl+C, Ctrl+D, Escape).
        _textField.KeyDown += OnTextFieldKeyDown;

        // Redraw when text changes so the status line stays in sync.
        _textField.ValueChanged += (_, _) => SetNeedsDraw();

        Add(_textField);
    }

    // --- Public API ---

    /// <summary>
    /// Wires the view to its controller. Must be called on the UI thread before
    /// the event loop starts. Binds the controller so Enter and EOF signals are
    /// routed to the chat channel.
    /// </summary>
    public void BindController(ComposerController controller)
    {
        _controller = controller;
    }

    /// <summary>
    /// Sets the autocomplete engine for slash-command ghost text.
    /// </summary>
    public void SetAutocomplete(AutocompleteEngine engine)
    {
        _autocomplete = engine;
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

    /// <summary>
    /// Sets the approximate context-window fill percentage shown to the left of
    /// the model identifier on the status line.
    /// </summary>
    public void SetContextUsagePercent(int? percent)
    {
        _contextUsagePercent = percent;
        SetNeedsDraw();
    }

    // --- Event handlers ---

    /// <summary>
    /// Called when Terminal.Gui invokes Command.Accept on the TextField (Enter key).
    ///
    /// Captures the current text, clears the field, and forwards the submission
    /// to the controller which writes it to the chat channel.
    ///
    /// Setting e.Handled prevents the Accept event from bubbling further, which
    /// avoids double-firing if the InputAreaView itself also has an Accept binding.
    /// </summary>
    private void OnTextFieldAccepting(object? sender, CommandEventArgs e)
    {
        var text = _textField.Text;
        _textField.Text = "";
        _controller?.OnViewSubmit(text);
        e.Handled = true;
    }

    /// <summary>
    /// Intercepts Tab, Ctrl+C, Ctrl+D, and Escape. Enter is no longer handled
    /// here — it flows through the framework's accept path instead.
    ///
    /// Tab triggers autocomplete. Ctrl+C and Ctrl+D signal EOF (session close).
    /// Escape is left to the REPL loop's turn-cancellation handler.
    /// </summary>
    private void OnTextFieldKeyDown(object? sender, Key key)
    {
        var keyCode = key.KeyCode;

        // Tab: accept autocomplete suggestion.
        if (keyCode == KeyCode.Tab)
        {
            var suffix = _autocomplete?.GetCompletion(_textField.Text);
            if (suffix is not null)
            {
                _textField.Text += suffix;
                _textField.MoveEnd();
            }
            key.Handled = true;
            return;
        }

        // Ctrl+C: EOF signal — closes the session.
        if (keyCode == (KeyCode.C | KeyCode.CtrlMask))
        {
            _controller?.OnViewEof();
            key.Handled = true;
            return;
        }

        // Ctrl+D on empty buffer: EOF (same semantics as Ctrl+C).
        if (keyCode == (KeyCode.D | KeyCode.CtrlMask) && string.IsNullOrEmpty(_textField.Text))
        {
            _controller?.OnViewEof();
            key.Handled = true;
            return;
        }

        // Escape: let the event bubble up to OxApp where the REPL loop
        // registers a per-turn handler that cancels the CancellationTokenSource.
        // Do NOT set key.Handled here — that would suppress propagation.
        if (keyCode == KeyCode.Esc)
            return;
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
    /// Draws the status line: throbber on the left, context/model summary on the right.
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

        // Context percentage and model identifier share a single right-aligned block so
        // the model remains visually anchored while the percentage appears immediately
        // to its left when usage data is available.
        var statusText = InputStatusFormatter.Compose(_contextUsagePercent, _modelId);
        if (statusText is not null)
        {
            var statusLen = Math.Min(statusText.Length, contentWidth);
            var modelStart = Math.Max(usedWidth, contentWidth - statusLen);
            var spacing = modelStart - usedWidth;

            SetAttribute(new Attribute(Color.None, Bg));
            for (var i = 0; i < spacing; i++)
                AddRune(' ');

            SetAttribute(new Attribute(ModelColor, Bg));
            AddStr(statusText[^statusLen..]);
            usedWidth = modelStart + statusLen;
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
