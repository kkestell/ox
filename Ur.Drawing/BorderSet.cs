namespace Ur.Drawing;

/// <summary>
/// Defines a set of characters for drawing borders.
/// Different border sets can provide different visual styles (single line, double line, etc.)
/// </summary>
/// <param name="Horizontal">Character for horizontal border segments</param>
/// <param name="Vertical">Character for vertical border segments</param>
/// <param name="TopLeft">Character for top-left corner</param>
/// <param name="TopRight">Character for top-right corner</param>
/// <param name="BottomLeft">Character for bottom-left corner</param>
/// <param name="BottomRight">Character for bottom-right corner</param>
public record BorderSet(
    char Horizontal,
    char Vertical,
    char TopLeft,
    char TopRight,
    char BottomLeft,
    char BottomRight)
{
    /// <summary>
    /// Standard single-line border using box-drawing characters.
    /// This is the default border style for most widgets.
    /// </summary>
    public static BorderSet Single => new('─', '│', '┌', '┐', '└', '┘');

    /// <summary>
    /// Double-line border using box-drawing characters.
    /// Provides a more prominent visual separation.
    /// </summary>
    public static BorderSet Double => new('═', '║', '╔', '╗', '╚', '╝');

    /// <summary>
    /// Rounded corner single-line border.
    /// Provides a softer visual appearance.
    /// </summary>
    public static BorderSet Rounded => new('─', '│', '╭', '╮', '╰', '╯');
}
