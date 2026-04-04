namespace Ur.Drawing;

/// <summary>
/// Represents the underlying screen buffer that Canvas writes to.
/// This is a 2D grid of cells representing the terminal screen.
/// The screen owns the actual cell storage; Canvas provides a window into it.
/// </summary>
public sealed class Screen
{
    private readonly Cell[] _cells;

    /// <summary>
    /// Width of the screen in columns.
    /// </summary>
    public int Width { get; }

    /// <summary>
    /// Height of the screen in rows.
    /// </summary>
    public int Height { get; }

    /// <summary>
    /// Creates a new Screen with the specified dimensions.
    /// All cells are initialized to the default (space with default style).
    /// </summary>
    public Screen(int width, int height)
    {
        Width = width;
        Height = height;
        _cells = new Cell[width * height];
        Array.Fill(_cells, Cell.Default);
    }

    /// <summary>
    /// Sets the cell at the specified absolute screen coordinates.
    /// Coordinates outside the screen bounds (including negative) are ignored (no-op).
    /// </summary>
    public void Set(int x, int y, Cell cell)
    {
        if (x < 0 || y < 0 || x >= Width || y >= Height)
            return;
        _cells[y * Width + x] = cell;
    }

    /// <summary>
    /// Gets the cell at the specified absolute screen coordinates.
    /// Returns Default cell if coordinates are out of bounds.
    /// </summary>
    public Cell Get(int x, int y)
    {
        if (x < 0 || y < 0 || x >= Width || y >= Height)
            return Cell.Default;
        return _cells[y * Width + x];
    }

    /// <summary>
    /// Clears all cells to the default state.
    /// </summary>
    public void Clear()
    {
        Array.Fill(_cells, Cell.Default);
    }
}
