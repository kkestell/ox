using Ox.Terminal.Input;

namespace Ox.Tests.Terminal;

public sealed class InputCoordinatorTests
{
    /// <summary>
    /// Events fire eagerly on enqueue (on the calling thread), so arrival
    /// order is immediately observable via the InputReceived event. The
    /// channel also preserves FIFO order for consumers that read from it.
    /// </summary>
    [Fact]
    public void Enqueue_FiresEventsEagerlyInArrivalOrder()
    {
        var source = new FakeInputSource();
        using var coordinator = new InputCoordinator(source);
        var observed = new List<string>();

        coordinator.InputReceived += (_, inputEvent) =>
        {
            observed.Add(inputEvent switch
            {
                KeyInputEvent => "key",
                MouseInputEvent => "mouse",
                _ => "unknown",
            });
        };

        source.EmitKey(KeyEventArgs.FromConsoleKeyInfo(new ConsoleKeyInfo('a', ConsoleKey.A, false, false, false)));
        source.EmitMouse(new MouseEventArgs([MouseFlags.Button1Clicked], new Point(3, 4)));
        source.EmitKey(KeyEventArgs.FromConsoleKeyInfo(new ConsoleKeyInfo('b', ConsoleKey.B, false, false, false)));

        // Events fired eagerly — no ProcessPendingInput call needed.
        Assert.Equal(["key", "mouse", "key"], observed);
    }

    /// <summary>
    /// Verifies that the channel reader preserves FIFO order and that all
    /// enqueued events are available for consumption.
    /// </summary>
    [Fact]
    public void Enqueue_WritesToChannelInOrder()
    {
        var source = new FakeInputSource();
        using var coordinator = new InputCoordinator(source);

        source.EmitKey(KeyEventArgs.FromConsoleKeyInfo(new ConsoleKeyInfo('a', ConsoleKey.A, false, false, false)));
        source.EmitMouse(new MouseEventArgs([MouseFlags.Button1Clicked], new Point(3, 4)));
        source.EmitKey(KeyEventArgs.FromConsoleKeyInfo(new ConsoleKeyInfo('b', ConsoleKey.B, false, false, false)));

        var channelOrder = new List<string>();
        while (coordinator.Reader.TryRead(out var evt))
        {
            channelOrder.Add(evt switch
            {
                KeyInputEvent => "key",
                MouseInputEvent => "mouse",
                _ => "unknown",
            });
        }

        Assert.Equal(["key", "mouse", "key"], channelOrder);
    }

    /// <summary>
    /// Disposing the coordinator completes the channel writer so any pending
    /// WaitToReadAsync unblocks with false.
    /// </summary>
    [Fact]
    public async Task Dispose_CompletesChannel()
    {
        var coordinator = new InputCoordinator();
        coordinator.Dispose();

        // WaitToReadAsync should return false immediately on a completed channel.
        var canRead = await coordinator.Reader.WaitToReadAsync();
        Assert.False(canRead);
    }

    [Fact]
    public void FromConsoleKeyInfo_MapsLetterAndModifierStateToKeyCode()
    {
        var key = KeyEventArgs.FromConsoleKeyInfo(new ConsoleKeyInfo('A', ConsoleKey.A, true, false, false));

        Assert.Equal(KeyCode.A | KeyCode.ShiftMask, key.KeyCode);
        Assert.True(key.KeyCode.HasShift());
        Assert.Equal(KeyCode.A, key.KeyCode.WithoutModifiers());
    }

    [Fact]
    public void MouseEventArgs_HasFlagSupportsCombinedFlags()
    {
        var mouse = new MouseEventArgs(
            [MouseFlags.Button1Pressed | MouseFlags.ButtonCtrl, MouseFlags.ReportMousePosition],
            new Point(5, 6));

        Assert.True(mouse.HasFlag(MouseFlags.Button1Pressed));
        Assert.True(mouse.HasFlag(MouseFlags.ButtonCtrl));
        Assert.True(mouse.HasAnyFlag(MouseFlags.Button3Clicked, MouseFlags.ReportMousePosition));
        Assert.False(mouse.HasFlag(MouseFlags.Button2Pressed));
    }

    private sealed class FakeInputSource : IInputSource
    {
        public event EventHandler<KeyEventArgs>? KeyPressed;
        public event EventHandler<MouseEventArgs>? MouseEvent;

        public void EmitKey(KeyEventArgs keyEvent) => KeyPressed?.Invoke(this, keyEvent);

        public void EmitMouse(MouseEventArgs mouseEvent) => MouseEvent?.Invoke(this, mouseEvent);
    }
}
