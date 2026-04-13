using System.Text;

namespace Ox.App.Conversation;

/// <summary>
/// Status of a tool call through its lifecycle.
/// </summary>
public enum ToolCallStatus
{
    Started,
    AwaitingApproval,
    Succeeded,
    Failed,
}

/// <summary>
/// Status of an individual plan/todo item.
/// </summary>
public enum PlanItemStatus
{
    Pending,
    InProgress,
    Completed,
}

/// <summary>
/// Base type for all visual elements in the conversation stream.
/// Each variant carries the data needed to render it in the ConversationView.
/// </summary>
public abstract class ConversationEntry;

/// <summary>
/// A message submitted by the user. Rendered with a blue circle prefix.
/// </summary>
public sealed class UserMessageEntry(string text) : ConversationEntry
{
    public string Text { get; } = text;
}

/// <summary>
/// Streaming assistant response text. Text grows as tokens arrive —
/// the StringBuilder is mutated by the main loop when ResponseChunk events
/// are processed.
/// </summary>
public sealed class AssistantTextEntry : ConversationEntry
{
    private readonly StringBuilder _text = new();

    public string Text => _text.ToString();

    /// <summary>Append a chunk of streamed text.</summary>
    public void Append(string chunk) => _text.Append(chunk);
}

/// <summary>
/// Streaming reasoning/thinking text from a model that supports extended thinking
/// (DeepSeek-R1, Gemini thinking mode, Ollama Qwen3, etc.).
///
/// Kept separate from <see cref="AssistantTextEntry"/> because thinking has distinct
/// rendering semantics (hollow circle prefix, muted color) and different lifecycle
/// behaviour — a thinking block can precede or accompany the response text but is
/// not the response itself. Merging it into AssistantTextEntry would conflate two
/// different concerns and complicate the null-boundary tracking in OxApp.
/// </summary>
public sealed class ThinkingEntry : ConversationEntry
{
    private readonly StringBuilder _text = new();

    public string Text => _text.ToString();

    /// <summary>Append a chunk of streamed reasoning text.</summary>
    public void Append(string chunk) => _text.Append(chunk);
}

/// <summary>
/// A tool invocation with lifecycle state. Status and Result are mutated
/// by the main loop as tool events arrive.
/// </summary>
public sealed class ToolCallEntry : ConversationEntry
{
    public required string CallId { get; init; }
    public required string ToolName { get; init; }
    public required string FormattedSignature { get; init; }
    public ToolCallStatus Status { get; set; } = ToolCallStatus.Started;
    public string? Result { get; set; }
    public bool IsError { get; set; }
}

/// <summary>
/// A plan/todo list rendered as a multi-line block with status markers.
/// </summary>
public sealed class PlanEntry : ConversationEntry
{
    public required IReadOnlyList<PlanItem> Items { get; set; }
}

/// <summary>Single item in a plan block.</summary>
public sealed record PlanItem(string Content, PlanItemStatus Status);

/// <summary>
/// Container for a sub-agent's events. Child entries are rendered indented
/// beneath the parent signature.
/// </summary>
public sealed class SubagentContainerEntry : ConversationEntry
{
    public required string CallId { get; init; }
    public required string FormattedSignature { get; init; }
    public ToolCallStatus Status { get; set; } = ToolCallStatus.Started;
    public List<ConversationEntry> Children { get; } = [];
}

/// <summary>
/// An error message. Rendered with a red circle prefix.
/// </summary>
public sealed class ErrorEntry(string message) : ConversationEntry
{
    public string Message { get; } = message;
}

/// <summary>
/// A cancellation marker. Rendered as plain "[cancelled]" with no circle.
/// </summary>
public sealed class CancellationEntry : ConversationEntry;
