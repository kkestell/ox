using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A container widget that arranges children in a stack (vertical or horizontal).
/// </summary>
public class Stack : Widget
{
    public Stack(LayoutDirection direction = LayoutDirection.Vertical)
    {
        Direction = direction;
    }

    public static Stack Vertical() => new(LayoutDirection.Vertical);
    public static Stack Horizontal() => new(LayoutDirection.Horizontal);

    public override void Draw(ICanvas canvas)
    {
        // Stack is a container - it doesn't draw anything itself
        // Children are drawn by the rendering system
    }
}
