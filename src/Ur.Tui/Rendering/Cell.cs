namespace Ur.Tui.Rendering;

/// <summary>
/// A single terminal cell: one character plus its visual attributes.
///
/// Using char rather than System.Text.Rune keeps things simple — the current
/// character set is ASCII plus a handful of Unicode box-drawing characters that
/// all fall within the BMP and are representable as a single char.
///
/// Terminal.Flush is the sole consumer; it reads Rune, Foreground, Background,
/// and Style and emits the appropriate SGR sequences followed by the character.
/// </summary>
internal readonly struct Cell(char rune, Color foreground, Color background, CellStyle style = CellStyle.None)
    : IEquatable<Cell>
{
    public char       Rune       { get; } = rune;
    public Color      Foreground { get; } = foreground;
    public Color      Background { get; } = background;
    public CellStyle  Style      { get; } = style;

    /// <summary>
    /// A blank cell with default terminal colors and no styling.
    /// Used to initialize ScreenBuffer and to pad rows to their full width.
    /// </summary>
    public static Cell Empty => new(' ', Color.Default, Color.Default);

    public bool Equals(Cell other) =>
        Rune == other.Rune &&
        Foreground == other.Foreground &&
        Background == other.Background &&
        Style == other.Style;

    public override bool Equals(object? obj) => obj is Cell c && Equals(c);
    public override int GetHashCode() => HashCode.Combine(Rune, Foreground, Background, Style);
    public static bool operator ==(Cell a, Cell b) => a.Equals(b);
    public static bool operator !=(Cell a, Cell b) => !a.Equals(b);
}
