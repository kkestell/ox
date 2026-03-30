namespace Ur.Terminal.Core;

public readonly record struct Color(byte R, byte G, byte B)
{
    public static readonly Color White = new(255, 255, 255);
    public static readonly Color Black = new(0, 0, 0);
    public static readonly Color Default = White;

    public Color Dim(float factor)
    {
        return new Color(
            (byte)Math.Clamp(R * factor, 0, 255),
            (byte)Math.Clamp(G * factor, 0, 255),
            (byte)Math.Clamp(B * factor, 0, 255));
    }
}
