using System.Collections.Concurrent;
using Ur.Terminal.Terminal;

namespace Ur.Terminal.Input;

public sealed class KeyReader
{
    private readonly ITerminal _terminal;
    private readonly Action<string>? _trace;
    private readonly ConcurrentQueue<KeyEvent> _queue = new();
    private Thread? _thread;
    private CancellationTokenSource? _cts;

    public KeyReader(ITerminal terminal, Action<string>? trace = null)
    {
        _terminal = terminal;
        _trace = trace;
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
        var activeModifiers = Modifiers.None;

        Trace("KeyReader started.");

        while (!ct.IsCancellationRequested)
        {
            int bytesRead;
            try
            {
                bytesRead = input.Read(buffer, pending, buffer.Length - pending);
            }
            catch (OperationCanceledException)
            {
                Trace("Read canceled.");
                break;
            }
            catch (ObjectDisposedException)
            {
                Trace("Input stream disposed.");
                break;
            }

            if (bytesRead == 0)
            {
                Trace($"Read 0 bytes. pending={pending} buffer={FormatBytes(buffer.AsSpan(0, pending))}");

                if (pending == 1 && buffer[0] == 0x1B)
                {
                    Trace("Flushing deferred ESC as bare Escape key.");
                    _queue.Enqueue(new KeyEvent(Key.Escape, Modifiers.None, null));
                    pending = 0;
                }

                continue;
            }

            Trace($"Read {bytesRead} byte(s): {FormatBytes(buffer.AsSpan(pending, bytesRead))}");
            pending += bytesRead;
            var span = buffer.AsSpan(0, pending);
            Trace($"Pending buffer: {FormatBytes(span)}");

            while (span.Length > 0)
            {
                if (span.Length == 1 && span[0] == 0x1B)
                {
                    Trace("Deferring lone ESC until next read.");
                    break;
                }

                var result = KeyParser.Parse(span, out var consumed);
                if (result == null)
                {
                    Trace($"Parser needs more data for: {FormatBytes(span)}");
                    break;
                }

                var key = result.Value;
                Trace($"Parsed {key.EventType} {key.Key} mods={key.Mods} char={FormatChar(key.Char)} consumed={consumed} from {FormatBytes(span[..consumed])}");

                if (TryUpdateModifierState(key, ref activeModifiers))
                {
                    Trace($"Updated active modifiers to {activeModifiers} from modifier-key event.");
                }
                else
                {
                    if (activeModifiers != Modifiers.None)
                        key = key with { Mods = key.Mods | activeModifiers };

                    _queue.Enqueue(key);
                }

                span = span[consumed..];
            }

            // Shift unconsumed bytes to the start
            var remaining = span.Length;
            if (remaining > 0 && pending - remaining > 0)
            {
                span.CopyTo(buffer);
                Trace($"Shifted {remaining} pending byte(s) to buffer start: {FormatBytes(buffer.AsSpan(0, remaining))}");
            }

            pending = remaining;
        }

        Trace("KeyReader stopped.");
    }

    private void Trace(string message)
    {
        _trace?.Invoke($"[{DateTimeOffset.Now:HH:mm:ss.fff}] {message}");
    }

    private static string FormatBytes(ReadOnlySpan<byte> data)
    {
        if (data.IsEmpty)
            return "(empty)";

        return string.Join(" ", data.ToArray().Select(static b => $"0x{b:X2}"));
    }

    private static string FormatChar(char? value) => value switch
    {
        null => "null",
        ' ' => "' '",
        '\t' => @"'\t'",
        '\r' => @"'\r'",
        '\n' => @"'\n'",
        _ => $"'{value}'",
    };

    private static bool TryUpdateModifierState(KeyEvent key, ref Modifiers activeModifiers)
    {
        var modifier = GetModifierFromKeyEvent(key);
        if (modifier == Modifiers.None)
            return false;

        if (key.EventType == KeyEventType.Release)
            activeModifiers &= ~modifier;
        else
            activeModifiers |= modifier;

        return true;
    }

    private static Modifiers GetModifierFromKeyEvent(KeyEvent key)
    {
        if (key.Char is null)
            return Modifiers.None;

        return ((int)key.Char.Value) switch
        {
            57441 or 57447 => Modifiers.Shift,
            57442 or 57448 => Modifiers.Ctrl,
            57443 or 57449 => Modifiers.Alt,
            _ => Modifiers.None,
        };
    }
}
