namespace Ox.Rendering;

/// <summary>
/// One horizontal row of terminal cells produced by a renderable.
///
/// CellRow is the unit of exchange between renderables and the viewport.
/// Renderables build CellRows from text segments with typed colors and styles;
/// the viewport writes each CellRow into a ScreenBuffer; Terminal.Flush converts
/// the buffer to ANSI escape sequences. No ANSI codes appear in CellRow or any
/// of the renderables that produce it.
/// </summary>
internal sealed class CellRow
{
    private readonly List<Cell> _cells = [];

    /// <summary>The cells that make up this row, in column order.</summary>
    public IReadOnlyList<Cell> Cells => _cells;

    /// <summary>An empty row (zero cells). Used for blank separator lines.</summary>
    public static CellRow Empty => new();

    /// <summary>
    /// Creates a row by converting every character of <paramref name="text"/> to a cell
    /// with uniform color and style. The common case: a styled text segment.
    /// </summary>
    public static CellRow FromText(string text, Color fg, Color bg, CellStyle style = CellStyle.None)
    {
        var row = new CellRow();
        foreach (var ch in text)
            row._cells.Add(new Cell(ch, fg, bg, style));
        return row;
    }

    /// <summary>Appends a single cell to this row.</summary>
    public void Append(char rune, Color fg, Color bg, CellStyle style = CellStyle.None) =>
        _cells.Add(new Cell(rune, fg, bg, style));

    /// <summary>Appends a uniformly-styled text segment to this row.</summary>
    public void Append(string text, Color fg, Color bg, CellStyle style = CellStyle.None)
    {
        foreach (var ch in text)
            _cells.Add(new Cell(ch, fg, bg, style));
    }

}
