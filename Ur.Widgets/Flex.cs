using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A container widget that arranges children using the 7-pass flex layout algorithm.
/// Flex replaces both Stack (which had no layout logic) and LayoutEngine (which had
/// all the logic but lived outside the widget tree). By baking the algorithm into a
/// widget, every container participates in the Layout() top-down pass without needing
/// a separate engine call.
///
/// The 7-pass algorithm:
///   1. FitWidth       — natural widths bottom-up
///   2. ApplyMinMaxW   — enforce width constraints bottom-up
///   3. GrowShrinkW    — distribute spare / absorb overflow top-down (width)
///   4. FitHeight      — natural heights bottom-up (widths are final, so wrapping is correct)
///   5. ApplyMinMaxH   — enforce height constraints bottom-up
///   6. GrowShrinkH    — distribute spare / absorb overflow top-down (height)
///   7. Position       — parent-relative X/Y coords, then recurse into children
/// </summary>
public class Flex : Widget
{
    public Flex(LayoutDirection direction = LayoutDirection.Vertical)
    {
        Direction = direction;
    }

    public static Flex Vertical()   => new(LayoutDirection.Vertical);
    public static Flex Horizontal() => new(LayoutDirection.Horizontal);

    /// <summary>
    /// Flex is a pure container — children draw themselves; this widget draws nothing.
    /// </summary>
    public override void Draw(ICanvas canvas) { }

    /// <summary>
    /// Runs the 7-pass algorithm over this widget's subtree, then positions children
    /// in parent-relative coordinates and recurses into each child's Layout().
    ///
    /// availableWidth/availableHeight are the dimensions the parent is giving us.
    /// We apply our own sizing mode (Fixed/Grow/Fit) to decide our final size, then
    /// distribute whatever interior space remains to our children.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        // Passes 1-6 compute Width/Height for every node in our subtree.
        Pass1_FitWidth(this);
        Pass2_ApplyMinMaxWidth(this);
        Pass3_GrowShrinkWidth(this);
        Pass4_FitHeight(this);
        Pass5_ApplyMinMaxHeight(this);
        Pass6_GrowShrinkHeight(this);

        // Apply our own sizing relative to the available space given by our parent.
        // This must happen after the bottom-up passes so we know our children's natural
        // sizes, and before Pass 7 so position offsets are based on our final size.
        ApplyOwnSizing(availableWidth, availableHeight);

        // Pass 7 sets child X/Y in parent-relative coordinates (origin = our top-left)
        // and then calls child.Layout() so nested containers recurse.
        Pass7_Position(this);
    }

    // --- Private pass implementations ---

    private static void Pass1_FitWidth(Widget root) =>
        Traversal.ReverseBreadthFirst(root, widget =>
        {
            widget.Width = widget.HorizontalSizing switch
            {
                SizingMode.Fixed => widget.FixedWidth,
                _ => Calculations.CalculateDimension(widget, isWidth: true)
            };
        });

    private static void Pass2_ApplyMinMaxWidth(Widget root) =>
        Traversal.ReverseBreadthFirst(root, widget =>
        {
            if (widget.MinWidth > 0)
                widget.Width = Math.Max(widget.Width, widget.MinWidth);
            if (widget.MaxWidth > 0)
                widget.Width = Math.Min(widget.Width, widget.MaxWidth);
        });

    private static void Pass3_GrowShrinkWidth(Widget root) =>
        Traversal.BreadthFirst(root, widget => GrowShrink.Apply(widget, isWidth: true));

    private static void Pass4_FitHeight(Widget root) =>
        Traversal.ReverseBreadthFirst(root, widget =>
        {
            widget.Height = widget.VerticalSizing switch
            {
                SizingMode.Fixed => widget.FixedHeight,
                _ => Calculations.CalculateDimension(widget, isWidth: false)
            };
        });

    private static void Pass5_ApplyMinMaxHeight(Widget root) =>
        Traversal.ReverseBreadthFirst(root, widget =>
        {
            if (widget.MinHeight > 0)
                widget.Height = Math.Max(widget.Height, widget.MinHeight);
            if (widget.MaxHeight > 0)
                widget.Height = Math.Min(widget.Height, widget.MaxHeight);
        });

    private static void Pass6_GrowShrinkHeight(Widget root) =>
        Traversal.BreadthFirst(root, widget => GrowShrink.Apply(widget, isWidth: false));

    /// <summary>
    /// Applies this Flex's own sizing mode relative to the available space provided
    /// by the parent. Grow fills the available space; Fixed uses the declared size;
    /// Fit keeps whatever the bottom-up passes computed.
    /// Called after passes 1-6 so natural child sizes are already known.
    /// </summary>
    private void ApplyOwnSizing(int availableWidth, int availableHeight)
    {
        Width = HorizontalSizing switch
        {
            SizingMode.Fixed => FixedWidth,
            SizingMode.Grow  => availableWidth,
            _                => Width // Fit: already set by Pass 1
        };
        Height = VerticalSizing switch
        {
            SizingMode.Fixed => FixedHeight,
            SizingMode.Grow  => availableHeight,
            _                => Height // Fit: already set by Pass 4
        };
    }

    /// <summary>
    /// Sets each child's X/Y in parent-relative coordinates (origin = our top-left
    /// content area, after margin/padding). Then calls each child's Layout() so the
    /// tree recurses depth-first with the child's final dimensions already known.
    /// </summary>
    private static void Pass7_Position(Widget container)
    {
        // Start at the inset origin: skip over our own margin and padding.
        var x = container.Margin.Left + container.Padding.Left;
        var y = container.Margin.Top  + container.Padding.Top;

        foreach (var child in container.Children)
        {
            child.X = x;
            child.Y = y;

            if (container.Direction == LayoutDirection.Horizontal)
                x += child.Width + container.ChildGap;
            else
                y += child.Height + container.ChildGap;

            // Recurse: give the child its assigned dimensions so it can position
            // its own children and call Layout() on them in turn.
            child.Layout(child.Width, child.Height);
        }
    }
}
