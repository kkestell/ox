using Ur.Terminal.Core;
using Ur.Terminal.Rendering;

namespace Ur.Terminal.Tests;

public class LayerTests
{
    [Fact]
    public void Clear_ResetsContentAndShadow()
    {
        var layer = new Layer(0, 0, 5, 5);
        layer.Content.Set(2, 2, new Cell('A', Color.White, Color.Black));
        layer.MarkShadow(new Rect(0, 0, 3, 3));
        layer.Clear();

        Assert.True(layer.Content.Get(2, 2).IsTransparent);
        Assert.False(layer.ShadowMask[0]);
    }

    [Fact]
    public void MarkShadow_SetsRegion()
    {
        var layer = new Layer(0, 0, 10, 10);
        layer.MarkShadow(new Rect(2, 2, 3, 3));

        Assert.True(layer.ShadowMask[2 * 10 + 2]);
        Assert.True(layer.ShadowMask[4 * 10 + 4]);
        Assert.False(layer.ShadowMask[0]);
        Assert.False(layer.ShadowMask[5 * 10 + 5]);
    }
}
