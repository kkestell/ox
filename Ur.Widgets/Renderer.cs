using Ur.Drawing;

namespace Ur.Widgets;

public static class Renderer
{
    /// <summary>
    /// Renders a laid-out widget tree into a Screen, with optional modal overlay.
    /// Root.Layout(w, h) must have been called before this — the Renderer only draws,
    /// it does not participate in sizing or positioning.
    ///
    /// Two-pass rendering when a modal is present:
    ///   1. Render the Root tree normally (the main application content).
    ///   2. Dim every cell on screen so the background fades visually.
    ///   3. Render the modal centered over the dimmed background.
    /// This is the simplest overlay model — no z-ordering or compositing needed,
    /// just two sequential tree walks into the same screen buffer.
    /// </summary>
    public static Screen Render(Widget root, Dialog? modal = null)
    {
        var screen = new Screen(root.Width, root.Height);
        var canvas = CanvasFactory.CreateCanvas(screen);

        // Pass 1: render the main application tree.
        RenderWidget(root, canvas);

        // Pass 2: if a modal is active, dim the background and render it centered.
        if (modal != null)
        {
            DimScreen(screen);

            // Center the modal in the terminal viewport.
            var modalX = (root.Width - modal.Width) / 2;
            var modalY = (root.Height - modal.Height) / 2;
            var modalRect = new Rect(modalX, modalY, modal.Width, modal.Height);
            var modalCanvas = canvas.SubCanvas(modalRect);

            RenderWidget(modal, modalCanvas);
        }

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

    /// <summary>
    /// Dims the entire screen buffer by halving each cell's foreground and background
    /// RGB values. This creates a darkened backdrop that makes the modal dialog pop
    /// visually while still letting the user see the application state behind it.
    ///
    /// Direct color manipulation is more reliable across terminals than the ANSI Dim
    /// modifier, which varies in implementation (some terminals ignore it entirely,
    /// others only affect foreground).
    /// </summary>
    private static void DimScreen(Screen screen)
    {
        for (var y = 0; y < screen.Height; y++)
        {
            for (var x = 0; x < screen.Width; x++)
            {
                var cell = screen.Get(x, y);
                var fg = cell.Style.Fg.Components;
                var bg = cell.Style.Bg.Components;

                var dimStyle = new Style(
                    Color.FromRgb((byte)(fg.R / 2), (byte)(fg.G / 2), (byte)(fg.B / 2)),
                    Color.FromRgb((byte)(bg.R / 2), (byte)(bg.G / 2), (byte)(bg.B / 2)),
                    cell.Style.Modifiers);

                screen.Set(x, y, Cell.Create(cell.Rune, dimStyle));
            }
        }
    }
}
