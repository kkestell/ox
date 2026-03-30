using Ur.Terminal.Core;

namespace Ur.Terminal.Rendering;

public sealed class Compositor
{
    private static readonly Cell DefaultCell = new(' ', Color.Default, Color.Black);

    private readonly List<Layer> _layers = new();

    public int Width { get; private set; }
    public int Height { get; private set; }

    public Compositor(int width, int height)
    {
        Width = width;
        Height = height;
    }

    public void AddLayer(Layer layer) => _layers.Add(layer);

    public void RemoveLayer(Layer layer) => _layers.Remove(layer);

    public Core.Buffer Compose()
    {
        var output = new Core.Buffer(Width, Height);
        output.Fill(new Rect(0, 0, Width, Height), DefaultCell);

        for (var sy = 0; sy < Height; sy++)
        for (var sx = 0; sx < Width; sx++)
        {
            var accumulated = output.Get(sx, sy);

            foreach (var layer in _layers)
            {
                var lx = sx - layer.X;
                var ly = sy - layer.Y;

                if (lx < 0 || lx >= layer.Width || ly < 0 || ly >= layer.Height)
                    continue;

                var maskIndex = ly * layer.Width + lx;

                if (layer.ShadowMask[maskIndex])
                {
                    accumulated = new Cell(
                        accumulated.Char,
                        accumulated.Fg.Dim(layer.ShadowDimFactor),
                        accumulated.Bg.Dim(layer.ShadowDimFactor));
                }
                else
                {
                    var content = layer.Content.Get(lx, ly);
                    if (!content.IsTransparent)
                        accumulated = content;
                }
            }

            output.Set(sx, sy, accumulated);
        }

        return output;
    }

    public void Resize(int width, int height)
    {
        Width = width;
        Height = height;
    }
}
