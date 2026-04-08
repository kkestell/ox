namespace Te.Input;

/// <summary>
/// Common event envelope used by the coordinator queue.
/// A single ordered queue keeps keyboard and mouse events in arrival order,
/// which matters once callers start mixing shortcuts, hover, and click behavior.
/// </summary>
public abstract record InputEvent;

public sealed record KeyInputEvent(KeyEventArgs Key) : InputEvent;

public sealed record MouseInputEvent(MouseEventArgs Mouse) : InputEvent;
