using Ox.Views;
using Te.Rendering;

namespace Ox.Connect;

/// <summary>
/// Renders the connect wizard as a centered floating modal overlay.
///
/// The wizard has two visual modes:
///
///   List mode (SelectProvider / SelectModel):
///     ┌──────────────────────┐
///     │  Select Provider     │
///     ├──────────────────────┤
///     │ > Google             │
///     │   Ollama             │
///     │   OpenAI             │
///     └──────────────────────┘
///
///   Input mode (EnterApiKey):
///     ┌──────────────────────┐
///     │  API Key             │
///     ├──────────────────────┤
///     │  your-key-here█      │
///     └──────────────────────┘
///
/// Width = max(widest item + 6, 32), centered horizontally.
/// Height = 2 (borders) + 1 (title) + 1 (divider) + item count for list,
///          or 5 (borders + title + divider + 1 input row) for input mode.
/// Vertical position = center of the buffer.
/// Box chrome matches the flatter monochrome panels elsewhere in the TUI:
/// ┌─┐ │ ├─┤ └─┘ on a pure-black fill.
/// </summary>
public sealed class ConnectWizardView
{
    private const char TopLeftCornerRune = '┌';
    private const char TopRightCornerRune = '┐';
    private const char BottomLeftCornerRune = '└';
    private const char BottomRightCornerRune = '┘';
    private const char DividerLeftRune = '├';
    private const char DividerRightRune = '┤';
    private const char HorizontalBorderRune = '─';
    private const char VerticalBorderRune = '│';

    // Minimum box width — keeps the modal from being comically narrow on wide
    // terminals with short provider names.
    private const int MinWidth = 32;

    // Chars consumed by the box frame plus the item prefix on each content row:
    //   │ [space] [>/ ] [space] item [space] │
    //   1 + 1 + 1 + 1 + item.len + 1 = item.len + 5, so we add 6 to get a
    //   right-side margin of one space inside the border.
    private const int FrameOverhead = 6;

    private readonly OxThemePalette _theme = OxThemePalette.Ox;

    /// <summary>
    /// Render the wizard modal centred in <paramref name="buffer"/>.
    /// Should only be called when <paramref name="wizard"/>.IsActive is true.
    /// </summary>
    public void Render(ConsoleBuffer buffer, ConnectWizardController wizard)
    {
        var isInput = wizard.CurrentStep == WizardStep.EnterApiKey;

        // ── Compute dimensions ───────────────────────────────────────────

        var items = wizard.DisplayItems;
        var contentWidth = isInput
            // Input row needs room for the key editor; use a generous minimum.
            ? Math.Max(MinWidth - FrameOverhead, 24)
            // List rows: wide enough for the longest item plus the prefix.
            : items.Count > 0 ? items.Max(s => s.Length) : 0;

        var boxWidth = Math.Max(contentWidth + FrameOverhead, MinWidth);

        // List: title + divider + items; input: title + divider + one input row.
        var innerRows = isInput ? 1 : items.Count;
        var boxHeight = 2 + 1 + 1 + innerRows; // top + bottom + title + divider + content

        // ── Compute position (centred) ───────────────────────────────────

        var startX = Math.Max(0, (buffer.Width - boxWidth) / 2);
        var startY = Math.Max(0, (buffer.Height - boxHeight) / 2);

        // Clamp so the box never spills outside the buffer.
        boxWidth = Math.Min(boxWidth, buffer.Width);
        boxHeight = Math.Min(boxHeight, buffer.Height);

        var bgColor = _theme.Background;
        var borderColor = _theme.ChromeBorder;
        var textColor = _theme.Text;
        var dimColor = _theme.StatusText;
        var innerWidth = boxWidth - 2;

        // Paint the modal body first so whatever was previously on screen does
        // not bleed through untouched interior cells.
        for (var row = startY; row < startY + boxHeight; row++)
            buffer.FillCells(startX, row, boxWidth, ' ', textColor, bgColor);

        buffer.SetCell(startX, startY, TopLeftCornerRune, borderColor, bgColor);
        buffer.FillCells(startX + 1, startY, boxWidth - 2, HorizontalBorderRune, borderColor, bgColor);
        buffer.SetCell(startX + boxWidth - 1, startY, TopRightCornerRune, borderColor, bgColor);

        // ── Row 1: title ─────────────────────────────────────────────────

        var titleRow = startY + 1;
        buffer.SetCell(startX, titleRow, VerticalBorderRune, borderColor, bgColor);
        buffer.SetCell(startX + boxWidth - 1, titleRow, VerticalBorderRune, borderColor, bgColor);

        var titleText = wizard.CurrentStep switch
        {
            WizardStep.SelectProvider => "Select Provider",
            WizardStep.EnterApiKey => "API Key",
            WizardStep.SelectModel => "Select Model",
            _ => "",
        };

        // Pad the title with a leading space and render left-aligned.
        var titleStr = $"  {titleText}";
        DrawText(buffer, startX + 1, titleRow, titleStr, innerWidth, textColor, bgColor);

        var dividerRow = startY + 2;
        buffer.SetCell(startX, dividerRow, DividerLeftRune, borderColor, bgColor);
        buffer.FillCells(startX + 1, dividerRow, boxWidth - 2, HorizontalBorderRune, borderColor, bgColor);
        buffer.SetCell(startX + boxWidth - 1, dividerRow, DividerRightRune, borderColor, bgColor);

        // ── Rows 2+: content ─────────────────────────────────────────────

        var contentStartRow = startY + 3;

        if (isInput)
        {
            RenderInputRow(buffer, startX, contentStartRow, boxWidth, wizard, textColor, borderColor, bgColor);
        }
        else
        {
            RenderListRows(buffer, startX, contentStartRow, boxWidth, wizard, textColor, dimColor, borderColor, bgColor);
        }

        var bottomRow = startY + boxHeight - 1;
        buffer.SetCell(startX, bottomRow, BottomLeftCornerRune, borderColor, bgColor);
        buffer.FillCells(startX + 1, bottomRow, boxWidth - 2, HorizontalBorderRune, borderColor, bgColor);
        buffer.SetCell(startX + boxWidth - 1, bottomRow, BottomRightCornerRune, borderColor, bgColor);
    }

    private static void RenderListRows(
        ConsoleBuffer buffer,
        int startX,
        int startRow,
        int boxWidth,
        ConnectWizardController wizard,
        Color textColor,
        Color dimColor,
        Color borderColor,
        Color bgColor)
    {
        var items = wizard.DisplayItems;
        for (var i = 0; i < items.Count; i++)
        {
            var row = startRow + i;
            if (row >= buffer.Height) break;

            buffer.SetCell(startX, row, VerticalBorderRune, borderColor, bgColor);
            buffer.SetCell(startX + boxWidth - 1, row, VerticalBorderRune, borderColor, bgColor);

            // Prefix: "> " for the selected item, "  " for others.
            var selected = i == wizard.SelectedIndex;
            var prefix = selected ? " > " : "   ";
            var color = selected ? textColor : dimColor;

            var rowText = prefix + items[i];
            DrawText(buffer, startX + 1, row, rowText, boxWidth - 2, color, bgColor);
        }
    }

    private static void RenderInputRow(
        ConsoleBuffer buffer,
        int startX,
        int row,
        int boxWidth,
        ConnectWizardController wizard,
        Color textColor,
        Color borderColor,
        Color bgColor)
    {
        if (row >= buffer.Height) return;

        buffer.SetCell(startX, row, VerticalBorderRune, borderColor, bgColor);
        buffer.SetCell(startX + boxWidth - 1, row, VerticalBorderRune, borderColor, bgColor);

        var editor = wizard.KeyEditor;
        var text = editor.Text;
        var innerWidth = boxWidth - 2;

        // Leading two-space indent to align with the list-mode prefix.
        var fieldX = startX + 1 + 2;
        var fieldWidth = innerWidth - 2;

        for (var i = 0; i < text.Length && i < fieldWidth; i++)
            buffer.SetCell(fieldX + i, row, text[i], textColor, bgColor);

        // Cursor block — white cell with black text, same style as the main
        // input area.
        var cursorCol = fieldX + editor.CursorPosition;
        if (cursorCol < startX + boxWidth - 1)
        {
            var cursorChar = editor.CursorPosition < text.Length ? text[editor.CursorPosition] : ' ';
            buffer.SetCell(cursorCol, row, cursorChar, Color.Black, textColor);
        }
    }

    // Write up to `maxWidth` characters of `text` into the buffer starting at (x, row).
    private static void DrawText(ConsoleBuffer buffer, int x, int row, string text, int maxWidth, Color fg, Color bg)
    {
        for (var i = 0; i < text.Length && i < maxWidth; i++)
            buffer.SetCell(x + i, row, text[i], fg, bg);
    }
}
