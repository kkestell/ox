using Ur.Drawing;

namespace Ur.Widgets;

public static class Renderer
{
    /// <summary>
    /// Renders a laid-out widget tree into a Screen.
    /// The root widget's X/Y/Width/Height must already be set by the layout engine.
    /// All widget coordinates are absolute (relative to the root canvas origin).
    /// </summary>
    public static Screen Render(Widget root)
    {
        var screen = new Screen(root.Width, root.Height);
        var canvas = CanvasFactory.CreateCanvas(screen);
        RenderWidget(root, canvas);
        return screen;
    }

    /// <summary>
    /// Renders a widget subtree into the provided canvas.
    /// Used by ScrollView to render its offscreen content buffer independently of the main tree walk.
    /// The root widget's layout coordinates must already be set before calling this.
    /// </summary>
    public static void RenderTree(Widget root, ICanvas canvas) => RenderWidget(root, canvas);

    private static void RenderWidget(Widget widget, ICanvas rootCanvas)
    {
        if (widget.Width <= 0 || widget.Height <= 0)
            return;

        // Widget X/Y are absolute screen coordinates set by the layout engine,
        // so we always create sub-canvases relative to the root canvas (origin 0,0)
        // rather than relative to the parent widget's canvas.
        var sub = rootCanvas.SubCanvas(new Rect(
            Math.Max(0, widget.X),
            Math.Max(0, widget.Y),
            widget.Width,
            widget.Height));

        widget.Draw(sub);

        foreach (var child in widget.Children)
            RenderWidget(child, rootCanvas);
    }
}
