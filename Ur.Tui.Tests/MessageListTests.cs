using System.Text;
using Ur.Terminal.Core;
using Ur.Tui.Components;
using Ur.Tui.State;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Tests;

public class MessageListTests
{
    private readonly ChatState _state = new();
    private readonly MessageList _list;

    public MessageListTests()
    {
        _list = new MessageList(_state);
    }

    private static string ReadRow(Buffer buffer, int y, int startX, int width)
    {
        var chars = new char[width];
        for (var i = 0; i < width; i++)
            chars[i] = buffer.Get(startX + i, y).Char;
        return new string(chars).TrimEnd();
    }

    [Fact]
    public void Render_SingleUserMessage()
    {
        _state.Messages.Add(new DisplayMessage(MessageRole.User) { Content = { } });
        _state.Messages[0].Content.Append("hello");

        var buffer = new Buffer(40, 5);
        _list.Render(buffer, new Rect(0, 0, 40, 5));

        // Message should appear at the bottom row
        var row = ReadRow(buffer, 4, 0, 40);
        Assert.StartsWith("hello", row);
    }

    [Fact]
    public void Render_MultipleMessages_BottomUp()
    {
        _state.Messages.Add(new DisplayMessage(MessageRole.User));
        _state.Messages[0].Content.Append("first");

        _state.Messages.Add(new DisplayMessage(MessageRole.Assistant));
        _state.Messages[1].Content.Append("response");

        var buffer = new Buffer(40, 5);
        _list.Render(buffer, new Rect(0, 0, 40, 5));

        // Latest message (assistant response) should be at the bottom
        var bottomRow = ReadRow(buffer, 4, 0, 40);
        Assert.Equal("response", bottomRow);

        // User message should be one row above
        var aboveRow = ReadRow(buffer, 3, 0, 40);
        Assert.StartsWith("first", aboveRow);
    }

    [Fact]
    public void Render_ScrollOffset_ShiftsViewport()
    {
        // Add enough messages to fill the viewport
        for (var i = 0; i < 10; i++)
        {
            var msg = new DisplayMessage(MessageRole.User);
            msg.Content.Append($"msg{i}");
            _state.Messages.Add(msg);
        }

        _state.ScrollOffset = 3;

        var buffer = new Buffer(40, 5);
        _list.Render(buffer, new Rect(0, 0, 40, 5));

        // With scroll offset 3, the bottom visible message should be msg6 (10 - 3 - 1 = 6)
        var bottomRow = ReadRow(buffer, 4, 0, 40);
        Assert.StartsWith("msg6", bottomRow);
    }

    [Fact]
    public void Render_StreamingMessage_ShowsCursor()
    {
        var msg = new DisplayMessage(MessageRole.Assistant) { IsStreaming = true };
        msg.Content.Append("thinking");
        _state.Messages.Add(msg);

        var buffer = new Buffer(40, 3);
        _list.Render(buffer, new Rect(0, 0, 40, 3));

        var bottomRow = ReadRow(buffer, 2, 0, 40);
        Assert.Contains("▍", bottomRow);
    }

    [Fact]
    public void Render_LongMessage_WordWraps()
    {
        var msg = new DisplayMessage(MessageRole.User);
        msg.Content.Append("this is a long message that should wrap");
        _state.Messages.Add(msg);

        // Width 20 means "You: " (5) + 15 chars per first line
        var buffer = new Buffer(20, 5);
        _list.Render(buffer, new Rect(0, 0, 20, 5));

        // Verify content spans multiple rows
        var row4 = ReadRow(buffer, 4, 0, 20);
        var row3 = ReadRow(buffer, 3, 0, 20);
        // Both rows should have content (wrapped)
        Assert.False(string.IsNullOrWhiteSpace(row4));
        Assert.False(string.IsNullOrWhiteSpace(row3));
    }

    [Fact]
    public void Render_ToolMessage_ShowsToolName()
    {
        var msg = new DisplayMessage(MessageRole.Tool) { ToolName = "search" };
        msg.Content.Append("found 3 results");
        _state.Messages.Add(msg);

        var buffer = new Buffer(50, 3);
        _list.Render(buffer, new Rect(0, 0, 50, 3));

        var bottomRow = ReadRow(buffer, 2, 0, 50);
        Assert.StartsWith("[tool: search] found 3 results", bottomRow);
    }

    [Fact]
    public void Render_ErrorMessage_UsesSystemPrefix()
    {
        var msg = new DisplayMessage(MessageRole.System) { IsError = true };
        msg.Content.Append("something went wrong");
        _state.Messages.Add(msg);

        var buffer = new Buffer(50, 3);
        _list.Render(buffer, new Rect(0, 0, 50, 3));

        var bottomRow = ReadRow(buffer, 2, 0, 50);
        Assert.StartsWith("System: something went wrong", bottomRow);

        // Verify error color (red)
        var cell = buffer.Get(0, 2);
        Assert.Equal(new Color(255, 80, 80), cell.Fg);
    }
}
