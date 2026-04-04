namespace Ur.Widgets;

/// <summary>
/// Defines how a widget should size itself within its parent container.
/// This is the core concept that determines widget behavior in layouts.
/// </summary>
public enum SizingMode
{
    /// <summary>
    /// Size based on content/children (natural size).
    /// This is the default mode - the widget takes only as much space as it needs.
    /// </summary>
    Fit,

    /// <summary>
    /// Use explicit fixed size.
    /// The widget will be exactly the specified dimension regardless of available space.
    /// </summary>
    Fixed,

    /// <summary>
    /// Expand to fill available space in parent.
    /// The widget will grow to take any extra space in its container.
    /// </summary>
    Grow
}
