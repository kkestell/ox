namespace Ur.Tui.Dummy;

public abstract class DummyAgentLoopEvent;

public sealed class DummyResponseChunk : DummyAgentLoopEvent
{
    public required string Text { get; init; }
}

public sealed class DummyToolCallStarted : DummyAgentLoopEvent
{
    public required string CallId { get; init; }
    public required string ToolName { get; init; }
}

public sealed class DummyToolCallCompleted : DummyAgentLoopEvent
{
    public required string CallId { get; init; }
    public required string ToolName { get; init; }
    public required string Result { get; init; }
    public required bool IsError { get; init; }
}

public sealed class DummyTurnCompleted : DummyAgentLoopEvent;

public sealed class DummyError : DummyAgentLoopEvent
{
    public required string Message { get; init; }
    public required bool IsFatal { get; init; }
}
