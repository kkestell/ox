namespace Ox.Terminal.Rendering;

/// <summary>
/// ANSI text decorations that can be combined on a single cell.
/// These stay as typed flags until render time so callers never need to think
/// in raw SGR parameter numbers.
/// </summary>
[Flags]
public enum TextDecoration
{
    None = 0,
    Bold = 1,
    Dim = 2,
    Italic = 4,
    Underline = 8,
    Blink = 16,
    Reverse = 32,
    Strikethrough = 64,
}
