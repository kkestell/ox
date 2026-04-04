namespace Ur.Widgets;

/// <summary>
/// Defines external spacing around a widget.
/// Margin creates empty space between a widget and its siblings or parent.
/// All values are in character cells.
/// </summary>
/// <param name="Top">Top margin in cells</param>
/// <param name="Right">Right margin in cells</param>
/// <param name="Bottom">Bottom margin in cells</param>
/// <param name="Left">Left margin in cells</param>
public record Margin(ushort Top, ushort Right, ushort Bottom, ushort Left)
{
    /// <summary>
    /// Creates a Margin with the same value on all sides.
    /// </summary>
    public static Margin All(ushort value) => new(value, value, value, value);

    /// <summary>
    /// Zero margin (no spacing).
    /// </summary>
    public static Margin None => new(0, 0, 0, 0);
}

/// <summary>
/// Defines which sides of a widget should have a border.
/// Borders are drawn between margin (outside) and padding (inside).
/// </summary>
/// <param name="Top">Whether to draw top border</param>
/// <param name="Right">Whether to draw right border</param>
/// <param name="Bottom">Whether to draw bottom border</param>
/// <param name="Left">Whether to draw left border</param>
public record Border(bool Top, bool Right, bool Bottom, bool Left)
{
    /// <summary>
    /// Creates a Border on all sides.
    /// </summary>
    public static Border All(bool value) => new(value, value, value, value);

    /// <summary>
    /// No border on any side.
    /// </summary>
    public static Border None => new(false, false, false, false);
}

/// <summary>
/// Defines internal spacing around a widget's content area.
/// Padding creates space inside the widget around its children or content.
/// All values are in character cells.
/// </summary>
/// <param name="Top">Top padding in cells</param>
/// <param name="Right">Right padding in cells</param>
/// <param name="Bottom">Bottom padding in cells</param>
/// <param name="Left">Left padding in cells</param>
public record Padding(ushort Top, ushort Right, ushort Bottom, ushort Left)
{
    /// <summary>
    /// Creates a Padding with the same value on all sides.
    /// </summary>
    public static Padding All(ushort value) => new(value, value, value, value);

    /// <summary>
    /// Zero padding (no internal spacing).
    /// </summary>
    public static Padding None => new(0, 0, 0, 0);
}
