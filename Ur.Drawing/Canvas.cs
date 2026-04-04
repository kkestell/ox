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
    ///
    /// rect is in parent-canvas-relative coordinates and may have a negative X or Y —
    /// this happens when the Renderer positions a scrolled child above or left of the
    /// viewport origin. The absolute origin is preserved (not clamped) so that the
    /// coordinate system inside the sub-canvas correctly maps local child positions
    /// to screen positions.
    ///
    /// Clipping is handled in two complementary ways:
    ///   - Right/bottom: width and height are clamped to the parent boundary here,
    ///     preventing canvas allocations that extend beyond the viewport.
    ///   - Top/left: the parent's Bounds are pushed onto the clip stack, so SetCell
    ///     rejects writes whose absolute coordinates fall outside the parent viewport.
    ///     This is necessary because a negative absoluteY can still land on a valid
    ///     screen row (e.g. a scrolled child whose first visible row overwrites a
    ///     sibling widget above the scroll view).
    /// </summary>
    public ICanvas SubCanvas(Rect rect)
    {
        var absoluteX = Bounds.X + rect.X;
        var absoluteY = Bounds.Y + rect.Y;
        var width = rect.Width;
        var height = rect.Height;

        // Clip the right/bottom edges to the parent canvas boundaries.
        if (absoluteX >= Bounds.Right)
            width = 0;
        else if (absoluteX + width > Bounds.Right)
            width = Bounds.Right - absoluteX;

        if (absoluteY >= Bounds.Bottom)
            height = 0;
        else if (absoluteY + height > Bounds.Bottom)
            height = Bounds.Bottom - absoluteY;

        width = Math.Max(0, width);
        height = Math.Max(0, height);

        var absoluteBounds = new Rect(absoluteX, absoluteY, width, height);

        // Accumulate the parent's bounds as a clip rect. Each SubCanvas level adds
        // its parent to the stack, so SetCell enforces the intersection of all
        // ancestor viewports — correctly clipping the top/left edges that the
        // width/height clamping above cannot guard against.
        var newClipStack = new List<Rect>(_clipStack) { Bounds };
        return new Canvas(_screen, absoluteBounds, newClipStack);
    }

    /// <summary>
    /// Sets the content and style of a single cell at coordinates (x, y).
    /// Coordinates are relative to canvas origin. Clipped if outside clip stack.
    /// </summary>
    public void SetCell(int x, int y, char rune, Style style)
    {
        if (x < 0 || y < 0 || x >= Bounds.Width || y >= Bounds.Height)
            return;

        var absX = Bounds.X + x;
        var absY = Bounds.Y + y;

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
    public Cell GetCell(int x, int y)
    {
        if (x < 0 || y < 0 || x >= Bounds.Width || y >= Bounds.Height)
            return Cell.Default;

        var absX = Bounds.X + x;
        var absY = Bounds.Y + y;

        return _screen.Get(absX, absY);
    }

    /// <summary>
    /// Fills the canvas area with a space character and the specified style.
    /// </summary>
    public void Clear(Style style)
    {
        var cell = Cell.Create(' ', style);
        for (var y = 0; y < Bounds.Height; y++)
        {
            for (var x = 0; x < Bounds.Width; x++)
            {
                _screen.Set(Bounds.X + x, Bounds.Y + y, cell);
            }
        }
    }

    /// <summary>
    /// Draws a string starting at (x, y), applying the style to all characters.
    /// Handles newlines by moving to the next line at the same x offset.
    /// </summary>
    public void DrawText(int x, int y, string text, Style style)
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
        SetCell(rect.X + rect.Width - 1, rect.Y, borderSet.TopRight, style);
        SetCell(rect.X, rect.Y + rect.Height - 1, borderSet.BottomLeft, style);
        SetCell(rect.X + rect.Width - 1, rect.Y + rect.Height - 1, borderSet.BottomRight, style);

        for (var x = rect.X + 1; x < rect.X + rect.Width - 1; x++)
        {
            SetCell(x, rect.Y, borderSet.Horizontal, style);
            SetCell(x, rect.Y + rect.Height - 1, borderSet.Horizontal, style);
        }

        for (var y = rect.Y + 1; y < rect.Y + rect.Height - 1; y++)
        {
            SetCell(rect.X, y, borderSet.Vertical, style);
            SetCell(rect.X + rect.Width - 1, y, borderSet.Vertical, style);
        }
    }

    /// <summary>
    /// Draws a horizontal line segment of 'width' length starting at (x, y).
    /// </summary>
    public void DrawHLine(int x, int y, int width, char rune, Style style)
    {
        for (var i = 0; i < width; i++)
        {
            SetCell(x + i, y, rune, style);
        }
    }

    /// <summary>
    /// Draws a vertical line segment of 'height' length starting at (x, y).
    /// </summary>
    public void DrawVLine(int x, int y, int height, char rune, Style style)
    {
        for (var i = 0; i < height; i++)
        {
            SetCell(x, y + i, rune, style);
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
