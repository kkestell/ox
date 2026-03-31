using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Layout;

/// <summary>Widget that positions a fixed-size child in the center of its area.</summary>
public sealed class Center : Widget
{
    private readonly Widget _child;
    private readonly int _childWidth;
    private readonly int _childHeight;

    public Center(Widget child, int width, int height)
    {
        _child = child;
        _childWidth = width;
        _childHeight = height;
    }

    protected override void RenderContent(Buffer buffer, Rect area)
    {
        var x = area.X + (area.Width - _childWidth) / 2;
        var y = area.Y + (area.Height - _childHeight) / 2;
        _child.Render(buffer, new Rect(x, y, _childWidth, _childHeight));
    }

    public override bool HandleKey(KeyEvent key) => _child.HandleKey(key);
}
