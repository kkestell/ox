namespace Ur.Drawing;

/// <summary>
/// Concrete implementation of ICanvas.
/// Holds a reference to the underlying screen and defines its bounds.
/// Supports hierarchical clipping through a clip stack.
/// </summary>
internal sealed class Canvas : ICanvas
{
    private readonly Screen _screen;
    private readonly List<Rect> _clipStack;

    /// <summary>
    /// The rectangular bounds of this canvas in absolute screen coordinates.
    /// </summary>
    public Rect Bounds { get; }

    /// <summary>
    /// Creates a Canvas that covers the entire Screen.
    /// This is the root canvas for a rendering operation.
    /// </summary>
    public Canvas(Screen screen)
    {
        _screen = screen;
        Bounds = new Rect(0, 0, screen.Width, screen.Height);
        _clipStack = [];
    }

    /// <summary>
    /// Private constructor for creating sub-canvases.
    /// </summary>
    private Canvas(Screen screen, Rect bounds, List<Rect> clipStack)
    {
        _screen = screen;
        Bounds = bounds;
        _clipStack = clipStack;
    }

    /// <summary>
    /// Creates a new Canvas that is a sub-region of this canvas.
    /// The sub-canvas's bounds are clamped to the parent's bounds.
    /// </summary>
    public ICanvas SubCanvas(Rect rect)
    {
        var absoluteX = (ushort)(Bounds.X + rect.X);
        var absoluteY = (ushort)(Bounds.Y + rect.Y);

        ushort width = rect.Width;
        ushort height = rect.Height;

        if (absoluteX >= Bounds.Right)
            width = 0;
        else if (absoluteX + width > Bounds.Right)
            width = (ushort)(Bounds.Right - absoluteX);

        if (absoluteY >= Bounds.Bottom)
            height = 0;
        else if (absoluteY + height > Bounds.Bottom)
            height = (ushort)(Bounds.Bottom - absoluteY);

        var absoluteBounds = new Rect(absoluteX, absoluteY, width, height);
        return new Canvas(_screen, absoluteBounds, []);
    }

    /// <summary>
    /// Sets the content and style of a single cell at coordinates (x, y).
    /// Coordinates are relative to canvas origin. Clipped if outside clip stack.
    /// </summary>
    public void SetCell(ushort x, ushort y, char rune, Style style)
    {
        if (x >= Bounds.Width || y >= Bounds.Height)
            return;

        var absX = (ushort)(Bounds.X + x);
        var absY = (ushort)(Bounds.Y + y);

        foreach (var clipRect in _clipStack)
        {
            if (!clipRect.Contains(absX, absY))
                return;
        }

        _screen.Set(absX, absY, Cell.Create(rune, style));
    }

    /// <summary>
    /// Gets the content and style of a single cell at coordinates (x, y).
    /// Returns Default cell if coordinates are out of bounds.
    /// </summary>
    public Cell GetCell(ushort x, ushort y)
    {
        if (x >= Bounds.Width || y >= Bounds.Height)
            return Cell.Default;

        var absX = (ushort)(Bounds.X + x);
        var absY = (ushort)(Bounds.Y + y);

        return _screen.Get(absX, absY);
    }

    /// <summary>
    /// Fills the canvas area with a space character and the specified style.
    /// </summary>
    public void Clear(Style style)
    {
        var cell = Cell.Create(' ', style);
        for (ushort y = 0; y < Bounds.Height; y++)
        {
            for (ushort x = 0; x < Bounds.Width; x++)
            {
                var absX = (ushort)(Bounds.X + x);
                var absY = (ushort)(Bounds.Y + y);
                _screen.Set(absX, absY, cell);
            }
        }
    }

    /// <summary>
    /// Draws a string starting at (x, y), applying the style to all characters.
    /// Handles newlines by moving to the next line at the same x offset.
    /// </summary>
    public void DrawText(ushort x, ushort y, string text, Style style)
    {
        var col = x;
        var row = y;

        foreach (var r in text)
        {
            if (r == '\n')
            {
                row++;
                col = x;
                continue;
            }
            if (r == '\r')
                continue;

            SetCell(col, row, r, style);
            col++;
            if (col >= Bounds.Width)
                break;
        }
    }

    /// <summary>
    /// Fills the interior of the Rect with a single character and style.
    /// </summary>
    public void DrawRect(Rect rect, char fillRune, Style style)
    {
        for (var y = rect.Y; y < rect.Y + rect.Height; y++)
        {
            for (var x = rect.X; x < rect.X + rect.Width; x++)
            {
                SetCell(x, y, fillRune, style);
            }
        }
    }

    /// <summary>
    /// Draws a border around the Rect using characters defined by BorderSet.
    /// Requires at least 2x2 dimensions to draw a border.
    /// </summary>
    public void DrawBorder(Rect rect, Style style, BorderSet borderSet)
    {
        if (rect.Width < 2 || rect.Height < 2)
            return;

        SetCell(rect.X, rect.Y, borderSet.TopLeft, style);
        SetCell((ushort)(rect.X + rect.Width - 1), rect.Y, borderSet.TopRight, style);
        SetCell(rect.X, (ushort)(rect.Y + rect.Height - 1), borderSet.BottomLeft, style);
        SetCell((ushort)(rect.X + rect.Width - 1), (ushort)(rect.Y + rect.Height - 1), borderSet.BottomRight, style);

        for (var x = (ushort)(rect.X + 1); x < rect.X + rect.Width - 1; x++)
        {
            SetCell(x, rect.Y, borderSet.Horizontal, style);
            SetCell(x, (ushort)(rect.Y + rect.Height - 1), borderSet.Horizontal, style);
        }

        for (var y = (ushort)(rect.Y + 1); y < rect.Y + rect.Height - 1; y++)
        {
            SetCell(rect.X, y, borderSet.Vertical, style);
            SetCell((ushort)(rect.X + rect.Width - 1), y, borderSet.Vertical, style);
        }
    }

    /// <summary>
    /// Draws a horizontal line segment of 'width' length starting at (x, y).
    /// </summary>
    public void DrawHLine(ushort x, ushort y, ushort width, char rune, Style style)
    {
        for (var i = (ushort)0; i < width; i++)
        {
            SetCell((ushort)(x + i), y, rune, style);
        }
    }

    /// <summary>
    /// Draws a vertical line segment of 'height' length starting at (x, y).
    /// </summary>
    public void DrawVLine(ushort x, ushort y, ushort height, char rune, Style style)
    {
        for (var i = (ushort)0; i < height; i++)
        {
            SetCell(x, (ushort)(y + i), rune, style);
        }
    }

    /// <summary>
    /// Pushes a clipping rectangle onto the clip stack.
    /// The rect should be in absolute screen coordinates.
    /// </summary>
    public void PushClip(Rect rect)
    {
        _clipStack.Add(rect);
    }

    /// <summary>
    /// Removes the top clipping rectangle from the clip stack.
    /// </summary>
    public void PopClip()
    {
        if (_clipStack.Count > 0)
            _clipStack.RemoveAt(_clipStack.Count - 1);
    }
}

/// <summary>
/// Factory for creating Canvas instances.
/// </summary>
public static class CanvasFactory
{
    /// <summary>
    /// Creates a Canvas that covers the entire Screen.
    /// </summary>
    public static ICanvas CreateCanvas(Screen screen) => new Canvas(screen);
}
