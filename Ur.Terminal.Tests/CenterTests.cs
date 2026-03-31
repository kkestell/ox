using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Terminal.Layout;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Tests;

public class CenterTests
{
    private sealed class SpyWidget : Widget
    {
        public Rect? RenderedRect { get; private set; }
        public bool KeyHandled { get; set; }

        protected override void RenderContent(Buffer buffer, Rect area)
        {
            RenderedRect = area;
        }

        public override bool HandleKey(KeyEvent key) => KeyHandled;
    }

    [Fact]
    public void Render_CentersChildInArea()
    {
        var child = new SpyWidget();
        var center = new Center(child, 20, 10);
        var buffer = new Buffer(80, 24);

        center.Render(buffer, new Rect(0, 0, 80, 24));

        // (80-20)/2 = 30, (24-10)/2 = 7
        Assert.Equal(new Rect(30, 7, 20, 10), child.RenderedRect);
    }

    [Fact]
    public void Render_WithOffset_CentersRelativeToArea()
    {
        var child = new SpyWidget();
        var center = new Center(child, 10, 6);
        var buffer = new Buffer(60, 30);

        center.Render(buffer, new Rect(10, 5, 40, 20));

        // Area starts at (10,5). Center of 40x20: (40-10)/2 + 10 = 25, (20-6)/2 + 5 = 12
        Assert.Equal(new Rect(25, 12, 10, 6), child.RenderedRect);
    }

    [Fact]
    public void HandleKey_DelegatesToChild()
    {
        var child = new SpyWidget { KeyHandled = true };
        var center = new Center(child, 20, 10);

        var result = center.HandleKey(new KeyEvent(Key.Enter, Modifiers.None, null));

        Assert.True(result);
    }

    [Fact]
    public void HandleKey_ReturnsFalse_WhenChildDoesNot()
    {
        var child = new SpyWidget { KeyHandled = false };
        var center = new Center(child, 20, 10);

        var result = center.HandleKey(new KeyEvent(Key.Enter, Modifiers.None, null));

        Assert.False(result);
    }
}
