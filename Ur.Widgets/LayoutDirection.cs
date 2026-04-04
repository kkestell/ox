namespace Ur.Widgets;

/// <summary>
/// Defines how child widgets are arranged within a container widget.
/// This determines the primary layout axis for container widgets.
/// </summary>
public enum LayoutDirection
{
    /// <summary>
    /// Stack children vertically (top to bottom).
    /// Children are arranged in a column, with widths typically independent
    /// and heights summed along the vertical axis.
    /// </summary>
    Vertical,

    /// <summary>
    /// Stack children horizontally (left to right).
    /// Children are arranged in a row, with heights typically independent
    /// and widths summed along the horizontal axis.
    /// </summary>
    Horizontal
}
