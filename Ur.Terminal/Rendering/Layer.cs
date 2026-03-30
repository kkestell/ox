using Ur.Terminal.Core;

namespace Ur.Terminal.Rendering;

public sealed class Layer
{
    public int X { get; set; }
    public int Y { get; set; }
    public Core.Buffer Content { get; private set; }
    public bool[] ShadowMask { get; private set; }
    public float ShadowDimFactor { get; set; } = 0.4f;

    public int Width => Content.Width;
    public int Height => Content.Height;

    public Layer(int x, int y, int width, int height)
    {
        X = x;
        Y = y;
        Content = new Core.Buffer(width, height);
        ShadowMask = new bool[width * height];
    }

    public void Clear()
    {
        Content.Clear();
        Array.Fill(ShadowMask, false);
    }

    public void MarkShadow(Rect region)
    {
        var clipped = region.Intersect(new Rect(0, 0, Width, Height));
        for (var y = clipped.Y; y < clipped.Bottom; y++)
        for (var x = clipped.X; x < clipped.Right; x++)
            ShadowMask[y * Width + x] = true;
    }

    public void Resize(int width, int height)
    {
        Content = new Core.Buffer(width, height);
        ShadowMask = new bool[width * height];
    }
}
