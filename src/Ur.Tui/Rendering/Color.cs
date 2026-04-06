namespace Ur.Tui.Rendering;

/// <summary>
/// Discriminates which SGR color encoding a <see cref="Color"/> value uses.
/// The encoding determines which ANSI escape parameters Terminal.Flush emits.
/// </summary>
internal enum ColorKind
{
    /// <summary>Terminal default — emits SGR 39 (fg) or 49 (bg).</summary>
    Default,
    /// <summary>Standard 8-color palette — emits SGR 30–37 (fg) or 40–47 (bg).</summary>
    Basic,
    /// <summary>High-intensity 8-color palette — emits SGR 90–97 (fg) or 100–107 (bg).</summary>
    Bright,
    /// <summary>256-color palette — emits SGR 38;5;N (fg) or 48;5;N (bg).</summary>
    Color256,
}

/// <summary>
/// An immutable terminal color value. Carries both the kind (which encoding to use)
/// and the palette index. Terminal.Flush is the only code that reads this struct and
/// converts it to ANSI SGR parameters — nothing else needs to understand the encoding.
/// </summary>
internal readonly struct Color : IEquatable<Color>
{
    public ColorKind Kind  { get; }
    /// <summary>
    /// Palette index. Meaning depends on Kind:
    ///   Basic/Bright — 0=black, 1=red, 2=green, 3=yellow, 4=blue, 5=magenta, 6=cyan, 7=white
    ///   Color256     — 0–255 xterm 256-color index
    ///   Default      — ignored
    /// </summary>
    public byte      Value { get; }

    private Color(ColorKind kind, byte value) { Kind = kind; Value = value; }

    // --- Standard 8 foreground colors (SGR 30–37 / 40–47) ---
    public static Color Default  => new(ColorKind.Default, 0);
    public static Color Black    => new(ColorKind.Basic,   0); // SGR 30/40
    public static Color Red      => new(ColorKind.Basic,   1); // SGR 31/41
    public static Color Green    => new(ColorKind.Basic,   2); // SGR 32/42
    public static Color Yellow   => new(ColorKind.Basic,   3); // SGR 33/43
    public static Color Blue     => new(ColorKind.Basic,   4); // SGR 34/44
    public static Color Magenta  => new(ColorKind.Basic,   5); // SGR 35/45
    public static Color Cyan     => new(ColorKind.Basic,   6); // SGR 36/46
    public static Color White    => new(ColorKind.Basic,   7); // SGR 37/47

    // --- High-intensity variants (SGR 90–97 / 100–107) ---
    public static Color BrightBlack   => new(ColorKind.Bright, 0); // dark gray, SGR 90/100
    public static Color BrightRed     => new(ColorKind.Bright, 1); // SGR 91/101
    public static Color BrightGreen   => new(ColorKind.Bright, 2); // SGR 92/102
    public static Color BrightYellow  => new(ColorKind.Bright, 3); // SGR 93/103
    public static Color BrightBlue    => new(ColorKind.Bright, 4); // SGR 94/104
    public static Color BrightMagenta => new(ColorKind.Bright, 5); // SGR 95/105
    public static Color BrightCyan    => new(ColorKind.Bright, 6); // SGR 96/106
    public static Color BrightWhite   => new(ColorKind.Bright, 7); // SGR 97/107

    /// <summary>256-color palette entry (SGR 38;5;N for fg, 48;5;N for bg).</summary>
    public static Color FromIndex(byte index) => new(ColorKind.Color256, index);

    public bool Equals(Color other) => Kind == other.Kind && Value == other.Value;
    public override bool Equals(object? obj) => obj is Color c && Equals(c);
    public override int GetHashCode() => HashCode.Combine(Kind, Value);
    public static bool operator ==(Color a, Color b) => a.Equals(b);
    public static bool operator !=(Color a, Color b) => !a.Equals(b);
}
