namespace Ur.Widgets;

/// <summary>
/// Core layout calculations for widget dimensions.
/// These helpers compute natural sizes based on content and children.
/// The box model is simplified to margin + padding + content — borders are
/// a purely visual concern handled by individual widgets during drawing.
/// </summary>
internal static class Calculations
{
    /// <summary>
    /// Calculates the natural dimension (width or height) for a widget.
    /// For containers: sums children on-axis, takes max cross-axis.
    /// For leaves: uses preferred dimension from content.
    /// </summary>
    public static int CalculateDimension(Widget widget, bool isWidth)
    {
        var extraSpace = GetExtraSpace(widget, isWidth);
        var onAxis = IsOnAxis(widget, isWidth);

        if (widget.Children.Count == 0)
        {
            return extraSpace + (isWidth ? widget.PreferredWidth : widget.PreferredHeight);
        }

        var gapSpace = widget.Children.Count > 1 ? widget.ChildGap * (widget.Children.Count - 1) : 0;

        if (onAxis)
        {
            var childrenSize = widget.Children.Sum(c => isWidth ? c.Width : c.Height);
            return extraSpace + gapSpace + childrenSize;
        }
        else
        {
            var maxChildSize = widget.Children.Max(c => isWidth ? c.Width : c.Height);
            return extraSpace + maxChildSize;
        }
    }

    /// <summary>
    /// Gets the available interior space for children within a widget.
    /// Accounts for margin and padding (no border — that is a drawing concern).
    /// </summary>
    public static int GetAvailableSpace(Widget widget, bool isWidth)
    {
        var extraSpace = GetExtraSpace(widget, isWidth);
        var dimension = isWidth ? widget.Width : widget.Height;
        return Math.Max(0, dimension - extraSpace);
    }

    /// <summary>
    /// Determines if the dimension being calculated is along the layout direction.
    /// For horizontal layout: width is on-axis, height is cross-axis.
    /// For vertical layout: height is on-axis, width is cross-axis.
    /// </summary>
    public static bool IsOnAxis(Widget widget, bool isWidth) =>
        (widget.Direction == LayoutDirection.Horizontal && isWidth) ||
        (widget.Direction == LayoutDirection.Vertical && !isWidth);

    /// <summary>
    /// Gets the extra space consumed by margin and padding on one axis.
    /// This is the space a widget reserves around its content area,
    /// distinct from the space consumed by its children.
    /// </summary>
    public static int GetExtraSpace(Widget widget, bool isWidth)
    {
        int margin = isWidth
            ? widget.Margin.Left + widget.Margin.Right
            : widget.Margin.Top + widget.Margin.Bottom;

        int padding = isWidth
            ? widget.Padding.Left + widget.Padding.Right
            : widget.Padding.Top + widget.Padding.Bottom;

        return margin + padding;
    }
}
