namespace Ur.Drawing;

/// <summary>
/// Defines the minimal drawing primitives for the TUI library.
/// A Canvas is a rectangular sub-region of a Screen (like a slice is to an array).
/// All coordinates are relative to the canvas's origin.
///
/// The canvas abstraction allows widgets to draw without knowing their absolute
/// screen position, enabling nested layouts and clipping.
/// </summary>
public interface ICanvas
{
    /// <summary>
    /// Sets the content and style of a single cell at coordinates (x, y).
    /// Coordinates are relative to the canvas origin, not the screen.
    /// </summary>
    void SetCell(int x, int y, char rune, Style style);

    /// <summary>
    /// Gets the content and style of a single cell at coordinates (x, y).
    /// Returns Default cell if coordinates are out of bounds.
    /// </summary>
    Cell GetCell(int x, int y);

    /// <summary>
    /// Fills the canvas area with a space character and the specified style.
    /// </summary>
    void Clear(Style style);

    /// <summary>
    /// Draws a string starting at (x, y), applying the style to all characters.
    /// Handles newlines by moving to the next line.
    /// </summary>
    void DrawText(int x, int y, string text, Style style);

    /// <summary>
    /// Fills the interior of the Rect with a single character and style.
    /// </summary>
    void DrawRect(Rect rect, char fillRune, Style style);

    /// <summary>
    /// Draws a border around the Rect using characters defined by BorderSet.
    /// </summary>
    void DrawBorder(Rect rect, Style style, BorderSet borderSet);

    /// <summary>
    /// Draws a horizontal line segment of 'width' length starting at (x, y).
    /// </summary>
    void DrawHLine(int x, int y, int width, char rune, Style style);

    /// <summary>
    /// Draws a vertical line segment of 'height' length starting at (x, y).
    /// </summary>
    void DrawVLine(int x, int y, int height, char rune, Style style);

    /// <summary>
    /// Creates a new Canvas that is a sub-region of this canvas.
    /// The sub-canvas's origin becomes (parent origin + rect position).
    /// rect.X and rect.Y may be negative when content is scrolled; SubCanvas
    /// clamps the resulting bounds so negative-origin children are clipped, not wrapped.
    /// </summary>
    ICanvas SubCanvas(Rect rect);

    /// <summary>
    /// Pushes a clipping rectangle onto the clip stack.
    /// All drawing operations will be clipped to the intersection of all rects in the stack.
    /// Clip rects are in absolute screen coordinates.
    /// </summary>
    void PushClip(Rect rect);

    /// <summary>
    /// Removes the top clipping rectangle from the clip stack.
    /// </summary>
    void PopClip();

    /// <summary>
    /// Returns the rectangular bounds of this canvas, relative to its parent.
    /// The top-level canvas (from NewCanvas) will have bounds relative to the screen (0, 0).
    /// </summary>
    Rect Bounds { get; }
}
