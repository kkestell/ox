using System.Collections.Concurrent;
using Ur.Terminal.Terminal;

namespace Ur.Terminal.Input;

public sealed class KeyReader
{
    private readonly ITerminal _terminal;
    private readonly ConcurrentQueue<KeyEvent> _queue = new();
    private Thread? _thread;
    private CancellationTokenSource? _cts;

    public KeyReader(ITerminal terminal)
    {
        _terminal = terminal;
    }

    public void Start(CancellationToken ct)
    {
        _cts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        _thread = new Thread(() => ReadLoop(_cts.Token))
        {
            IsBackground = true,
            Name = "KeyReader",
        };
        _thread.Start();
    }

    public void Drain(List<KeyEvent> output)
    {
        while (_queue.TryDequeue(out var key))
            output.Add(key);
    }

    public void Stop()
    {
        _cts?.Cancel();
        _thread?.Join(timeout: TimeSpan.FromMilliseconds(500));
    }

    private void ReadLoop(CancellationToken ct)
    {
        using var input = _terminal.OpenInput();
        var buffer = new byte[256];
        var pending = 0;

        while (!ct.IsCancellationRequested)
        {
            int bytesRead;
            try
            {
                bytesRead = input.Read(buffer, pending, buffer.Length - pending);
            }
            catch (OperationCanceledException)
            {
                break;
            }
            catch (ObjectDisposedException)
            {
                break;
            }

            if (bytesRead == 0)
                break;

            pending += bytesRead;
            var span = buffer.AsSpan(0, pending);

            while (span.Length > 0)
            {
                var result = KeyParser.Parse(span, out var consumed);
                if (result == null)
                    break;

                _queue.Enqueue(result.Value);
                span = span[consumed..];
            }

            // Shift unconsumed bytes to the start
            var remaining = span.Length;
            if (remaining > 0 && pending - remaining > 0)
                span.CopyTo(buffer);
            pending = remaining;
        }
    }
}
