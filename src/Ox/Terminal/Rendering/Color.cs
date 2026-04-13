namespace Ox.Terminal.Rendering;

/// <summary>
/// Describes which ANSI encoding strategy a <see cref="Color"/> uses.
/// Keeping the encoding decision in the value object lets the renderer stay
/// generic and avoids baking escape sequences into individual cells.
/// </summary>
public enum ColorKind
{
    Default,
    Basic,
    Bright,
    Color256,
}

/// <summary>
/// Immutable terminal color metadata.
/// The buffer stores semantic colors and the renderer decides how to emit them.
/// That keeps the double-buffer model testable and avoids mixing terminal I/O
/// concerns into the backing cell grid.
/// </summary>
public readonly struct Color : IEquatable<Color>
{
    public ColorKind Kind { get; }
    public byte Value { get; }

    private Color(ColorKind kind, byte value)
    {
        Kind = kind;
        Value = value;
    }

    public static Color Default => new(ColorKind.Default, 0);
    public static Color Black => new(ColorKind.Basic, 0);
    public static Color Red => new(ColorKind.Basic, 1);
    public static Color Green => new(ColorKind.Basic, 2);
    public static Color Yellow => new(ColorKind.Basic, 3);
    public static Color Blue => new(ColorKind.Basic, 4);
    public static Color Magenta => new(ColorKind.Basic, 5);
    public static Color Cyan => new(ColorKind.Basic, 6);
    public static Color White => new(ColorKind.Basic, 7);

    public static Color BrightBlack => new(ColorKind.Bright, 0);
    public static Color BrightWhite => new(ColorKind.Bright, 7);

    public static Color FromIndex(byte index) => new(ColorKind.Color256, index);

    public bool Equals(Color other) => Kind == other.Kind && Value == other.Value;
    public override bool Equals(object? obj) => obj is Color other && Equals(other);
    public override int GetHashCode() => HashCode.Combine((int)Kind, Value);
    public static bool operator ==(Color left, Color right) => left.Equals(right);
    public static bool operator !=(Color left, Color right) => !left.Equals(right);
}
