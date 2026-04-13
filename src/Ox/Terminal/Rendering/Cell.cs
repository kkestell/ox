namespace Ox.Terminal.Rendering;

/// <summary>
/// A single terminal cell.
/// The buffer stores a fully materialized frame as cells so rendering can stay
/// deterministic and diff-based instead of mixing drawing logic with console I/O.
/// </summary>
public readonly struct Cell : IEquatable<Cell>
{
    public char Rune { get; }
    public Color Foreground { get; }
    public Color Background { get; }
    public TextDecoration Decorations { get; }

    public Cell(char rune, Color foreground, Color background, TextDecoration decorations = TextDecoration.None)
    {
        Rune = rune;
        Foreground = foreground;
        Background = background;
        Decorations = decorations;
    }

    public static Cell Empty => new(' ', Color.Default, Color.Default);

    public bool Equals(Cell other) =>
        Rune == other.Rune &&
        Foreground == other.Foreground &&
        Background == other.Background &&
        Decorations == other.Decorations;

    public override bool Equals(object? obj) => obj is Cell other && Equals(other);
    public override int GetHashCode() => HashCode.Combine(Rune, Foreground, Background, (int)Decorations);
    public static bool operator ==(Cell left, Cell right) => left.Equals(right);
    public static bool operator !=(Cell left, Cell right) => !left.Equals(right);
}
