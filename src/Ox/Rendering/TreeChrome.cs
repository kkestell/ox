using Te.Rendering;

namespace Ox.Rendering;

/// <summary>
/// Shared circle-prefix helpers used by <see cref="EventList"/> and
/// <see cref="SubagentRenderable"/> to render flat conversation items.
///
/// Each top-level item gets a <c>● </c> prefix (2 columns). Continuation rows
/// (wrapped text) get 2 spaces to align with the content after the circle.
/// The <c>└─</c> subordination for tool output is handled inside
/// <see cref="ToolRenderable"/> — TreeChrome only provides the circle prefix.
/// </summary>
internal static class TreeChrome
{
    // ● U+25CF BLACK CIRCLE — status indicator for circle-style items.
    public const char CircleChar = '●';

    // Chrome width: "● " = 2 columns (circle + space). Content width =
    // availableWidth - CircleChrome. Replaces the old 5-column ChildChrome
    // and 3-column NestChrome from the nested tree layout.
    public const int CircleChrome = 2;

    /// <summary>
    /// Prepends <c>● </c> to the content row. The circle glyph uses the supplied
    /// color (blue for User items, state-dependent for tool calls).
    /// </summary>
    public static CellRow MakeCircleRow(CellRow childRow, Color circleColor)
    {
        var row = new CellRow();
        row.Append(CircleChar, circleColor, Color.Default);
        row.Append(' ', Color.Default, Color.Default);

        foreach (var cell in childRow.Cells)
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Decorations);

        return row;
    }

    /// <summary>
    /// Prepends 2 spaces to a continuation row so wrapped text aligns with the
    /// content column after the <c>● </c> prefix.
    /// </summary>
    public static CellRow MakeContinuationRow(CellRow childRow)
    {
        var row = new CellRow();
        row.Append("  ", Color.Default, Color.Default);

        foreach (var cell in childRow.Cells)
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Decorations);

        return row;
    }
}
