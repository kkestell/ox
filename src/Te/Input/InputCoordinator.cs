using System.Collections.Concurrent;

namespace Te.Input;

/// <summary>
/// Minimal input coordinator that serializes keys and mouse events into one
/// processing stream.
/// ConsoleEx's original coordinator also owned focus, drag, resize, portal, and
/// window-routing policy. Te deliberately stops at queueing and dispatch because
/// those higher-level behaviors should belong to whatever UI model grows on top.
/// </summary>
public sealed class InputCoordinator : IDisposable
{
    private readonly ConcurrentQueue<InputEvent> _pending = new();
    private IInputSource? _inputSource;

    public InputCoordinator(IInputSource? inputSource = null)
    {
        if (inputSource is not null)
            Attach(inputSource);
    }

    public int PendingInputCount => _pending.Count;

    public event EventHandler<InputEvent>? InputReceived;
    public event EventHandler<KeyEventArgs>? KeyReceived;
    public event EventHandler<MouseEventArgs>? MouseReceived;

    public void Attach(IInputSource inputSource)
    {
        ArgumentNullException.ThrowIfNull(inputSource);

        if (ReferenceEquals(_inputSource, inputSource))
            return;

        Detach();
        _inputSource = inputSource;
        _inputSource.KeyPressed += OnKeyPressed;
        _inputSource.MouseEvent += OnMouseEvent;
    }

    public void Detach()
    {
        if (_inputSource is null)
            return;

        _inputSource.KeyPressed -= OnKeyPressed;
        _inputSource.MouseEvent -= OnMouseEvent;
        _inputSource = null;
    }

    public void Enqueue(InputEvent inputEvent)
    {
        ArgumentNullException.ThrowIfNull(inputEvent);
        _pending.Enqueue(inputEvent);
    }

    public void EnqueueKey(KeyEventArgs keyEvent) => Enqueue(new KeyInputEvent(keyEvent));

    public void EnqueueMouse(MouseEventArgs mouseEvent) => Enqueue(new MouseInputEvent(mouseEvent));

    public void ProcessPendingInput(CancellationToken cancellationToken = default)
    {
        while (!cancellationToken.IsCancellationRequested && _pending.TryDequeue(out var inputEvent))
        {
            InputReceived?.Invoke(this, inputEvent);

            switch (inputEvent)
            {
                case KeyInputEvent keyInput:
                    KeyReceived?.Invoke(this, keyInput.Key);
                    break;
                case MouseInputEvent mouseInput:
                    MouseReceived?.Invoke(this, mouseInput.Mouse);
                    break;
            }
        }
    }

    public void Dispose() => Detach();

    private void OnKeyPressed(object? sender, KeyEventArgs e) => EnqueueKey(e);

    private void OnMouseEvent(object? sender, MouseEventArgs e) => EnqueueMouse(e);
}
