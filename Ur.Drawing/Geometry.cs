namespace Ur.Drawing;

/// <summary>
/// Represents a rectangular region using absolute screen coordinates.
/// Used for defining widget bounds, clipping regions, and drawing areas.
/// Immutable record providing value equality semantics.
/// </summary>
/// <param name="X">X coordinate of top-left corner</param>
/// <param name="Y">Y coordinate of top-left corner</param>
/// <param name="Width">Width in columns</param>
/// <param name="Height">Height in rows</param>
public record Rect(ushort X, ushort Y, ushort Width, ushort Height)
{
    /// <summary>
    /// Creates a new Rect with the specified dimensions.
    /// Factory method for more readable construction.
    /// </summary>
    public static Rect Create(ushort x, ushort y, ushort width, ushort height) => new(x, y, width, height);

    /// <summary>
    /// Returns true if the point (x, y) is inside the rectangle.
    /// Points on the right and bottom edges are considered outside (half-open interval).
    /// </summary>
    public bool Contains(ushort x, ushort y) =>
        x >= X && x < X + Width && y >= Y && y < Y + Height;

    /// <summary>
    /// Returns the x-coordinate of the right edge (exclusive).
    /// </summary>
    public ushort Right => (ushort)(X + Width);

    /// <summary>
    /// Returns the y-coordinate of the bottom edge (exclusive).
    /// </summary>
    public ushort Bottom => (ushort)(Y + Height);
}
