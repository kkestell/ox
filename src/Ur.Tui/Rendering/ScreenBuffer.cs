namespace Ur.Tui.Rendering;

/// <summary>
/// A width × height grid of <see cref="Cell"/> values representing a full terminal frame.
///
/// The viewport creates one ScreenBuffer per redraw, writes all visible CellRows into it
/// via <see cref="WriteRow"/>, then hands it to <see cref="Terminal.Flush"/> which iterates
/// every cell and emits the minimal ANSI sequence needed to paint it. Nothing else touches
/// the buffer — it is a pure data container, not a drawing abstraction.
/// </summary>
internal sealed class ScreenBuffer
{
    // Row-major: _cells[row, col] where 0 ≤ row < Height and 0 ≤ col < Width.
    private readonly Cell[,] _cells;

    public int Width  { get; }
    public int Height { get; }

    public ScreenBuffer(int width, int height)
    {
        Width  = Math.Max(1, width);
        Height = Math.Max(1, height);
        _cells = new Cell[Height, Width];
        // Initialize all cells to Empty (space, default colors, no style).
        Clear();
    }

    /// <summary>Gets or sets the cell at the given (0-based) row and column.</summary>
    public Cell this[int row, int col]
    {
        get => _cells[row, col];
        set => _cells[row, col] = value;
    }

    /// <summary>
    /// Writes <paramref name="cellRow"/> into the buffer at the given 0-based row.
    /// Cells beyond <see cref="Width"/> are truncated; columns not covered by the row
    /// are filled with <see cref="Cell.Empty"/> so leftover content from a prior frame
    /// does not bleed through.
    /// </summary>
    public void WriteRow(int row, CellRow cellRow)
    {
        if (row < 0 || row >= Height)
            return;

        var cells = cellRow.Cells;
        for (var col = 0; col < Width; col++)
        {
            _cells[row, col] = col < cells.Count ? cells[col] : Cell.Empty;
        }
    }

    /// <summary>Resets all cells to <see cref="Cell.Empty"/>.</summary>
    public void Clear()
    {
        for (var r = 0; r < Height; r++)
        for (var c = 0; c < Width;  c++)
            _cells[r, c] = Cell.Empty;
    }
}
