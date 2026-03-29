namespace Ur.AgentLoop;

/// <summary>
/// Base type for events emitted by the agent loop during a turn.
/// The UI layer subscribes to these to render streaming responses, tool status, etc.
/// </summary>
public abstract class AgentLoopEvent;

/// <summary>
/// A chunk of streaming text from the LLM response.
/// </summary>
public sealed class ResponseChunk : AgentLoopEvent
{
    public required string Text { get; init; }
}

/// <summary>
/// A tool call has been requested by the LLM and is about to execute.
/// </summary>
public sealed class ToolCallStarted : AgentLoopEvent
{
    public required string CallId { get; init; }
    public required string ToolName { get; init; }
}

/// <summary>
/// A tool call has finished executing.
/// </summary>
public sealed class ToolCallCompleted : AgentLoopEvent
{
    public required string CallId { get; init; }
    public required string ToolName { get; init; }
    public required string Result { get; init; }
    public required bool IsError { get; init; }
}

/// <summary>
/// The turn completed (LLM returned a final response with no further tool calls).
/// </summary>
public sealed class TurnCompleted : AgentLoopEvent;

/// <summary>
/// An error occurred during the turn (LLM API failure, etc.).
/// </summary>
public sealed class Error : AgentLoopEvent
{
    public required string Message { get; init; }
    public required bool IsFatal { get; init; }
}
