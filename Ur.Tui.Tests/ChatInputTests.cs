using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Tui.Components;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Tests;

public class ChatInputTests
{
    private readonly ChatInput _input = new();
    private readonly Buffer _buffer = new(40, 1);
    private readonly Rect _area = new(0, 0, 40, 1);

    private static KeyEvent Char(char c)
    {
        var key = c switch
        {
            >= 'a' and <= 'z' => (Key)(Key.A + (c - 'a')),
            >= 'A' and <= 'Z' => (Key)(Key.A + (c - 'A')),
            ' ' => Key.Space,
            '/' => Key.Unknown,
            _ => Key.Unknown,
        };
        return new KeyEvent(key, Modifiers.None, c);
    }

    private static KeyEvent Named(Key key) => new(key, Modifiers.None, null);

    private string ReadBufferText(int startX, int length)
    {
        var chars = new char[length];
        for (var i = 0; i < length; i++)
            chars[i] = _buffer.Get(startX + i, 0).Char;
        return new string(chars);
    }

    [Fact]
    public void Render_ShowsPromptAndText()
    {
        _input.HandleKey(Char('h'));
        _input.HandleKey(Char('e'));
        _input.HandleKey(Char('l'));
        _input.HandleKey(Char('l'));
        _input.HandleKey(Char('o'));

        _input.Render(_buffer, _area);

        Assert.Equal("> ", ReadBufferText(0, 2));
        // Text starts at position 2; cursor is at pos 5 so chars 0-4 are "hello"
        // But cursor inverts the char at _cursorPos (5), which is a space
        Assert.Equal('h', _buffer.Get(2, 0).Char);
        Assert.Equal('e', _buffer.Get(3, 0).Char);
        Assert.Equal('l', _buffer.Get(4, 0).Char);
        Assert.Equal('l', _buffer.Get(5, 0).Char);
        Assert.Equal('o', _buffer.Get(6, 0).Char);
    }

    [Fact]
    public void HandleKey_PrintableChar_InsertsAtCursor()
    {
        _input.HandleKey(Char('a'));

        Assert.Equal("a", _input.Text);
    }

    [Fact]
    public void HandleKey_Backspace_DeletesBehindCursor()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Named(Key.Backspace));

        Assert.Equal("a", _input.Text);
    }

    [Fact]
    public void HandleKey_Enter_ReturnsFalse()
    {
        _input.HandleKey(Char('x'));
        var consumed = _input.HandleKey(Named(Key.Enter));

        Assert.False(consumed);
    }

    [Fact]
    public void HandleKey_ArrowKeys_MovesCursor()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Char('c'));

        // Cursor at 3 (end). Move left twice.
        _input.HandleKey(Named(Key.Left));
        _input.HandleKey(Named(Key.Left));

        // Insert 'x' at position 1
        _input.HandleKey(Char('x'));

        Assert.Equal("axbc", _input.Text);
    }

    [Fact]
    public void HandleKey_Home_MovesCursorToStart()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Named(Key.Home));
        _input.HandleKey(Char('x'));

        Assert.Equal("xab", _input.Text);
    }

    [Fact]
    public void HandleKey_End_MovesCursorToEnd()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Named(Key.Home));
        _input.HandleKey(Named(Key.End));
        _input.HandleKey(Char('c'));

        Assert.Equal("abc", _input.Text);
    }

    [Fact]
    public void HandleKey_Delete_DeletesAtCursor()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Named(Key.Home));
        _input.HandleKey(Named(Key.Delete));

        Assert.Equal("b", _input.Text);
    }

    [Fact]
    public void Clear_ResetsTextAndCursor()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.Clear();

        Assert.Equal("", _input.Text);

        // After clear, typing should start at position 0
        _input.HandleKey(Char('x'));
        Assert.Equal("x", _input.Text);
    }

    [Fact]
    public void Render_ShowsCursorAtEndAsInvertedSpace()
    {
        _input.HandleKey(Char('a'));
        _input.Render(_buffer, _area);

        // Cursor is at position 1 (after 'a'), rendered at buffer x=3 (prompt=2 + cursor=1)
        var cursorCell = _buffer.Get(3, 0);
        Assert.Equal(' ', cursorCell.Char);
        Assert.Equal(Color.Black, cursorCell.Fg);
        Assert.Equal(Color.White, cursorCell.Bg);
    }
}
