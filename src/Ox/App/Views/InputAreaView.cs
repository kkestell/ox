using Ox.App.Input;
using Ox.Terminal.Rendering;

namespace Ox.App.Views;

/// <summary>
/// Renders the 5-row input region at the bottom of the screen.
///
/// Layout (each row spans full terminal width):
///   Row 0: Top border     ┌────────────────────────────┐
///   Row 1: Text field     │ user input here█            │
///   Row 2: Divider        ├────────────────────────────┤
///   Row 3: Status line    │ ● ● ● ● ● ● ● ●  1%  model │
///   Row 4: Bottom border  └────────────────────────────┘
///
/// The view reads from TextEditor for the input field and from external
/// state for the status line (throbber, model, context %). It does not
/// own these — OxApp passes them in during each render call.
/// </summary>
public sealed class InputAreaView
{
    private const int ContentPadding = 1;
    private const char TopLeftCornerRune = '┌';
    private const char TopRightCornerRune = '┐';
    private const char BottomLeftCornerRune = '└';
    private const char BottomRightCornerRune = '┘';
    private const char DividerLeftRune = '├';
    private const char DividerRightRune = '┤';
    private const char HorizontalBorderRune = '─';
    private const char VerticalBorderRune = '│';

    /// <summary>Fixed height of the input area in rows.</summary>
    public const int Height = 5;

    /// <summary>
    /// The composer now uses a flush box frame instead of an offset shadow, so
    /// layout no longer needs to reserve extra space on the right edge.
    /// </summary>
    public const int ShadowWidth = 0;

    /// <summary>
    /// The composer frame ends on its bottom border, so layout can anchor it
    /// directly against the terminal floor without a shadow gutter.
    /// </summary>
    public const int ShadowHeight = 0;

    /// <summary>
    /// Empty gutter kept outside the composer body on both sides so the slab
    /// has breathing room inside the terminal frame.
    /// </summary>
    public const int HorizontalMargin = 1;

    private readonly OxThemePalette _theme = OxThemePalette.Ox;

    /// <summary>
    /// Render the input area into <paramref name="buffer"/> at the given position.
    /// </summary>
    /// <param name="buffer">Target rendering buffer.</param>
    /// <param name="x">Left column.</param>
    /// <param name="y">Top row of the 5-row region.</param>
    /// <param name="width">Available width in columns.</param>
    /// <param name="editor">Text editor state for the input field.</param>
    /// <param name="ghostText">Autocomplete ghost text to show after the cursor, or null.</param>
    /// <param name="statusRight">Right-aligned status text (model + context %).</param>
    /// <param name="throbber">Throbber to render on the status line, or null if inactive.</param>
    /// <param name="isFocused">Whether the input field has focus (shows cursor).</param>
    public void Render(
        ConsoleBuffer buffer,
        int x, int y, int width,
        TextEditor editor,
        string? ghostText,
        string? statusRight,
        Throbber? throbber,
        bool isFocused)
    {
        if (width < 4) return; // Too narrow to render anything useful.

        var bgColor = _theme.Background;
        var borderColor = _theme.ChromeBorder;
        // Keep one interior blank cell on both sides so typing and status
        // content do not visually touch the frame. That shared content box
        // is reused by the editor text, throbber, and model/context summary.
        var contentLeft = x + 1 + ContentPadding;
        var contentRightExclusive = x + width - 1 - ContentPadding;
        var contentWidth = Math.Max(0, contentRightExclusive - contentLeft);

        // Paint the composer body first so both editable rows inherit the same
        // pure-black surface before the border and divider are layered on top.
        for (var row = y; row < y + Height && row < buffer.Height; row++)
            buffer.FillCells(x, row, width, ' ', _theme.Text, bgColor);

        buffer.SetCell(x, y, TopLeftCornerRune, borderColor, bgColor);
        buffer.FillCells(x + 1, y, width - 2, HorizontalBorderRune, borderColor, bgColor);
        buffer.SetCell(x + width - 1, y, TopRightCornerRune, borderColor, bgColor);

        // Row 1: Text field with cursor
        var textRow = y + 1;
        buffer.SetCell(x, textRow, VerticalBorderRune, borderColor, bgColor);
        buffer.SetCell(x + width - 1, textRow, VerticalBorderRune, borderColor, bgColor);

        var text = editor.Text;
        var cursor = editor.CursorPosition;

        // Keep the text one column in from the border so the thin-line frame
        // still leaves a little breathing room around the editable content.
        var textStart = contentLeft;
        for (var i = 0; i < text.Length && i < contentWidth; i++)
        {
            var col = textStart + i;
            buffer.SetCell(col, textRow, text[i], _theme.Text, bgColor);
        }

        // Draw ghost text (autocomplete suggestion) in dim color after the cursor.
        if (ghostText is not null)
        {
            var ghostStart = textStart + text.Length;
            for (var i = 0; i < ghostText.Length && ghostStart + i < contentRightExclusive; i++)
                buffer.SetCell(ghostStart + i, textRow, ghostText[i], _theme.StatusText, bgColor);
        }

        // Show cursor as a white block with black text (visible against the dark background).
        if (isFocused)
        {
            var cursorCol = textStart + cursor;
            if (cursorCol < contentRightExclusive)
            {
                var cursorChar = cursor < text.Length ? text[cursor] : ' ';
                buffer.SetCell(cursorCol, textRow, cursorChar, Color.Black, _theme.Text);
            }
        }

        // Row 2: Divider between typing and status. The split keeps the quiet
        // status strip legible without reintroducing the heavier shadow slab.
        var dividerRow = y + 2;
        buffer.SetCell(x, dividerRow, DividerLeftRune, borderColor, bgColor);
        buffer.FillCells(x + 1, dividerRow, width - 2, HorizontalBorderRune, borderColor, bgColor);
        buffer.SetCell(x + width - 1, dividerRow, DividerRightRune, borderColor, bgColor);

        // Row 3: Status line
        var statusRow = y + 3;
        buffer.SetCell(x, statusRow, VerticalBorderRune, borderColor, bgColor);
        buffer.SetCell(x + width - 1, statusRow, VerticalBorderRune, borderColor, bgColor);

        // Left side: throbber (when active).
        if (throbber is not null && throbber.Counter > 0)
        {
            throbber.Render(buffer, contentLeft, statusRow, _theme.ThrobberActive, _theme.ThrobberInactive, bgColor);
        }

        // Right side: model + context %.
        if (statusRight is not null)
        {
            var rightStart = contentRightExclusive - statusRight.Length;
            for (var i = 0; i < statusRight.Length; i++)
            {
                var col = rightStart + i;
                if (col >= contentLeft && col < contentRightExclusive)
                    buffer.SetCell(col, statusRow, statusRight[i], _theme.StatusText, bgColor);
            }
        }

        var bottomRow = y + 4;
        buffer.SetCell(x, bottomRow, BottomLeftCornerRune, borderColor, bgColor);
        buffer.FillCells(x + 1, bottomRow, width - 2, HorizontalBorderRune, borderColor, bgColor);
        buffer.SetCell(x + width - 1, bottomRow, BottomRightCornerRune, borderColor, bgColor);
    }
}
