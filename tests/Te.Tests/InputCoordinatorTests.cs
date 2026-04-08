using Te.Input;

namespace Te.Tests;

public sealed class InputCoordinatorTests
{
    [Fact]
    public void ProcessPendingInput_PreservesArrivalOrderAcrossInputKinds()
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

        coordinator.ProcessPendingInput();

        Assert.Equal(["key", "mouse", "key"], observed);
        Assert.Equal(0, coordinator.PendingInputCount);
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
