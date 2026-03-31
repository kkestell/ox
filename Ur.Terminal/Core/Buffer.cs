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

    public void DrawBox(Rect area, Color fg, Color bg) =>
        DrawBorder(area, top: true, bottom: true, left: true, right: true, fg, bg);

    public void DrawBorder(Rect area, bool top, bool bottom, bool left, bool right, Color fg, Color bg)
    {
        if (area.Width < 1 || area.Height < 1)
            return;

        var r = area.Right - 1;
        var b = area.Bottom - 1;

        if (top)
        {
            var startX = left ? area.X + 1 : area.X;
            var endX = right ? r : area.Right;
            for (var x = startX; x < endX; x++)
                Set(x, area.Y, new Cell('─', fg, bg));
        }

        if (bottom)
        {
            var startX = left ? area.X + 1 : area.X;
            var endX = right ? r : area.Right;
            for (var x = startX; x < endX; x++)
                Set(x, b, new Cell('─', fg, bg));
        }

        if (left)
        {
            var startY = top ? area.Y + 1 : area.Y;
            var endY = bottom ? b : area.Bottom;
            for (var y = startY; y < endY; y++)
                Set(area.X, y, new Cell('│', fg, bg));
        }

        if (right)
        {
            var startY = top ? area.Y + 1 : area.Y;
            var endY = bottom ? b : area.Bottom;
            for (var y = startY; y < endY; y++)
                Set(r, y, new Cell('│', fg, bg));
        }

        // Corners — only where both adjacent sides are active
        if (top && left)   Set(area.X, area.Y, new Cell('┌', fg, bg));
        if (top && right)  Set(r, area.Y,       new Cell('┐', fg, bg));
        if (bottom && left)  Set(area.X, b,     new Cell('└', fg, bg));
        if (bottom && right) Set(r, b,           new Cell('┘', fg, bg));
    }

    public void Clear()
    {
        Array.Fill(_cells, Cell.Transparent);
    }
}
