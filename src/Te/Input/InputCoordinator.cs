using System.Threading.Channels;

namespace Te.Input;

/// <summary>
/// Minimal input coordinator that serializes keys and mouse events into one
/// processing stream backed by an unbounded <see cref="Channel{T}"/>.
///
/// Events are fired eagerly on the calling thread (the background stdin reader)
/// as soon as they are enqueued, enabling immediate escape-key detection during
/// turns. Consumers that need serial processing (InputReader) read from
/// <see cref="Reader"/> on their own thread rather than subscribing to events.
///
/// ConsoleEx's original coordinator also owned focus, drag, resize, portal, and
/// window-routing policy. Te deliberately stops at queueing and dispatch because
/// those higher-level behaviors should belong to whatever UI model grows on top.
/// </summary>
public sealed class InputCoordinator : IDisposable
{
    // Unbounded channel replaces the old ConcurrentQueue. Writers never block;
    // readers await WaitToReadAsync which wakes instantly on enqueue — no polling.
    private readonly Channel<InputEvent> _channel = Channel.CreateUnbounded<InputEvent>();
    private IInputSource? _inputSource;

    public InputCoordinator(IInputSource? inputSource = null)
    {
        if (inputSource is not null)
            Attach(inputSource);
    }

    /// <summary>
    /// Exposes the read side of the channel so consumers (InputReader) can
    /// await input without polling. Only one consumer should read at a time.
    /// </summary>
    public ChannelReader<InputEvent> Reader => _channel.Reader;

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

    /// <summary>
    /// Writes the event to the channel AND fires the corresponding event
    /// immediately on the calling thread (the background stdin reader).
    /// This dual dispatch is the key design choice: the channel gives
    /// InputReader a serial, awaitable stream; the events give escape
    /// monitoring instant notification without channel reads.
    /// </summary>
    public void Enqueue(InputEvent inputEvent)
    {
        ArgumentNullException.ThrowIfNull(inputEvent);
        _channel.Writer.TryWrite(inputEvent);

        // Fire events eagerly on the caller's thread so subscribers
        // (e.g. escape handler in Program.cs) react instantly.
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

    public void EnqueueKey(KeyEventArgs keyEvent) => Enqueue(new KeyInputEvent(keyEvent));

    public void EnqueueMouse(MouseEventArgs mouseEvent) => Enqueue(new MouseInputEvent(mouseEvent));

    /// <summary>
    /// Completes the channel writer so any pending WaitToReadAsync unblocks,
    /// then detaches from the input source.
    /// </summary>
    public void Dispose()
    {
        _channel.Writer.TryComplete();
        Detach();
    }

    private void OnKeyPressed(object? sender, KeyEventArgs e) => EnqueueKey(e);

    private void OnMouseEvent(object? sender, MouseEventArgs e) => EnqueueMouse(e);
}
