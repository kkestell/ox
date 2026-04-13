namespace Ox.Terminal.Input;

/// <summary>
/// Small adapter boundary between Te and any concrete console input backend.
/// This is intentionally narrower than ConsoleEx's driver abstraction because
/// the minimal extraction only needs event ingress, not terminal ownership.
/// </summary>
public interface IInputSource
{
    event EventHandler<KeyEventArgs>? KeyPressed;
    event EventHandler<MouseEventArgs>? MouseEvent;
}
