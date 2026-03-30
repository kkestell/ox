using Ur.Terminal.App;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Terminal.Rendering;
using Ur.Terminal.Terminal;
using Buffer = Ur.Terminal.Core.Buffer;

var logPath = Path.Combine(
    Path.GetTempPath(),
    $"ur-kitty-{DateTimeOffset.Now:yyyyMMdd-HHmmss}.log");

using var traceLog = new TraceLog(logPath);
using var terminal = new AnsiTerminal();
terminal.EnterRawMode();
terminal.EnterAlternateBuffer();
terminal.HideCursor();

var compositor = new Compositor(terminal.Width, terminal.Height);
var layer = new Layer(0, 0, terminal.Width, terminal.Height);
compositor.AddLayer(layer);

traceLog.Write($"Trace log: {logPath}");

var keyReader = new KeyReader(terminal, traceLog.Write);
var app = new KittyApp(compositor, layer, logPath, traceLog.Write);
var renderLoop = new RenderLoop(terminal, compositor, keyReader, targetFps: 30);

using var cts = new CancellationTokenSource();
Console.CancelKeyPress += (_, e) =>
{
    e.Cancel = true;
    cts.Cancel();
};

keyReader.Start(cts.Token);

try
{
    await renderLoop.RunAsync(app.ProcessFrame, cts.Token);
}
finally
{
    keyReader.Stop();
}

file sealed class KittyApp
{
    private static readonly Color Background = new(8, 12, 20);
    private static readonly Color Header = new(255, 220, 120);
    private static readonly Color Body = Color.White;
    private static readonly Color Dim = new(130, 145, 165);

    private readonly Compositor _compositor;
    private readonly Layer _layer;
    private readonly string _logPath;
    private readonly Action<string> _trace;
    private readonly List<string> _events = [];

    public KittyApp(Compositor compositor, Layer layer, string logPath, Action<string> trace)
    {
        _compositor = compositor;
        _layer = layer;
        _logPath = logPath;
        _trace = trace;
    }

    public bool ProcessFrame(ReadOnlySpan<KeyEvent> keys)
    {
        var width = _compositor.Width;
        var height = _compositor.Height;

        if (_layer.Width != width || _layer.Height != height)
            _layer.Resize(width, height);

        foreach (var key in keys)
        {
            var line = Format(key);
            AddEvent(line);
            _trace($"Rendered event: {line}");

            if (key is { Key: Key.C, Mods: Modifiers.Ctrl, EventType: not KeyEventType.Release })
                return false;
        }

        Render();
        return true;
    }

    private void AddEvent(string line)
    {
        _events.Add(line);

        const int maxEvents = 512;
        if (_events.Count > maxEvents)
            _events.RemoveAt(0);
    }

    private void Render()
    {
        var buffer = _layer.Content;
        var bounds = new Rect(0, 0, buffer.Width, buffer.Height);

        _layer.Clear();
        buffer.Fill(bounds, new Cell(' ', Body, Background));

        buffer.WriteString(0, 0, "Ur.Kitty", Header, Background);
        buffer.WriteString(0, 1, "Displays parsed Ur.Terminal key events in real time.", Dim, Background);
        buffer.WriteString(0, 2, "Press keys in a Kitty-capable terminal. Ctrl+C exits.", Dim, Background);
        WriteTrimmed(buffer, 0, 3, $"Trace log: {_logPath}", Dim, Background);

        var visibleRows = Math.Max(0, buffer.Height - 5);
        var start = Math.Max(0, _events.Count - visibleRows);

        for (var row = 0; row < visibleRows && start + row < _events.Count; row++)
            WriteTrimmed(buffer, 0, row + 5, _events[start + row], Body, Background);
    }

    private static string Format(KeyEvent key)
    {
        var timestamp = DateTimeOffset.Now.ToString("HH:mm:ss.fff");
        var mods = key.Mods == Modifiers.None ? "None" : key.Mods.ToString();
        var ch = key.Char switch
        {
            null => "null",
            ' ' => "' '",
            '\t' => @"'\t'",
            '\r' => @"'\r'",
            '\n' => @"'\n'",
            _ => $"'{key.Char}'",
        };

        return $"{timestamp}  {key.EventType,-7}  Key={key.Key,-9} Mods={mods,-12} Char={ch}";
    }

    private static void WriteTrimmed(Buffer buffer, int x, int y, string text, Color fg, Color bg)
    {
        if (buffer.Width <= x)
            return;

        var available = buffer.Width - x;
        if (text.Length <= available)
        {
            buffer.WriteString(x, y, text, fg, bg);
            return;
        }

        if (available == 1)
        {
            buffer.WriteString(x, y, "…", fg, bg);
            return;
        }

        var trimmed = string.Create(available, text, static (span, value) =>
        {
            value.AsSpan(0, span.Length - 1).CopyTo(span);
            span[^1] = '…';
        });

        buffer.WriteString(x, y, trimmed, fg, bg);
    }
}

file sealed class TraceLog(string path) : IDisposable
{
    private readonly object _gate = new();
    private readonly StreamWriter _writer = new(path) { AutoFlush = true };

    public void Write(string line)
    {
        lock (_gate)
            _writer.WriteLine($"[{DateTimeOffset.Now:HH:mm:ss.fff}] {line}");
    }

    public void Dispose()
    {
        lock (_gate)
            _writer.Dispose();
    }
}
