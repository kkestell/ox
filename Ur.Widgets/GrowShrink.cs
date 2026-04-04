namespace Ur.Widgets;

/// <summary>
/// Implements the grow/shrink logic for adjusting widget dimensions.
/// This is the heart of the flexible layout system - it handles overflow and extra space distribution.
/// </summary>
internal static class GrowShrink
{
    /// <summary>
    /// Applies grow/shrink logic to a container's children.
    /// Different logic for on-axis (layout direction) vs cross-axis.
    /// </summary>
    public static void Apply(Widget container, bool isWidth)
    {
        if (container.Children.Count == 0) return;

        var availableSpace = Calculations.GetAvailableSpace(container, isWidth);
        var onAxis = Calculations.IsOnAxis(container, isWidth);

        if (onAxis)
            ApplyOnAxis(container, isWidth, availableSpace);
        else
            ApplyCrossAxis(container, isWidth, availableSpace);
    }

    private static void ApplyOnAxis(Widget container, bool isWidth, int availableSpace)
    {
        var gapSpace = container.Children.Count > 1 ? container.ChildGap * (container.Children.Count - 1) : 0;
        var childrenSize = container.Children.Sum(c => isWidth ? c.Width : c.Height) + gapSpace;

        if (availableSpace < childrenSize && childrenSize > 0)
            ShrinkOnAxis(container, isWidth, availableSpace, childrenSize);
        else if (availableSpace > childrenSize)
            GrowOnAxis(container, isWidth, availableSpace, childrenSize);
    }

    private static void ShrinkOnAxis(Widget container, bool isWidth, int availableSpace, int childrenSize)
    {
        var overflow = childrenSize - availableSpace;
        var shrinkableChildren = container.Children
            .Where(c => GetSizing(c, isWidth) != SizingMode.Fixed)
            .ToList();

        var totalShrinkableSpace = shrinkableChildren.Sum(c => isWidth ? c.Width : c.Height);
        if (totalShrinkableSpace == 0) return;

        foreach (var child in shrinkableChildren)
        {
            var currentDim = isWidth ? child.Width : child.Height;
            var min = isWidth ? child.MinWidth : child.MinHeight;
            var maxShrinkable = min > 0 ? currentDim - min : currentDim;

            if (maxShrinkable <= 0) continue;

            var proportion = (double)currentDim / totalShrinkableSpace;
            var reduction = (int)Math.Ceiling(overflow * proportion);
            var actualReduction = Math.Min(reduction, maxShrinkable);

            SetDimension(child, isWidth, currentDim - actualReduction);
        }
    }

    private static void GrowOnAxis(Widget container, bool isWidth, int availableSpace, int childrenSize)
    {
        var growChildren = container.Children
            .Where(c => GetSizing(c, isWidth) == SizingMode.Grow)
            .ToList();

        if (growChildren.Count == 0) return;

        var extraSpace = availableSpace - childrenSize;
        var spacePerChild = extraSpace / growChildren.Count;
        var remainder = extraSpace % growChildren.Count;

        for (var i = 0; i < growChildren.Count; i++)
        {
            var child = growChildren[i];
            var currentDim = isWidth ? child.Width : child.Height;
            var max = isWidth ? child.MaxWidth : child.MaxHeight;
            var extra = spacePerChild + (i < remainder ? 1 : 0);

            var actualGrowth = max > 0 ? Math.Min(extra, max - currentDim) : extra;
            if (actualGrowth > 0)
                SetDimension(child, isWidth, currentDim + actualGrowth);
        }
    }

    private static void ApplyCrossAxis(Widget container, bool isWidth, int availableSpace)
    {
        foreach (var child in container.Children)
        {
            var sizing = GetSizing(child, isWidth);
            if (sizing == SizingMode.Fixed) continue;

            var currentDim = isWidth ? child.Width : child.Height;
            var min = isWidth ? child.MinWidth : child.MinHeight;
            var max = isWidth ? child.MaxWidth : child.MaxHeight;

            if (currentDim > availableSpace)
            {
                var newDim = Math.Max(availableSpace, min);
                SetDimension(child, isWidth, newDim);
            }
            else if (sizing == SizingMode.Grow && currentDim < availableSpace)
            {
                var newDim = max > 0 ? Math.Min(availableSpace, max) : availableSpace;
                SetDimension(child, isWidth, newDim);
            }
        }
    }

    private static SizingMode GetSizing(Widget widget, bool isWidth) =>
        isWidth ? widget.HorizontalSizing : widget.VerticalSizing;

    private static void SetDimension(Widget widget, bool isWidth, int value)
    {
        if (isWidth)
            widget.Width = value;
        else
            widget.Height = value;
    }
}
