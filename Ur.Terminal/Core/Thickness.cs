namespace Ur.Terminal.Core;

/// <summary>Describes spacing on four sides. Used for widget padding.</summary>
public readonly record struct Thickness(int Top, int Right, int Bottom, int Left)
{
    public static readonly Thickness Zero = new(0, 0, 0, 0);

    public static Thickness Uniform(int value) => new(value, value, value, value);

    public int Horizontal => Left + Right;
    public int Vertical => Top + Bottom;
}
