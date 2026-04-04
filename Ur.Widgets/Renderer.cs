using Ur.Drawing;

namespace Ur.Widgets;

public static class Renderer
{
    /// <summary>
    /// Renders a laid-out widget tree into a Screen.
    /// Root.Layout(w, h) must have been called before this — the Renderer only draws,
    /// it does not participate in sizing or positioning.
    /// </summary>
    public static Screen Render(Widget root)
    {
        var screen = new Screen(root.Width, root.Height);
        var canvas = CanvasFactory.CreateCanvas(screen);
        RenderWidget(root, canvas);
        return screen;
    }

    /// <summary>
    /// Renders widget W into the provided canvas, then recurses into its children.
    ///
    /// Key design change from the old renderer: children are drawn relative to the
    /// *parent's canvas*, not the root canvas. Each child's sub-canvas is carved out
    /// of its parent's canvas at position (child.X - parent.OffsetX, child.Y - parent.OffsetY).
    ///
    /// This is the standard retained-mode model: parent-relative coordinates accumulate
    /// naturally as we descend the tree, and SubCanvas handles clipping at each level.
    /// OffsetX/OffsetY on the parent translate the entire child layer — ScrollView uses
    /// OffsetY to scroll without any special-casing in the Renderer.
    /// </summary>
    private static void RenderWidget(Widget widget, ICanvas widgetCanvas)
    {
        if (widget.Width <= 0 || widget.Height <= 0)
            return;

        widget.Draw(widgetCanvas);

        foreach (var child in widget.Children)
        {
            // Position the child relative to the parent's canvas, applying the parent's
            // scroll offset as a translation. A positive OffsetY scrolls content up,
            // so a child at Y=10 with OffsetY=10 appears at row 0 of the parent canvas.
            var childRect = new Rect(
                child.X - widget.OffsetX,
                child.Y - widget.OffsetY,
                child.Width,
                child.Height);

            // SubCanvas clamps negative origins to zero and reduces the visible size
            // accordingly, clipping children scrolled outside the parent viewport.
            var childCanvas = widgetCanvas.SubCanvas(childRect);

            RenderWidget(child, childCanvas);
        }
    }
}
