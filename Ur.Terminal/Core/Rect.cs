namespace Ur.Terminal.Core;

public readonly record struct Rect(int X, int Y, int Width, int Height)
{
    public int Right => X + Width;
    public int Bottom => Y + Height;

    public bool Contains(int x, int y) =>
        x >= X && x < Right && y >= Y && y < Bottom;

    public Rect Intersect(Rect other)
    {
        var x = Math.Max(X, other.X);
        var y = Math.Max(Y, other.Y);
        var right = Math.Min(Right, other.Right);
        var bottom = Math.Min(Bottom, other.Bottom);

        if (right <= x || bottom <= y)
            return new Rect(0, 0, 0, 0);

        return new Rect(x, y, right - x, bottom - y);
    }
}
