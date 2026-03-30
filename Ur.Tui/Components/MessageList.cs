using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Tui.State;
using Ur.Tui.Util;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

public sealed class MessageList : IComponent
{
    private static readonly Color UserFg = new(0, 200, 200);
    private static readonly Color AssistantFg = Color.White;
    private static readonly Color ToolFg = new(200, 200, 0);
    private static readonly Color SystemFg = new(128, 128, 128);
    private static readonly Color ErrorFg = new(255, 80, 80);
    private static readonly Color Bg = Color.Black;

    private readonly ChatState _state;

    public MessageList(ChatState state)
    {
        _state = state;
    }

    public void Render(Buffer buffer, Rect area)
    {
        if (area.Width < 1 || area.Height < 1)
            return;

        buffer.Fill(area, new Cell(' ', AssistantFg, Bg));

        // Build all rendered lines from all messages (bottom-up rendering)
        var allLines = new List<RenderedLine>();
        foreach (var msg in _state.Messages)
        {
            var lines = RenderMessage(msg, area.Width);
            allLines.AddRange(lines);
        }

        // Apply scroll offset: we render from the bottom, so scroll shifts upward
        var totalLines = allLines.Count;
        var visibleLines = area.Height;
        var bottomIndex = totalLines - _state.ScrollOffset;
        var topIndex = bottomIndex - visibleLines;

        // Draw lines from bottom to top
        for (var row = visibleLines - 1; row >= 0; row--)
        {
            var lineIndex = topIndex + row;
            if (lineIndex < 0 || lineIndex >= totalLines)
                continue;

            var line = allLines[lineIndex];
            buffer.WriteString(area.X, area.Y + row, line.Text, line.Fg, Bg);
        }
    }

    public bool HandleKey(KeyEvent key)
    {
        switch (key.Key)
        {
            case Key.PageUp:
                _state.ScrollOffset += 5;
                return true;
            case Key.PageDown:
                _state.ScrollOffset = Math.Max(0, _state.ScrollOffset - 5);
                return true;
            default:
                return false;
        }
    }

    private static List<RenderedLine> RenderMessage(DisplayMessage msg, int width)
    {
        var lines = new List<RenderedLine>();
        var (prefix, fg) = msg.Role switch
        {
            MessageRole.User => ("You: ", UserFg),
            MessageRole.Assistant => ("", AssistantFg),
            MessageRole.Tool => ($"[tool: {msg.ToolName}] ", ToolFg),
            MessageRole.System => ("System: ", msg.IsError ? ErrorFg : SystemFg),
            _ => ("", AssistantFg),
        };

        var content = msg.Content.ToString();
        if (msg is { IsStreaming: true, Role: MessageRole.Assistant })
            content += "▍";

        if (content.Length == 0 && prefix.Length == 0)
        {
            lines.Add(new RenderedLine("", fg));
            return lines;
        }

        // First line includes the prefix
        var firstLineWidth = width - prefix.Length;
        if (firstLineWidth <= 0)
        {
            lines.Add(new RenderedLine(prefix, fg));
            return lines;
        }

        var wrapped = WordWrap.Wrap(content, firstLineWidth);
        if (wrapped.Count == 0)
            wrapped.Add("");

        // First line: prefix + first wrapped segment
        lines.Add(new RenderedLine(prefix + wrapped[0], fg));

        // Continuation lines: indented to align with content after prefix
        var indent = new string(' ', prefix.Length);
        for (var i = 1; i < wrapped.Count; i++)
        {
            var continuation = WordWrap.Wrap(wrapped[i], width);
            if (continuation.Count == 0)
                lines.Add(new RenderedLine(indent, fg));
            else
            {
                lines.Add(new RenderedLine(indent + continuation[0], fg));
                for (var j = 1; j < continuation.Count; j++)
                    lines.Add(new RenderedLine(continuation[j], fg));
            }
        }

        return lines;
    }

    private readonly record struct RenderedLine(string Text, Color Fg);
}
