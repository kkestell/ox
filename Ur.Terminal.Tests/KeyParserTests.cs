using Ur.Terminal.Input;

namespace Ur.Terminal.Tests;

public class KeyParserTests
{
    private static KeyEvent Parse(byte[] input, int expectedConsumed)
    {
        var result = KeyParser.Parse(input, out var consumed);

        Assert.NotNull(result);
        Assert.Equal(expectedConsumed, consumed);

        return result.Value;
    }

    [Fact]
    public void PrintableChar_ReturnsKeyAndChar()
    {
        var result = Parse([(byte)'a'], expectedConsumed: 1);

        Assert.Equal(Key.A, result.Key);
        Assert.Equal(Modifiers.None, result.Mods);
        Assert.Equal('a', result.Char);
        Assert.Equal(KeyEventType.Press, result.EventType);
    }

    [Fact]
    public void Enter_CR_ReturnsEnter()
    {
        var result = Parse([0x0D], expectedConsumed: 1);

        Assert.Equal(Key.Enter, result.Key);
        Assert.Equal(KeyEventType.Press, result.EventType);
    }

    [Fact]
    public void Enter_LF_ReturnsEnter()
    {
        // LF (0x0A) is what the app receives when icrnl is active (default in stty -icanon)
        var result = Parse([0x0A], expectedConsumed: 1);

        Assert.Equal(Key.Enter, result.Key);
        Assert.Equal(KeyEventType.Press, result.EventType);
    }

    [Fact]
    public void Escape_ReturnsEscape()
    {
        var result = Parse([0x1B], expectedConsumed: 1);

        Assert.Equal(Key.Escape, result.Key);
        Assert.Equal(KeyEventType.Press, result.EventType);
    }

    [Fact]
    public void ArrowUp_ReturnsUp()
    {
        var result = Parse([0x1B, 0x5B, 0x41], expectedConsumed: 3);

        Assert.Equal(Key.Up, result.Key);
        Assert.Equal(KeyEventType.Press, result.EventType);
    }

    [Fact]
    public void ArrowDown_ReturnsDown()
    {
        var result = Parse([0x1B, 0x5B, 0x42], expectedConsumed: 3);

        Assert.Equal(Key.Down, result.Key);
    }

    [Fact]
    public void ArrowLeft_ReturnsLeft()
    {
        var result = Parse([0x1B, 0x5B, 0x44], expectedConsumed: 3);

        Assert.Equal(Key.Left, result.Key);
    }

    [Fact]
    public void ArrowRight_ReturnsRight()
    {
        var result = Parse([0x1B, 0x5B, 0x43], expectedConsumed: 3);

        Assert.Equal(Key.Right, result.Key);
    }

    [Fact]
    public void PageUp_ReturnsPageUp()
    {
        var result = Parse([0x1B, 0x5B, 0x35, 0x7E], expectedConsumed: 4);

        Assert.Equal(Key.PageUp, result.Key);
    }

    [Fact]
    public void PageDown_ReturnsPageDown()
    {
        var result = Parse([0x1B, 0x5B, 0x36, 0x7E], expectedConsumed: 4);

        Assert.Equal(Key.PageDown, result.Key);
    }

    [Fact]
    public void Delete_ReturnsDelete()
    {
        var result = Parse([0x1B, 0x5B, 0x33, 0x7E], expectedConsumed: 4);

        Assert.Equal(Key.Delete, result.Key);
    }

    [Fact]
    public void Backspace_ReturnsBackspace()
    {
        var result = Parse([0x7F], expectedConsumed: 1);

        Assert.Equal(Key.Backspace, result.Key);
    }

    [Fact]
    public void Tab_ReturnsTab()
    {
        var result = Parse([0x09], expectedConsumed: 1);

        Assert.Equal(Key.Tab, result.Key);
    }

    [Fact]
    public void Space_ReturnsSpaceWithChar()
    {
        var result = Parse([0x20], expectedConsumed: 1);

        Assert.Equal(Key.Space, result.Key);
        Assert.Equal(' ', result.Char);
    }

    [Fact]
    public void CtrlC_ReturnsCtrlModifier()
    {
        var result = Parse([0x03], expectedConsumed: 1);

        Assert.Equal(Key.C, result.Key);
        Assert.Equal(Modifiers.Ctrl, result.Mods);
    }

    [Fact]
    public void MultipleKeysInBuffer_ParsesFirst()
    {
        var result = Parse([(byte)'a', (byte)'b'], expectedConsumed: 1);

        Assert.Equal(Key.A, result.Key);
        Assert.Equal('a', result.Char);
    }

    [Fact]
    public void IncompleteEscape_ReturnsNull()
    {
        var result = KeyParser.Parse([0x1B, 0x5B], out _);
        Assert.Null(result);
    }

    [Fact]
    public void UnknownSequence_ReturnsUnknown()
    {
        var result = KeyParser.Parse([0x1B, 0x5B, 0xFF], out var consumed);

        Assert.NotNull(result);
        Assert.Equal(Key.Unknown, result.Value.Key);
        Assert.Equal(3, consumed);
    }

    [Fact]
    public void KittyShiftEnter_ReturnsShiftModifier()
    {
        var result = Parse([0x1B, 0x5B, 0x31, 0x33, 0x3B, 0x32, 0x75], expectedConsumed: 7);

        Assert.Equal(Key.Enter, result.Key);
        Assert.Equal(Modifiers.Shift, result.Mods);
        Assert.Null(result.Char);
        Assert.Equal(KeyEventType.Press, result.EventType);
    }

    [Fact]
    public void KittyPrintableKey_ReturnsCharModifiersAndKey()
    {
        var result = Parse([0x1B, 0x5B, 0x36, 0x35, 0x3B, 0x32, 0x75], expectedConsumed: 7);

        Assert.Equal(Key.A, result.Key);
        Assert.Equal(Modifiers.Shift, result.Mods);
        Assert.Equal('A', result.Char);
        Assert.Equal(KeyEventType.Press, result.EventType);
    }

    [Fact]
    public void KittyRepeatEvent_ReturnsRepeat()
    {
        var result = Parse([0x1B, 0x5B, 0x39, 0x37, 0x3B, 0x31, 0x3A, 0x32, 0x75], expectedConsumed: 9);

        Assert.Equal(Key.A, result.Key);
        Assert.Equal(KeyEventType.Repeat, result.EventType);
    }

    [Fact]
    public void KittyReleaseEvent_ReturnsRelease()
    {
        var result = Parse([0x1B, 0x5B, 0x39, 0x37, 0x3B, 0x31, 0x3A, 0x33, 0x75], expectedConsumed: 9);

        Assert.Equal(Key.A, result.Key);
        Assert.Equal(KeyEventType.Release, result.EventType);
    }

    [Fact]
    public void KittyModifiedArrow_ReturnsModifiers()
    {
        var result = Parse([0x1B, 0x5B, 0x31, 0x3B, 0x35, 0x42], expectedConsumed: 6);

        Assert.Equal(Key.Down, result.Key);
        Assert.Equal(Modifiers.Ctrl, result.Mods);
        Assert.Equal(KeyEventType.Press, result.EventType);
    }

    [Fact]
    public void KittyModifiedTildeSequence_ReturnsPageDownAndModifiers()
    {
        var result = Parse([0x1B, 0x5B, 0x36, 0x3B, 0x33, 0x7E], expectedConsumed: 6);

        Assert.Equal(Key.PageDown, result.Key);
        Assert.Equal(Modifiers.Alt, result.Mods);
    }

    [Fact]
    public void KittyCursorRelease_ReturnsRelease()
    {
        var result = Parse([0x1B, 0x5B, 0x31, 0x3B, 0x31, 0x3A, 0x33, 0x42], expectedConsumed: 8);

        Assert.Equal(Key.Down, result.Key);
        Assert.Equal(KeyEventType.Release, result.EventType);
    }

    [Fact]
    public void IncompleteKittySequence_ReturnsNull()
    {
        var result = KeyParser.Parse([0x1B, 0x5B, 0x31, 0x33, 0x3B, 0x32], out var consumed);

        Assert.Null(result);
        Assert.Equal(0, consumed);
    }

    [Fact]
    public void UnsupportedKittyEventType_ReturnsUnknown()
    {
        var result = Parse([0x1B, 0x5B, 0x39, 0x37, 0x3B, 0x31, 0x3A, 0x39, 0x75], expectedConsumed: 9);

        Assert.Equal(Key.Unknown, result.Key);
    }
}
