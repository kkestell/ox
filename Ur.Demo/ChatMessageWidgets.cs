using Ur.Drawing;
using Ur.Widgets;

namespace Ur.Demo;

/// <summary>
/// Renders a <see cref="UserMessage"/> as "Author: Content" on a single line.
/// Author name is drawn in bright white to make it visually distinct from
/// the message body, which uses the default text color.
/// </summary>
public class UserMessageWidget : Widget
{
    private static readonly Style AuthorStyle = new(Color.BrightWhite, Color.Black, Modifier.Bold);
    private static readonly Style ContentStyle = new(Color.White, Color.Black);

    private readonly string _author;
    private readonly string _content;

    public UserMessageWidget(UserMessage msg)
    {
        ArgumentNullException.ThrowIfNull(msg);
        _author = msg.Author;
        _content = msg.Content;

        var line = $"{_author}: {_content}";
        PreferredWidth = line.Length;
        PreferredHeight = 1;
        HorizontalSizing = SizingMode.Grow;
    }

    public override void Draw(ICanvas canvas)
    {
        // Draw author name in bold first, then append the message body in normal weight.
        // The author prefix is measured so the body starts at the correct column.
        var prefix = $"{_author}: ";
        canvas.DrawText(0, 0, prefix, AuthorStyle);
        canvas.DrawText(prefix.Length, 0, _content, ContentStyle);
    }
}

/// <summary>
/// Renders a <see cref="SystemMessage"/> as "[System] Content" on a single line.
/// Yellow color distinguishes system notices from user messages at a glance.
/// </summary>
public class SystemMessageWidget : Widget
{
    private static readonly Style SystemStyle = new(Color.Yellow, Color.Black, Modifier.Dim);

    private readonly string _content;

    public SystemMessageWidget(SystemMessage msg)
    {
        ArgumentNullException.ThrowIfNull(msg);
        _content = msg.Content;

        var line = $"[System] {_content}";
        PreferredWidth = line.Length;
        PreferredHeight = 1;
        HorizontalSizing = SizingMode.Grow;
    }

    public override void Draw(ICanvas canvas)
    {
        canvas.DrawText(0, 0, $"[System] {_content}", SystemStyle);
    }
}

/// <summary>
/// Renders a <see cref="ToolMessage"/> as "[ToolName]\nContent" where the content
/// may span multiple lines. Cyan color visually groups tool output separately from
/// user and system messages.
/// </summary>
public class ToolMessageWidget : Widget
{
    private static readonly Style HeaderStyle = new(Color.BrightCyan, Color.Black, Modifier.Bold);
    private static readonly Style BodyStyle = new(Color.Cyan, Color.Black);

    private readonly string _toolName;
    private readonly string[] _contentLines;

    public ToolMessageWidget(ToolMessage msg)
    {
        ArgumentNullException.ThrowIfNull(msg);
        _toolName = msg.ToolName;
        _contentLines = msg.Content.Split('\n');

        // Height: 1 row for the header + one row per content line.
        var headerWidth = $"[{_toolName}]".Length;
        var maxContentWidth = _contentLines.Max(l => l.Length);
        PreferredWidth = Math.Max(headerWidth, maxContentWidth);
        PreferredHeight = 1 + _contentLines.Length;
        HorizontalSizing = SizingMode.Grow;
    }

    public override void Draw(ICanvas canvas)
    {
        canvas.DrawText(0, 0, $"[{_toolName}]", HeaderStyle);

        for (var i = 0; i < _contentLines.Length; i++)
            canvas.DrawText(0, i + 1, _contentLines[i], BodyStyle);
    }
}
