using Ur.Terminal.Input;

namespace Ur.Terminal.Tests;

public class KeyParserTests
{
    [Fact]
    public void PrintableChar_ReturnsKeyAndChar()
    {
        var result = KeyParser.Parse([(byte)'a'], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.A, result.Value.Key);
        Assert.Equal(Modifiers.None, result.Value.Mods);
        Assert.Equal('a', result.Value.Char);
        Assert.Equal(1, consumed);
    }

    [Fact]
    public void Enter_ReturnsEnter()
    {
        var result = KeyParser.Parse([0x0D], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Enter, result.Value.Key);
        Assert.Equal(1, consumed);
    }

    [Fact]
    public void Escape_ReturnsEscape()
    {
        var result = KeyParser.Parse([0x1B], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Escape, result.Value.Key);
        Assert.Equal(1, consumed);
    }

    [Fact]
    public void ArrowUp_ReturnsUp()
    {
        var result = KeyParser.Parse([0x1B, 0x5B, 0x41], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Up, result.Value.Key);
        Assert.Equal(3, consumed);
    }

    [Fact]
    public void ArrowDown_ReturnsDown()
    {
        var result = KeyParser.Parse([0x1B, 0x5B, 0x42], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Down, result.Value.Key);
        Assert.Equal(3, consumed);
    }

    [Fact]
    public void ArrowLeft_ReturnsLeft()
    {
        var result = KeyParser.Parse([0x1B, 0x5B, 0x44], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Left, result.Value.Key);
        Assert.Equal(3, consumed);
    }

    [Fact]
    public void ArrowRight_ReturnsRight()
    {
        var result = KeyParser.Parse([0x1B, 0x5B, 0x43], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Right, result.Value.Key);
        Assert.Equal(3, consumed);
    }

    [Fact]
    public void PageUp_ReturnsPageUp()
    {
        var result = KeyParser.Parse([0x1B, 0x5B, 0x35, 0x7E], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.PageUp, result.Value.Key);
        Assert.Equal(4, consumed);
    }

    [Fact]
    public void PageDown_ReturnsPageDown()
    {
        var result = KeyParser.Parse([0x1B, 0x5B, 0x36, 0x7E], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.PageDown, result.Value.Key);
        Assert.Equal(4, consumed);
    }

    [Fact]
    public void Delete_ReturnsDelete()
    {
        var result = KeyParser.Parse([0x1B, 0x5B, 0x33, 0x7E], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Delete, result.Value.Key);
        Assert.Equal(4, consumed);
    }

    [Fact]
    public void Backspace_ReturnsBackspace()
    {
        var result = KeyParser.Parse([0x7F], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Backspace, result.Value.Key);
        Assert.Equal(1, consumed);
    }

    [Fact]
    public void Tab_ReturnsTab()
    {
        var result = KeyParser.Parse([0x09], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Tab, result.Value.Key);
        Assert.Equal(1, consumed);
    }

    [Fact]
    public void Space_ReturnsSpaceWithChar()
    {
        var result = KeyParser.Parse([0x20], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.Space, result.Value.Key);
        Assert.Equal(' ', result.Value.Char);
        Assert.Equal(1, consumed);
    }

    [Fact]
    public void CtrlC_ReturnsCtrlModifier()
    {
        var result = KeyParser.Parse([0x03], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.C, result.Value.Key);
        Assert.Equal(Modifiers.Ctrl, result.Value.Mods);
        Assert.Equal(1, consumed);
    }

    [Fact]
    public void MultipleKeysInBuffer_ParsesFirst()
    {
        var result = KeyParser.Parse([(byte)'a', (byte)'b'], out var consumed);
        Assert.NotNull(result);
        Assert.Equal(Key.A, result.Value.Key);
        Assert.Equal('a', result.Value.Char);
        Assert.Equal(1, consumed);
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
}
