namespace Ur.Drawing;

/// <summary>
/// Represents a 24-bit RGB color value (0xRRGGBB format).
/// Immutable record for type-safe color handling.
/// </summary>
public readonly record struct Color(uint Value)
{
    /// <summary>
    /// Predefined common colors using RGB values.
    /// These match standard terminal color palette.
    /// </summary>
    public static readonly Color Black = new(0x000000);
    public static readonly Color Red = new(0x800000);
    public static readonly Color Green = new(0x008000);
    public static readonly Color Yellow = new(0x808000);
    public static readonly Color Blue = new(0x000080);
    public static readonly Color Magenta = new(0x800080);
    public static readonly Color Cyan = new(0x008080);
    public static readonly Color White = new(0xC0C0C0);
    public static readonly Color BrightBlack = new(0x808080);
    public static readonly Color BrightRed = new(0xFF0000);
    public static readonly Color BrightGreen = new(0x00FF00);
    public static readonly Color BrightYellow = new(0xFFFF00);
    public static readonly Color BrightBlue = new(0x0000FF);
    public static readonly Color BrightMagenta = new(0xFF00FF);
    public static readonly Color BrightCyan = new(0x00FFFF);
    public static readonly Color BrightWhite = new(0xFFFFFF);

    /// <summary>
    /// Creates a Color from RGB components (0-255).
    /// </summary>
    public static Color FromRgb(byte r, byte g, byte b) =>
        new((uint)((r << 16) | (g << 8) | b));

    /// <summary>
    /// Extracts the RGB components from this Color.
    /// </summary>
    public (byte R, byte G, byte B) Components =>
        ((byte)((Value >> 16) & 0xFF), (byte)((Value >> 8) & 0xFF), (byte)(Value & 0xFF));
}

/// <summary>
/// Represents text attributes like bold, italic, etc.
/// Multiple modifiers can be combined using bitwise OR.
/// Flags enum for composable text styling.
/// </summary>
[Flags]
public enum Modifier : byte
{
    None = 0,
    Bold = 1 << 0,
    Italic = 1 << 1,
    Underline = 1 << 2,
    Reversed = 1 << 3,
    Dim = 1 << 4,
    Blink = 1 << 5,
    Strike = 1 << 6
}

/// <summary>
/// Defines the visual appearance of a cell including foreground and
/// background colors and text modifiers (bold, italic, underline, etc.).
/// Immutable record for consistent styling across the rendering pipeline.
/// </summary>
/// <param name="Fg">Foreground (text) color</param>
/// <param name="Bg">Background color</param>
/// <param name="Modifiers">Text attributes (bitmask of Modifier flags)</param>
public record Style(Color Fg, Color Bg, Modifier Modifiers = Modifier.None)
{
    /// <summary>
    /// Returns a Style with white foreground and black background.
    /// This is the default terminal appearance.
    /// </summary>
    public static Style Default => new(Color.White, Color.Black);
}
