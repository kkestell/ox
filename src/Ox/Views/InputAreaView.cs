using Terminal.Gui.App;
using Terminal.Gui.Drawing;
using Terminal.Gui.ViewBase;
using Attribute = Terminal.Gui.Drawing.Attribute;

namespace Ox.Views;

/// <summary>
/// The chat composer panel at the bottom of the screen. Draws a rounded border
/// containing the input text row, a divider, and a status line (throbber + model ID).
///
/// This is a custom-drawn View rather than a TextField wrapper because we need:
///   - Ghost text rendering (grayed-out autocomplete suffix)
///   - A synthetic block cursor (reverse-video space)
///   - Custom border/divider chrome matching the original design
///   - Status line with animated throbber
///
/// Input handling is done externally by OxApp, which processes keys and updates
/// the prompt text via <see cref="SetPrompt"/>. This view is purely presentational.
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
    private string _prompt = "";
    private string? _completionSuffix;
    private bool _turnRunning;
    private string? _modelId;
    private long _turnStartedAtTickMs = -1;
    private int _lastAnimatedCounter = -1;
    private object? _throbberToken;

    public InputAreaView(IApplication app)
    {
        _app = app;
        CanFocus = false;
    }

    // --- Public API ---

    /// <summary>
    /// Updates the prompt text displayed in the input row.
    /// Called by the input processing loop after each keystroke.
    /// </summary>
    public void SetPrompt(string prompt)
    {
        _prompt = prompt;
        SetNeedsDraw();
    }

    /// <summary>
    /// Sets the ghost-text completion suffix shown after the cursor.
    /// Pass null to clear.
    /// </summary>
    public void SetCompletion(string? suffix)
    {
        _completionSuffix = suffix;
        SetNeedsDraw();
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

        // Row 1: input text with cursor
        DrawInputRow(1, width);

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
    /// Draws the input text row with border, padding, prompt text, ghost text,
    /// and synthetic cursor.
    /// </summary>
    private void DrawInputRow(int row, int width)
    {
        if (width <= 4) return;

        // Left border + padding
        Move(0, row);
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);
        AddRune(' ');

        var contentWidth = Math.Max(0, width - 4); // border + pad on each side

        // Prompt text in white
        var promptLength = Math.Min(_prompt.Length, contentWidth);
        SetAttribute(new Attribute(new Color(ColorName16.White), Bg));
        if (promptLength > 0)
            AddStr(_prompt[..promptLength]);

        var remaining = contentWidth - promptLength;

        // Ghost text or cursor
        if (!string.IsNullOrEmpty(_completionSuffix) && remaining > 0)
        {
            // First char of ghost text rendered as cursor (reverse video)
            SetAttribute(new Attribute(new Color(ColorName16.DarkGray), Bg, TextStyle.Reverse));
            AddRune(_completionSuffix[0]);
            remaining--;

            // Rest of ghost text in plain gray
            if (_completionSuffix.Length > 1 && remaining > 0)
            {
                SetAttribute(new Attribute(new Color(ColorName16.DarkGray), Bg));
                var ghostText = _completionSuffix[1..];
                var ghostLen = Math.Min(ghostText.Length, remaining);
                AddStr(ghostText[..ghostLen]);
                remaining -= ghostLen;
            }
        }
        else if (remaining > 0)
        {
            // Block cursor: reverse-video space
            SetAttribute(new Attribute(new Color(ColorName16.White), Bg, TextStyle.Reverse));
            AddRune(' ');
            remaining--;
        }

        // Fill remaining space
        SetAttribute(new Attribute(Color.None, Bg));
        for (var i = 0; i < remaining; i++)
            AddRune(' ');

        // Right padding + border
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
