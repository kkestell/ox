using System.Buffers;
using System.Diagnostics;
using Ur.Terminal.Input;
using Ur.Terminal.Rendering;
using Ur.Terminal.Terminal;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.App;

public sealed class RenderLoop
{
    private readonly ITerminal _terminal;
    private readonly Compositor _compositor;
    private readonly KeyReader _keyReader;
    private readonly int _targetFps;

    public RenderLoop(ITerminal terminal, Compositor compositor, KeyReader keyReader, int targetFps = 30)
    {
        _terminal = terminal;
        _compositor = compositor;
        _keyReader = keyReader;
        _targetFps = targetFps;
    }

    public async Task RunAsync(Func<ReadOnlySpan<KeyEvent>, bool> processFrame, CancellationToken ct)
    {
        var frameInterval = TimeSpan.FromMilliseconds(1000.0 / _targetFps);
        var previous = new Buffer(_compositor.Width, _compositor.Height);
        var outputWriter = new ArrayBufferWriter<byte>(4096);
        var keys = new List<KeyEvent>();
        var sw = new Stopwatch();
        var firstFrame = true;

        while (!ct.IsCancellationRequested)
        {
            sw.Restart();

            // Check for resize
            var newWidth = _terminal.Width;
            var newHeight = _terminal.Height;
            if (newWidth != _compositor.Width || newHeight != _compositor.Height)
            {
                _compositor.Resize(newWidth, newHeight);
                previous = new Buffer(newWidth, newHeight);
                firstFrame = true;
            }

            // Drain key events
            keys.Clear();
            _keyReader.Drain(keys);

            // Process frame
            var keySpan = System.Runtime.InteropServices.CollectionsMarshal.AsSpan(keys);
            if (!processFrame(keySpan))
                break;

            // Compose
            var current = _compositor.Compose();

            // Diff and write
            outputWriter.Clear();
            if (firstFrame)
            {
                Screen.WriteFullFrame(current, outputWriter);
                firstFrame = false;
            }
            else
            {
                Screen.WriteDiff(current, previous, outputWriter);
            }

            if (outputWriter.WrittenCount > 0)
                _terminal.Write(outputWriter.WrittenSpan);

            previous = current;

            // Wait for next frame
            sw.Stop();
            var remaining = frameInterval - sw.Elapsed;
            if (remaining > TimeSpan.Zero)
            {
                try
                {
                    await Task.Delay(remaining, ct);
                }
                catch (OperationCanceledException)
                {
                    break;
                }
            }
        }
    }
}
