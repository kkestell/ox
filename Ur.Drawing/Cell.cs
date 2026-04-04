namespace Ur.Drawing;

/// <summary>
/// Represents a single character cell in the terminal grid.
/// Each cell contains a Unicode character (rune) and its display style.
/// Immutable record providing value equality for efficient comparison.
/// </summary>
/// <param name="Rune">The character to display</param>
/// <param name="Style">The visual style (colors and modifiers)</param>
public record Cell(char Rune, Style Style)
{
    /// <summary>
    /// Creates a new Cell with the given rune and style.
    /// Factory method for more readable construction.
    /// </summary>
    public static Cell Create(char rune, Style style) => new(rune, style);

    /// <summary>
    /// Returns a Cell with a space character and default style.
    /// Useful for clearing cells to a blank state.
    /// </summary>
    public static Cell Default => new(' ', Style.Default);
}
