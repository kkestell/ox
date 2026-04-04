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
        // Width axis: compute natural widths bottom-up, fix our own width, then
        // distribute to Grow children. Our own width must be set before Pass 3 so
        // the GrowShrink pass knows the actual space available for Grow children
        // (e.g. a root Flex with Grow sizing must reflect the terminal width before
        // distributing that width to child widgets).
        Pass1_FitWidth(this);
        if (Parent == null) MaybeDumpTree(this, "1: FitWidth");
        Pass2_ApplyMinMaxWidth(this);
        if (Parent == null) MaybeDumpTree(this, "2: ApplyMinMaxWidth");
        ApplyOwnWidth(availableWidth);
        if (Parent == null) MaybeDumpTree(this, "3a: ApplyOwnWidth");
        Pass3_GrowShrinkWidth(this);
        if (Parent == null) MaybeDumpTree(this, "3b: GrowShrinkWidth");

        // Height axis: same pattern. Natural heights must come after widths are
        // final so that wrapping widgets can report the correct line count.
        Pass4_FitHeight(this);
        if (Parent == null) MaybeDumpTree(this, "4: FitHeight");
        Pass5_ApplyMinMaxHeight(this);
        if (Parent == null) MaybeDumpTree(this, "5: ApplyMinMaxHeight");
        ApplyOwnHeight(availableHeight);
        if (Parent == null) MaybeDumpTree(this, "6a: ApplyOwnHeight");
        Pass6_GrowShrinkHeight(this);
        if (Parent == null) MaybeDumpTree(this, "6b: GrowShrinkHeight");

        // Pass 7 sets child X/Y in parent-relative coordinates (origin = our top-left)
        // and then calls child.Layout() so nested containers recurse.
        Pass7_Position(this);

        // Debug aid: when OX_DUMP_TREE is set to a positive integer and this is the
        // root Flex (Parent == null means no container owns us), print the full widget
        // tree to stderr. The value is the maximum depth to descend (e.g. "5" shows
        // up to 5 levels). Invalid values and 0 are silently ignored so the env var
        // can be left set without affecting normal operation.
        if (Parent == null)
            MaybeDumpTree(this, "7: Position");
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
    /// Fixes our own width before Pass 3 distributes space to Grow children.
    /// Grow takes the available space from the parent; Fixed uses the declared size;
    /// Fit keeps the natural width already computed by Pass 1.
    /// </summary>
    private void ApplyOwnWidth(int availableWidth)
    {
        Width = HorizontalSizing switch
        {
            SizingMode.Fixed => FixedWidth,
            SizingMode.Grow  => availableWidth,
            _                => Width // Fit: already set by Pass 1
        };
    }

    /// <summary>
    /// Fixes our own height before Pass 6 distributes space to Grow children.
    /// Same logic as ApplyOwnWidth but on the vertical axis.
    /// </summary>
    private void ApplyOwnHeight(int availableHeight)
    {
        Height = VerticalSizing switch
        {
            SizingMode.Fixed => FixedHeight,
            SizingMode.Grow  => availableHeight,
            _                => Height // Fit: already set by Pass 4
        };
    }

    /// <summary>
    /// Prints the widget tree to stderr when OX_DUMP_TREE is set to a positive
    /// integer. Called once per frame on the root Flex (Parent == null). Each line
    /// shows the widget type, its absolute screen position (accumulated from root),
    /// its size, and its horizontal/vertical sizing mode. Children are indented by
    /// two spaces per level; the env var value caps how deep the dump descends.
    /// </summary>
    private static void MaybeDumpTree(Widget root, string label = "")
    {
        var envVal = Environment.GetEnvironmentVariable("OX_DUMP_TREE");
        if (!int.TryParse(envVal, out var maxDepth) || maxDepth < 1)
            return;

        var header = string.IsNullOrEmpty(label) ? "=== OX_DUMP_TREE ===" : $"=== OX_DUMP_TREE: {label} ===";
        System.Console.Error.WriteLine(header);
        DumpWidget(root, depth: 0, maxDepth: maxDepth, absX: 0, absY: 0);
    }

    private static void DumpWidget(Widget widget, int depth, int maxDepth, int absX, int absY)
    {
        if (depth > maxDepth)
            return;

        var indent  = new string(' ', depth * 2);
        var name    = widget.GetType().Name;
        var screenX = absX + widget.X;
        var screenY = absY + widget.Y;

        System.Console.Error.WriteLine(
            $"{indent}{name} pos=({screenX},{screenY}) size={widget.Width}x{widget.Height} " +
            $"h={widget.HorizontalSizing} v={widget.VerticalSizing}");

        // Children have X/Y in this widget's coordinate space, so accumulate.
        foreach (var child in widget.Children)
            DumpWidget(child, depth + 1, maxDepth, screenX, screenY);
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
