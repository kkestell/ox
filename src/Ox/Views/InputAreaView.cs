using Ox.Input;
using Te.Rendering;

namespace Ox.Views;

/// <summary>
/// Renders the 5-row input region at the bottom of the screen.
///
/// Layout (each row spans full terminal width):
///   Row 0: Top border     ╭────────────────────────────╮
///   Row 1: Text field     │ user input here█            │
///   Row 2: Divider        ├────────────────────────────┤
///   Row 3: Status line    │ ● ● ● ● ● ● ● ●  1%  model │
///   Row 4: Bottom border  ╰────────────────────────────╯
///
/// The view reads from TextEditor for the input field and from external
/// state for the status line (throbber, model, context %). It does not
/// own these — OxApp passes them in during each render call.
/// </summary>
public sealed class InputAreaView
{
    /// <summary>Fixed height of the input area in rows.</summary>
    public const int Height = 5;

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

        var borderColor = _theme.InputBorder;
        var dividerColor = _theme.Divider;
        var innerWidth = width - 2; // Subtract left and right border columns.

        // Row 0: Top border ╭───╮
        buffer.SetCell(x, y, '╭', borderColor, Color.Default);
        buffer.FillCells(x + 1, y, innerWidth, '─', borderColor, Color.Default);
        buffer.SetCell(x + width - 1, y, '╮', borderColor, Color.Default);

        // Row 1: Text field with cursor
        var textRow = y + 1;
        buffer.SetCell(x, textRow, '│', borderColor, Color.Default);
        buffer.SetCell(x + width - 1, textRow, '│', borderColor, Color.Default);

        // Render the text field content inside the borders.
        var fieldX = x + 1;
        var text = editor.Text;
        var cursor = editor.CursorPosition;

        // Draw text content with a 1-char padding inside the border.
        var textStart = fieldX + 1;
        for (var i = 0; i < text.Length && i < innerWidth - 2; i++)
        {
            var col = textStart + i;
            buffer.SetCell(col, textRow, text[i], _theme.Text, Color.Default);
        }

        // Draw ghost text (autocomplete suggestion) in dim color after the cursor.
        if (ghostText is not null)
        {
            var ghostStart = textStart + text.Length;
            for (var i = 0; i < ghostText.Length && ghostStart + i < x + width - 1; i++)
                buffer.SetCell(ghostStart + i, textRow, ghostText[i], _theme.StatusText, Color.Default);
        }

        // Show cursor as a white block with black text (visible against the dark background).
        if (isFocused)
        {
            var cursorCol = textStart + cursor;
            if (cursorCol < x + width - 1)
            {
                var cursorChar = cursor < text.Length ? text[cursor] : ' ';
                buffer.SetCell(cursorCol, textRow, cursorChar, Color.Black, _theme.Text);
            }
        }

        // Row 2: Horizontal divider — a clean line between the text field and status row.
        // Uses │ on the sides (same as the border verticals) to avoid T-junction clutter.
        var dividerRow = y + 2;
        buffer.SetCell(x, dividerRow, '│', borderColor, Color.Default);
        buffer.FillCells(x + 1, dividerRow, innerWidth, '─', dividerColor, Color.Default);
        buffer.SetCell(x + width - 1, dividerRow, '│', borderColor, Color.Default);

        // Row 3: Status line
        var statusRow = y + 3;
        buffer.SetCell(x, statusRow, '│', borderColor, Color.Default);
        buffer.SetCell(x + width - 1, statusRow, '│', borderColor, Color.Default);

        // Left side: throbber (when active).
        if (throbber is not null && throbber.Counter > 0)
        {
            throbber.Render(buffer, fieldX + 1, statusRow, _theme.ThrobberActive, _theme.ThrobberInactive);
        }

        // Right side: model + context %.
        if (statusRight is not null)
        {
            var rightStart = x + width - 2 - statusRight.Length;
            for (var i = 0; i < statusRight.Length; i++)
            {
                var col = rightStart + i;
                if (col > x && col < x + width - 1)
                    buffer.SetCell(col, statusRow, statusRight[i], _theme.StatusText, Color.Default);
            }
        }

        // Row 4: Bottom border ╰───╯
        var bottomRow = y + 4;
        buffer.SetCell(x, bottomRow, '╰', borderColor, Color.Default);
        buffer.FillCells(x + 1, bottomRow, innerWidth, '─', borderColor, Color.Default);
        buffer.SetCell(x + width - 1, bottomRow, '╯', borderColor, Color.Default);
    }
}
