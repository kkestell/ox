namespace Ur.Terminal.Core;

public readonly record struct Cell(char Char, Color Fg, Color Bg)
{
    public static readonly Cell Transparent = new('\0', Color.Default, Color.Default);

    public bool IsTransparent => Char == '\0';
}
