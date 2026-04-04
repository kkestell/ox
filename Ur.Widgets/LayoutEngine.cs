namespace Ur.Widgets;

using System.Text.Json;

/// <summary>
/// The main layout engine that orchestrates the 7-pass layout algorithm.
/// Each pass has a specific purpose and must run in the correct order.
///
/// The algorithm:
/// 1. FitWidth - Calculate natural widths bottom-up
/// 2. ApplyMinMaxWidth - Enforce width constraints bottom-up
/// 3. GrowShrinkWidth - Adjust widths to fit available space top-down
/// 4. FitHeight - Calculate natural heights bottom-up (now widths are final)
/// 5. ApplyMinMaxHeight - Enforce height constraints bottom-up
/// 6. GrowShrinkHeight - Adjust heights to fit available space top-down
/// 7. Position - Calculate final x,y coordinates top-down
/// </summary>
public static class LayoutEngine
{
    /// <summary>
    /// Runs the complete 7-pass layout algorithm on a widget tree.
    /// After this call, all widgets have their final X, Y, Width, and Height set.
    /// </summary>
    public static void Layout(Widget root, int stopAfterPass = 7)
    {
        ArgumentNullException.ThrowIfNull(root);

        var dumpAfterPass = -1;
        var envValue = Environment.GetEnvironmentVariable("OX_DUMP_TREE");
        if (int.TryParse(envValue, out var dumpVal) && dumpVal > 0 && dumpVal < 8)
            dumpAfterPass = dumpVal;

        var effectivePass = dumpAfterPass >= 0 ? dumpAfterPass : stopAfterPass;

        if (effectivePass < 1) return;
        Pass1_FitWidth(root);
        if (effectivePass < 2) { if (dumpAfterPass >= 0) DumpTreeJson(root); return; }
        Pass2_ApplyMinMaxWidth(root);
        if (effectivePass < 3) { if (dumpAfterPass >= 0) DumpTreeJson(root); return; }
        Pass3_GrowShrinkWidth(root);
        if (effectivePass < 4) { if (dumpAfterPass >= 0) DumpTreeJson(root); return; }
        Pass4_FitHeight(root);
        if (effectivePass < 5) { if (dumpAfterPass >= 0) DumpTreeJson(root); return; }
        Pass5_ApplyMinMaxHeight(root);
        if (effectivePass < 6) { if (dumpAfterPass >= 0) DumpTreeJson(root); return; }
        Pass6_GrowShrinkHeight(root);
        if (effectivePass < 7) { if (dumpAfterPass >= 0) DumpTreeJson(root); return; }
        Pass7_Position(root);
        if (dumpAfterPass >= 0 && dumpAfterPass == 7) DumpTreeJson(root);
    }

    private static void DumpTreeJson(Widget root)
    {
        var options = new JsonSerializerOptions { WriteIndented = true };
        var obj = WidgetToObject(root);
        var json = JsonSerializer.Serialize(obj, options);
        System.Console.Error.WriteLine(json);
    }

    private static object WidgetToObject(Widget w)
    {
        var children = w.Children.Select(WidgetToObject).ToList();
        var name = w.GetType().Name;

        var baseObj = new Dictionary<string, object>
        {
            { "type", name },
            { "x", w.X },
            { "y", w.Y },
            { "width", w.Width },
            { "height", w.Height },
        };

        if (w is Label l)
            baseObj["text"] = l.Text;

        if (children.Count > 0)
            baseObj["children"] = children;

        return baseObj;
    }

    /// <summary>
    /// Layouts a widget with explicit size constraints and offset.
    /// Used by the rendering system to layout within a specific screen region.
    /// </summary>
    public static void LayoutWithConstraints(Widget widget, int offsetX, int offsetY, int availableWidth, int availableHeight)
    {
        var (oldHSizing, oldVSizing, oldFixedWidth, oldFixedHeight) =
            (widget.HorizontalSizing, widget.VerticalSizing, widget.FixedWidth, widget.FixedHeight);

        if (availableWidth > 0)
        {
            widget.HorizontalSizing = SizingMode.Fixed;
            widget.FixedWidth = availableWidth;
        }
        if (availableHeight > 0)
        {
            widget.VerticalSizing = SizingMode.Fixed;
            widget.FixedHeight = availableHeight;
        }

        Layout(widget);

        widget.X = offsetX;
        widget.Y = offsetY;
        Pass7_Position(widget);

        (widget.HorizontalSizing, widget.VerticalSizing, widget.FixedWidth, widget.FixedHeight) =
            (oldHSizing, oldVSizing, oldFixedWidth, oldFixedHeight);
    }

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

    private static void Pass7_Position(Widget root)
    {
        root.X = root.X != 0 ? root.X : 0;
        root.Y = root.Y != 0 ? root.Y : 0;

        Traversal.BreadthFirst(root, widget =>
        {
            var x = widget.X + widget.Margin.Left + widget.Padding.Left;
            var y = widget.Y + widget.Margin.Top + widget.Padding.Top;

            foreach (var child in widget.Children)
            {
                child.X = x;
                child.Y = y;

                if (widget.Direction == LayoutDirection.Horizontal)
                    x += child.Width + widget.ChildGap;
                else
                    y += child.Height + widget.ChildGap;
            }
        });
    }
}
