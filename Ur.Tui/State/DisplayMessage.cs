using System.Text;

namespace Ur.Tui.State;

/// <summary>
/// Display-only role for a message in the TUI. Simpler than the M.E.AI
/// <c>ChatRole</c> because the TUI only needs to distinguish four visual styles.
/// </summary>
public enum MessageRole
{
    User,
    Assistant,
    Tool,
    System,
}

/// <summary>
/// A single message rendered in the TUI message list. Uses <see cref="StringBuilder"/>
/// for <see cref="Content"/> so that streaming chunks from the agent loop can be
/// appended in-place without allocating a new string per token.
///
/// DisplayMessage is a TUI-specific view model — it is not persisted and is
/// separate from the <see cref="Microsoft.Extensions.AI.ChatMessage"/> objects
/// stored in the session. The agent loop's events are translated into
/// DisplayMessages by <see cref="ChatApp.DrainAgentEvents"/>.
/// </summary>
public sealed class DisplayMessage
{
    public MessageRole Role { get; }
    public StringBuilder Content { get; } = new();

    /// <summary>True while the agent loop is still streaming tokens into this message.</summary>
    public bool IsStreaming { get; set; }

    /// <summary>For tool messages, the name of the tool that produced this result.</summary>
    public string? ToolName { get; init; }

    /// <summary>Whether this message represents an error (rendered in red).</summary>
    public bool IsError { get; init; }

    public DateTimeOffset Timestamp { get; } = DateTimeOffset.Now;

    public DisplayMessage(MessageRole role)
    {
        Role = role;
    }
}
