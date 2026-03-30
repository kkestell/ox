using System.Text;

namespace Ur.Tui.State;

public enum MessageRole
{
    User,
    Assistant,
    Tool,
    System,
}

public sealed class DisplayMessage
{
    public MessageRole Role { get; }
    public StringBuilder Content { get; } = new();
    public bool IsStreaming { get; set; }
    public string? ToolName { get; init; }
    public bool IsError { get; init; }
    public DateTimeOffset Timestamp { get; } = DateTimeOffset.Now;

    public DisplayMessage(MessageRole role)
    {
        Role = role;
    }
}
