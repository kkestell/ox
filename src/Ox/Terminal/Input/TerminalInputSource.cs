namespace Ox.Terminal.Input;

/// <summary>
/// Built-in terminal-backed input source for Te.
/// On Unix-like terminals it reads raw stdin bytes and parses ANSI key/mouse
/// sequences so mouse support can exist independently of the demo. On other
/// platforms it falls back to managed keyboard polling until a native mouse
/// implementation is added.
/// </summary>
public sealed class TerminalInputSource : IInputSource, IDisposable
{
    private const string EnableBasicMouse = "\u001b[?1000h";
    private const string EnableButtonTracking = "\u001b[?1002h";
    private const string EnableAnyMotion = "\u001b[?1003h";
    private const string EnableSgrMouse = "\u001b[?1006h";
    private const string DisableAnyMotion = "\u001b[?1003l";
    private const string DisableButtonTracking = "\u001b[?1002l";
    private const string DisableSgrMouse = "\u001b[?1006l";
    private const string DisableBasicMouse = "\u001b[?1000l";

    private readonly TerminalInputSourceOptions _options;
    private readonly CancellationTokenSource _cancellation = new();
    private readonly Task _readerTask;
    private readonly bool _usesUnixRawMode;
    private bool _disposed;
    private bool _mouseEnabled;

    public TerminalInputSource(TerminalInputSourceOptions? options = null)
    {
        _options = options ?? new TerminalInputSourceOptions();
        _usesUnixRawMode = UnixTerminalRawMode.IsSupported && !Console.IsInputRedirected;

        if (_usesUnixRawMode)
        {
            UnixTerminalRawMode.Enable();
            EnableMouseIfRequested();
            _readerTask = Task.Run(ReadUnixLoop);
        }
        else
        {
            _readerTask = Task.Run(ReadManagedLoop);
        }
    }

    public event EventHandler<KeyEventArgs>? KeyPressed;
    public event EventHandler<MouseEventArgs>? MouseEvent;

    public void Dispose()
    {
        if (_disposed)
            return;

        _disposed = true;
        _cancellation.Cancel();

        try
        {
            _readerTask.Wait(TimeSpan.FromMilliseconds(Math.Max(100, _options.PollTimeoutMs * 4)));
        }
        catch (AggregateException)
        {
        }
        finally
        {
            if (_mouseEnabled)
                DisableMouse();

            if (_usesUnixRawMode)
                UnixTerminalRawMode.Disable();

            _cancellation.Dispose();
        }
    }

    private void ReadManagedLoop()
    {
        while (!_cancellation.IsCancellationRequested)
        {
            if (!Console.KeyAvailable)
            {
                Thread.Sleep(_options.PollTimeoutMs);
                continue;
            }

            var key = Console.ReadKey(intercept: true);
            KeyPressed?.Invoke(this, KeyEventArgs.FromConsoleKeyInfo(key));
        }
    }

    private void ReadUnixLoop()
    {
        var parser = new AnsiInputParser();

        while (!_cancellation.IsCancellationRequested)
        {
            var nextByte = UnixTerminalRawMode.ReadByteWithTimeout(_options.PollTimeoutMs);
            if (nextByte < 0)
            {
                Dispatch(parser.Flush());
                continue;
            }

            Span<byte> singleByte = [(byte)nextByte];
            Dispatch(parser.Parse(singleByte));
        }

        Dispatch(parser.Flush());
    }

    private void Dispatch(IEnumerable<InputEvent> events)
    {
        foreach (var inputEvent in events)
        {
            switch (inputEvent)
            {
                case KeyInputEvent keyInput:
                    KeyPressed?.Invoke(this, keyInput.Key);
                    break;
                case MouseInputEvent mouseInput:
                    MouseEvent?.Invoke(this, mouseInput.Mouse);
                    break;
            }
        }
    }

    private void EnableMouseIfRequested()
    {
        if (!_options.EnableMouse || Console.IsOutputRedirected)
            return;

        Console.Write(EnableBasicMouse);
        Console.Write(EnableButtonTracking);
        if (_options.EnableMouseMove)
            Console.Write(EnableAnyMotion);
        Console.Write(EnableSgrMouse);
        Console.Out.Flush();
        _mouseEnabled = true;
    }

    private void DisableMouse()
    {
        Console.Write(DisableAnyMotion);
        Console.Write(DisableButtonTracking);
        Console.Write(DisableSgrMouse);
        Console.Write(DisableBasicMouse);
        Console.Out.Flush();
        _mouseEnabled = false;
    }
}
