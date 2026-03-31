using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Tui.Components;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Tests;

public class ApiKeyModalTests
{
    private readonly ApiKeyModal _modal = new();
    private readonly Buffer _buffer = new(ApiKeyModal.ModalWidth, ApiKeyModal.ModalHeight);
    private readonly Rect _area = new(0, 0, ApiKeyModal.ModalWidth, ApiKeyModal.ModalHeight);

    private static KeyEvent Char(char c) => new(Key.Unknown, Modifiers.None, c);
    private static KeyEvent Named(Key key) => new(key, Modifiers.None, null);

    private string ReadRow(int y, int startX, int width)
    {
        var chars = new char[width];
        for (var i = 0; i < width; i++)
            chars[i] = _buffer.Get(startX + i, y).Char;
        return new string(chars).TrimEnd();
    }

    [Fact]
    public void Render_ShowsBorderAndTitle()
    {
        _modal.Render(_buffer, _area);

        // Find the title "API Key" somewhere in the buffer
        var found = false;
        for (var y = 0; y < _buffer.Height; y++)
        {
            var row = ReadRow(y, 0, _buffer.Width);
            if (row.Contains("API Key"))
            {
                found = true;
                break;
            }
        }
        Assert.True(found, "Title 'API Key' should appear in rendered buffer");

        // Check for box-drawing corners (modal renders at 0,0 with its own size).
        Assert.Equal('┌', _buffer.Get(0, 0).Char);
        Assert.Equal('┐', _buffer.Get(ApiKeyModal.ModalWidth - 1, 0).Char);
    }

    [Fact]
    public void Render_MasksInput()
    {
        _modal.HandleKey(Char('s'));
        _modal.HandleKey(Char('e'));
        _modal.HandleKey(Char('c'));
        _modal.HandleKey(Char('r'));
        _modal.HandleKey(Char('e'));
        _modal.HandleKey(Char('t'));

        _modal.Render(_buffer, _area);

        // Masked input is in the content area: border (1) + padding row offset.
        // Content starts at y=1. Title at y=1, hint at y=3, input at y=4.
        var inputRow = ReadRow(4, 1, 10);
        Assert.Equal("******", inputRow);
    }

    [Fact]
    public void HandleKey_Enter_SetsSubmitted()
    {
        _modal.HandleKey(Char('k'));
        _modal.HandleKey(Char('e'));
        _modal.HandleKey(Char('y'));
        var consumed = _modal.HandleKey(Named(Key.Enter));

        Assert.False(consumed);
        Assert.True(_modal.Submitted);
        Assert.Equal("key", _modal.Value);
    }

    [Fact]
    public void HandleKey_Escape_SetsDismissed()
    {
        var consumed = _modal.HandleKey(Named(Key.Escape));

        Assert.False(consumed);
        Assert.True(_modal.Dismissed);
    }

    [Fact]
    public void HandleKey_Backspace_DeletesLastChar()
    {
        _modal.HandleKey(Char('a'));
        _modal.HandleKey(Char('b'));
        _modal.HandleKey(Named(Key.Backspace));
        _modal.HandleKey(Named(Key.Enter));

        Assert.Equal("a", _modal.Value);
    }
}
