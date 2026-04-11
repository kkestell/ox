namespace Te.Input;

/// <summary>
/// Minimal mouse event payload.
/// The original ConsoleEx type also referenced the active window, but that would
/// force a windowing abstraction into Te. This version keeps only portable event
/// data so the project can stay a reusable foundation.
/// </summary>
public sealed class MouseEventArgs : EventArgs
{
    public IReadOnlyList<MouseFlags> Flags { get; }
    public Point Position { get; }
    public Point AbsolutePosition { get; }
    public Point WindowPosition { get; }
    public bool Handled { get; set; }

    public MouseEventArgs(
        IEnumerable<MouseFlags> flags,
        Point position,
        Point? absolutePosition = null,
        Point? windowPosition = null)
    {
        ArgumentNullException.ThrowIfNull(flags);

        Flags = [.. flags];
        Position = position;
        AbsolutePosition = absolutePosition ?? position;
        WindowPosition = windowPosition ?? position;
    }

    public bool HasFlag(MouseFlags flag)
    {
        if (flag == MouseFlags.None)
            return false;

        return Flags.Any(candidate => (candidate & flag) == flag);
    }

    public bool HasAnyFlag(params MouseFlags[] flags)
    {
        ArgumentNullException.ThrowIfNull(flags);
        return flags.Any(HasFlag);
    }

    public MouseEventArgs WithPosition(Point position) =>
        new(Flags, position, AbsolutePosition, WindowPosition)
        {
            Handled = Handled,
        };

    public MouseEventArgs WithFlags(params MouseFlags[] additionalFlags)
    {
        ArgumentNullException.ThrowIfNull(additionalFlags);
        return new(Flags.Concat(additionalFlags), Position, AbsolutePosition, WindowPosition)
        {
            Handled = Handled,
        };
    }

    public MouseEventArgs WithReplacedFlags(params MouseFlags[] replacementFlags)
    {
        ArgumentNullException.ThrowIfNull(replacementFlags);
        return new(replacementFlags, Position, AbsolutePosition, WindowPosition)
        {
            Handled = Handled,
        };
    }
}
