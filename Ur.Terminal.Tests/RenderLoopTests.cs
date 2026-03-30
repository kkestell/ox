using Ur.Terminal.App;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Terminal.Rendering;
using Ur.Terminal.Terminal;

namespace Ur.Terminal.Tests;

public class RenderLoopTests
{
    [Fact]
    public async Task CallsProcessFrameEachTick()
    {
        var terminal = new TestTerminal { Width = 10, Height = 5 };
        var compositor = new Compositor(10, 5);
        var layer = new Layer(0, 0, 10, 5);
        layer.Content.Fill(new Rect(0, 0, 10, 5), new Cell(' ', Color.White, Color.Black));
        compositor.AddLayer(layer);
        var reader = new KeyReader(terminal);
        var loop = new RenderLoop(terminal, compositor, reader, targetFps: 60);
        var callCount = 0;

        using var cts = new CancellationTokenSource(TimeSpan.FromMilliseconds(200));
        await loop.RunAsync(_ =>
        {
            callCount++;
            return callCount < 3;
        }, cts.Token);

        Assert.True(callCount >= 3);
    }

    [Fact]
    public async Task FlushesComposedOutput()
    {
        var terminal = new TestTerminal { Width = 5, Height = 3 };
        var compositor = new Compositor(5, 3);
        var layer = new Layer(0, 0, 5, 3);
        layer.Content.Set(0, 0, new Cell('X', Color.White, Color.Black));
        compositor.AddLayer(layer);
        var reader = new KeyReader(terminal);
        var loop = new RenderLoop(terminal, compositor, reader, targetFps: 60);
        var frame = 0;

        using var cts = new CancellationTokenSource(TimeSpan.FromMilliseconds(200));
        await loop.RunAsync(_ => ++frame < 2, cts.Token);

        Assert.NotEmpty(terminal.Writes);
    }

    [Fact]
    public async Task RespectsExitSignal()
    {
        var terminal = new TestTerminal { Width = 5, Height = 3 };
        var compositor = new Compositor(5, 3);
        var layer = new Layer(0, 0, 5, 3);
        layer.Content.Fill(new Rect(0, 0, 5, 3), new Cell(' ', Color.White, Color.Black));
        compositor.AddLayer(layer);
        var reader = new KeyReader(terminal);
        var loop = new RenderLoop(terminal, compositor, reader, targetFps: 60);

        await loop.RunAsync(_ => false, CancellationToken.None);

        // If we get here, the loop exited. Success.
        Assert.True(true);
    }

    [Fact]
    public async Task HandlesCancellation()
    {
        var terminal = new TestTerminal { Width = 5, Height = 3 };
        var compositor = new Compositor(5, 3);
        var layer = new Layer(0, 0, 5, 3);
        layer.Content.Fill(new Rect(0, 0, 5, 3), new Cell(' ', Color.White, Color.Black));
        compositor.AddLayer(layer);
        var reader = new KeyReader(terminal);
        var loop = new RenderLoop(terminal, compositor, reader, targetFps: 60);

        using var cts = new CancellationTokenSource(TimeSpan.FromMilliseconds(100));
        await loop.RunAsync(_ => true, cts.Token);

        // If we get here without exception, cancellation was handled cleanly.
        Assert.True(true);
    }
}
