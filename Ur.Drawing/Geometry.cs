namespace Ur.Drawing;

/// <summary>
/// Represents a rectangular region. Coordinates can be negative — this is required
/// after the ushort→int migration so that SubCanvas can represent children that are
/// partially scrolled above or left of their parent's viewport origin.
/// </summary>
/// <param name="X">X coordinate of top-left corner (can be negative)</param>
/// <param name="Y">Y coordinate of top-left corner (can be negative)</param>
/// <param name="Width">Width in columns</param>
/// <param name="Height">Height in rows</param>
public record Rect(int X, int Y, int Width, int Height)
{
    /// <summary>
    /// Creates a new Rect with the specified dimensions.
    /// Factory method for more readable construction.
    /// </summary>
    public static Rect Create(int x, int y, int width, int height) => new(x, y, width, height);

    /// <summary>
    /// Returns true if the point (x, y) is inside the rectangle.
    /// Points on the right and bottom edges are considered outside (half-open interval).
    /// </summary>
    public bool Contains(int x, int y) =>
        x >= X && x < X + Width && y >= Y && y < Y + Height;

    /// <summary>
    /// Returns the x-coordinate of the right edge (exclusive).
    /// </summary>
    public int Right => X + Width;

    /// <summary>
    /// Returns the y-coordinate of the bottom edge (exclusive).
    /// </summary>
    public int Bottom => Y + Height;
}
