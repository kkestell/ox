using Ur.Terminal.Core;
using Ur.Terminal.Rendering;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Tests;

public class CompositorTests
{
    [Fact]
    public void SingleLayer_OpaqueCell_AppearsInOutput()
    {
        var compositor = new Compositor(10, 10);
        var layer = new Layer(0, 0, 10, 10);
        var cell = new Cell('A', Color.White, Color.Black);
        layer.Content.Set(3, 3, cell);
        compositor.AddLayer(layer);

        var output = compositor.Compose();

        Assert.Equal(cell, output.Get(3, 3));
    }

    [Fact]
    public void SingleLayer_TransparentCell_ShowsDefault()
    {
        var compositor = new Compositor(10, 10);
        var layer = new Layer(0, 0, 10, 10);
        compositor.AddLayer(layer);

        var output = compositor.Compose();

        Assert.Equal(' ', output.Get(0, 0).Char);
    }

    [Fact]
    public void TwoLayers_TopOpaqueOverridesBottom()
    {
        var compositor = new Compositor(10, 10);
        var bottom = new Layer(0, 0, 10, 10);
        var top = new Layer(0, 0, 10, 10);
        bottom.Content.Set(0, 0, new Cell('A', Color.White, Color.Black));
        top.Content.Set(0, 0, new Cell('B', Color.White, Color.Black));
        compositor.AddLayer(bottom);
        compositor.AddLayer(top);

        var output = compositor.Compose();

        Assert.Equal('B', output.Get(0, 0).Char);
    }

    [Fact]
    public void TwoLayers_TopTransparentShowsBottom()
    {
        var compositor = new Compositor(10, 10);
        var bottom = new Layer(0, 0, 10, 10);
        var top = new Layer(0, 0, 10, 10);
        bottom.Content.Set(0, 0, new Cell('A', Color.White, Color.Black));
        compositor.AddLayer(bottom);
        compositor.AddLayer(top);

        var output = compositor.Compose();

        Assert.Equal('A', output.Get(0, 0).Char);
    }

    [Fact]
    public void Shadow_PreservesCharacter()
    {
        var compositor = new Compositor(10, 10);
        var bottom = new Layer(0, 0, 10, 10);
        var top = new Layer(0, 0, 10, 10);
        bottom.Content.Set(0, 0, new Cell('X', new Color(200, 200, 200), Color.Black));
        top.MarkShadow(new Rect(0, 0, 1, 1));
        compositor.AddLayer(bottom);
        compositor.AddLayer(top);

        var output = compositor.Compose();

        Assert.Equal('X', output.Get(0, 0).Char);
    }

    [Fact]
    public void Shadow_DimsFgAndBg()
    {
        var compositor = new Compositor(10, 10);
        var bottom = new Layer(0, 0, 10, 10);
        var top = new Layer(0, 0, 10, 10);
        top.ShadowDimFactor = 0.4f;
        var fg = new Color(200, 100, 50);
        var bg = new Color(100, 50, 25);
        bottom.Content.Set(0, 0, new Cell('X', fg, bg));
        top.MarkShadow(new Rect(0, 0, 1, 1));
        compositor.AddLayer(bottom);
        compositor.AddLayer(top);

        var output = compositor.Compose();
        var cell = output.Get(0, 0);

        Assert.Equal(fg.Dim(0.4f), cell.Fg);
        Assert.Equal(bg.Dim(0.4f), cell.Bg);
    }

    [Fact]
    public void LayerOffset_PositionsCorrectly()
    {
        var compositor = new Compositor(20, 20);
        var layer = new Layer(5, 3, 10, 10);
        var cell = new Cell('Z', Color.White, Color.Black);
        layer.Content.Set(0, 0, cell);
        compositor.AddLayer(layer);

        var output = compositor.Compose();

        Assert.Equal(cell, output.Get(5, 3));
        Assert.Equal(' ', output.Get(0, 0).Char);
    }

    [Fact]
    public void LayerPartiallyOffScreen_Clips()
    {
        var compositor = new Compositor(5, 5);
        var layer = new Layer(3, 3, 10, 10);
        var cell = new Cell('Q', Color.White, Color.Black);
        layer.Content.Set(0, 0, cell);
        layer.Content.Set(9, 9, new Cell('R', Color.White, Color.Black));
        compositor.AddLayer(layer);

        var output = compositor.Compose();

        Assert.Equal(cell, output.Get(3, 3));
    }

    [Fact]
    public void ShadowAndContent_MutuallyExclusive()
    {
        var compositor = new Compositor(10, 10);
        var bottom = new Layer(0, 0, 10, 10);
        var top = new Layer(0, 0, 10, 10);
        bottom.Content.Set(0, 0, new Cell('A', Color.White, Color.Black));
        top.Content.Set(0, 0, new Cell('B', Color.White, Color.Black));
        top.MarkShadow(new Rect(0, 0, 1, 1));
        compositor.AddLayer(bottom);
        compositor.AddLayer(top);

        var output = compositor.Compose();

        Assert.Equal('A', output.Get(0, 0).Char);
    }
}
