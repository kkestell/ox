namespace Ur.Terminal.Core;

public sealed class Buffer
{
    private readonly Cell[] _cells;

    public int Width { get; }
    public int Height { get; }

    public Buffer(int width, int height)
    {
        Width = width;
        Height = height;
        _cells = new Cell[width * height];
        Array.Fill(_cells, Cell.Transparent);
    }

    public void Set(int x, int y, Cell cell)
    {
        if (x < 0 || x >= Width || y < 0 || y >= Height)
            return;
        _cells[y * Width + x] = cell;
    }

    public Cell Get(int x, int y)
    {
        if (x < 0 || x >= Width || y < 0 || y >= Height)
            return Cell.Transparent;
        return _cells[y * Width + x];
    }

    public void Fill(Rect area, Cell cell)
    {
        var clipped = area.Intersect(new Rect(0, 0, Width, Height));
        for (var y = clipped.Y; y < clipped.Bottom; y++)
        for (var x = clipped.X; x < clipped.Right; x++)
            _cells[y * Width + x] = cell;
    }

    public void WriteString(int x, int y, ReadOnlySpan<char> text, Color fg, Color bg)
    {
        if (y < 0 || y >= Height)
            return;

        for (var i = 0; i < text.Length; i++)
        {
            var cx = x + i;
            if (cx < 0) continue;
            if (cx >= Width) break;
            _cells[y * Width + cx] = new Cell(text[i], fg, bg);
        }
    }

    public void DrawBox(Rect area, Color fg, Color bg)
    {
        if (area.Width < 2 || area.Height < 2)
            return;

        var right = area.Right - 1;
        var bottom = area.Bottom - 1;

        Set(area.X, area.Y, new Cell('┌', fg, bg));
        Set(right, area.Y, new Cell('┐', fg, bg));
        Set(area.X, bottom, new Cell('└', fg, bg));
        Set(right, bottom, new Cell('┘', fg, bg));

        for (var x = area.X + 1; x < right; x++)
        {
            Set(x, area.Y, new Cell('─', fg, bg));
            Set(x, bottom, new Cell('─', fg, bg));
        }

        for (var y = area.Y + 1; y < bottom; y++)
        {
            Set(area.X, y, new Cell('│', fg, bg));
            Set(right, y, new Cell('│', fg, bg));
        }
    }

    public void Clear()
    {
        Array.Fill(_cells, Cell.Transparent);
    }
}
