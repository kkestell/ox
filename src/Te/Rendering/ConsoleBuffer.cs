using System.Text;

namespace Te.Rendering;

/// <summary>
/// Minimal double-buffered terminal renderer.
/// The back buffer is the next desired frame, the front buffer models what we
/// believe the terminal currently shows, and <see cref="Render"/> emits only the
/// cells that changed. That preserves the valuable part of ConsoleEx's rendering
/// architecture without dragging in its wider layout and windowing stack.
/// </summary>
public sealed class ConsoleBuffer
{
    private const string Escape = "\u001b[";

    private Cell[,] _backBuffer;
    private Cell[,] _frontBuffer;

    public int Width { get; private set; }
    public int Height { get; private set; }

    /// <summary>
    /// Allows callers to freeze terminal writes during resize or setup phases
    /// without losing the staged back-buffer contents.
    /// </summary>
    public bool LockRendering { get; set; }

    /// <summary>
    /// Optional override for <see cref="ColorKind.Default"/> backgrounds.
    /// When set, any cell whose background is <c>Color.Default</c> will be
    /// rendered using this color instead. This lets application code guarantee
    /// a specific background color (e.g. black) without requiring every view
    /// to hard-code it — <c>Color.Default</c> normally emits SGR 49, which
    /// means "use the terminal's configured default," and that can be any
    /// color the user has set in their terminal preferences.
    /// </summary>
    public Color? DefaultBackgroundOverride { get; set; }

    public ConsoleBuffer(int width, int height)
    {
        Width = Math.Max(1, width);
        Height = Math.Max(1, height);
        _backBuffer = new Cell[Height, Width];
        _frontBuffer = new Cell[Height, Width];
        InitializeBuffers();
    }

    public Cell GetCell(int x, int y) => IsValidPosition(x, y) ? _backBuffer[y, x] : Cell.Empty;

    public Cell GetRenderedCell(int x, int y) => IsValidPosition(x, y) ? _frontBuffer[y, x] : Cell.Empty;

    public void Resize(int width, int height)
    {
        Width = Math.Max(1, width);
        Height = Math.Max(1, height);
        _backBuffer = new Cell[Height, Width];
        _frontBuffer = new Cell[Height, Width];
        InitializeBuffers();
    }

    public void Clear(Cell? fill = null)
    {
        var value = fill ?? Cell.Empty;
        for (var y = 0; y < Height; y++)
        for (var x = 0; x < Width; x++)
            _backBuffer[y, x] = value;
    }

    public void SetCell(int x, int y, Cell cell)
    {
        if (!IsValidPosition(x, y))
            return;

        _backBuffer[y, x] = cell;
    }

    public void SetCell(
        int x,
        int y,
        char rune,
        Color foreground,
        Color background,
        TextDecoration decorations = TextDecoration.None)
    {
        SetCell(x, y, new Cell(rune, foreground, background, decorations));
    }

    public void FillCells(
        int x,
        int y,
        int width,
        char rune,
        Color foreground,
        Color background,
        TextDecoration decorations = TextDecoration.None)
    {
        if (width <= 0 || y < 0 || y >= Height || x >= Width)
            return;

        var fillCell = new Cell(rune, foreground, background, decorations);
        var start = Math.Max(0, x);
        var end = Math.Min(Width, x + width);
        for (var column = start; column < end; column++)
            _backBuffer[y, column] = fillCell;
    }

    public int GetDirtyCellCount()
    {
        var count = 0;
        for (var y = 0; y < Height; y++)
        for (var x = 0; x < Width; x++)
            if (_backBuffer[y, x] != _frontBuffer[y, x])
                count++;

        return count;
    }

    public void Render(TextWriter writer)
    {
        ArgumentNullException.ThrowIfNull(writer);

        if (LockRendering)
            return;

        Color? currentForeground = null;
        Color? currentBackground = null;
        TextDecoration? currentDecorations = null;
        var wroteAnyCells = false;

        for (var row = 0; row < Height; row++)
        {
            var column = 0;
            while (column < Width)
            {
                if (_backBuffer[row, column] == _frontBuffer[row, column])
                {
                    column++;
                    continue;
                }

                wroteAnyCells = true;
                writer.Write($"{Escape}{row + 1};{column + 1}H");

                while (column < Width && _backBuffer[row, column] != _frontBuffer[row, column])
                {
                    var cell = _backBuffer[row, column];
                    var effectiveBg = ResolveBackground(cell.Background);
                    if (cell.Foreground != currentForeground ||
                        effectiveBg != currentBackground ||
                        cell.Decorations != currentDecorations)
                    {
                        writer.Write(BuildSgr(cell.Foreground, effectiveBg, cell.Decorations));
                        currentForeground = cell.Foreground;
                        currentBackground = effectiveBg;
                        currentDecorations = cell.Decorations;
                    }

                    writer.Write(cell.Rune);
                    _frontBuffer[row, column] = cell;
                    column++;
                }
            }
        }

        if (!wroteAnyCells)
            return;

        writer.Write($"{Escape}0m");
        writer.Flush();
    }

    private void InitializeBuffers()
    {
        // Only initialize the back buffer to Cell.Empty. The front buffer
        // stays as the C# default struct (char '\0'), which differs from
        // Cell.Empty (char ' '). This ensures the first Render() pass sees
        // every cell as dirty and writes the full frame — critical for
        // painting the screen on startup and after a terminal resize.
        for (var y = 0; y < Height; y++)
        for (var x = 0; x < Width; x++)
            _backBuffer[y, x] = Cell.Empty;
    }

    /// <summary>
    /// Substitute <see cref="DefaultBackgroundOverride"/> for any
    /// <c>Color.Default</c> background so the terminal never falls back to
    /// its own configured background color.
    /// </summary>
    private Color ResolveBackground(Color bg) =>
        bg.Kind == ColorKind.Default && DefaultBackgroundOverride is { } over ? over : bg;

    private bool IsValidPosition(int x, int y) => x >= 0 && x < Width && y >= 0 && y < Height;

    private static string BuildSgr(Color foreground, Color background, TextDecoration decorations)
    {
        var builder = new StringBuilder();
        builder.Append(Escape);
        builder.Append('0');

        if ((decorations & TextDecoration.Bold) != 0)
            builder.Append(";1");
        if ((decorations & TextDecoration.Dim) != 0)
            builder.Append(";2");
        if ((decorations & TextDecoration.Italic) != 0)
            builder.Append(";3");
        if ((decorations & TextDecoration.Underline) != 0)
            builder.Append(";4");
        if ((decorations & TextDecoration.Blink) != 0)
            builder.Append(";5");
        if ((decorations & TextDecoration.Reverse) != 0)
            builder.Append(";7");
        if ((decorations & TextDecoration.Strikethrough) != 0)
            builder.Append(";9");

        builder.Append(';');
        builder.Append(ToSgrColor(foreground, background: false));
        builder.Append(';');
        builder.Append(ToSgrColor(background, background: true));
        builder.Append('m');
        return builder.ToString();
    }

    private static string ToSgrColor(Color color, bool background) => color.Kind switch
    {
        ColorKind.Default => background ? "49" : "39",
        ColorKind.Basic => background ? $"{40 + color.Value}" : $"{30 + color.Value}",
        ColorKind.Bright => background ? $"{100 + color.Value}" : $"{90 + color.Value}",
        ColorKind.Color256 => background ? $"48;5;{color.Value}" : $"38;5;{color.Value}",
        _ => background ? "49" : "39",
    };
}
