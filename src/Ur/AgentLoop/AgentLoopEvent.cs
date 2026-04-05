using System.Text.Json;

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
    // ReSharper disable once UnusedAutoPropertyAccessor.Global — init-only; retained for event correlation and serialization
    public required string CallId { get; init; }
    public required string ToolName { get; init; }
    // Arguments are carried here so UIs can render an informative status line
    // without needing to intercept or re-parse the raw LLM message.
    public required IDictionary<string, object?> Arguments { get; init; }

    /// <summary>
    /// Formats the call as <c>tool_name(key: "val", ...)</c> for display.
    /// Each argument value is truncated to 40 characters to avoid flooding narrow terminals.
    /// </summary>
    public string FormatCall()
    {
        const int maxLen = 40;

        if (Arguments.Count == 0)
            return ToolName;

        var parts = Arguments.Select(kvp =>
        {
            var val = FormatValue(kvp.Value);
            var display = val.Length > maxLen ? val[..maxLen] + "..." : val;
            return $"{kvp.Key}: \"{display}\"";
        });

        return $"{ToolName}({string.Join(", ", parts)})";
    }

    private static string FormatValue(object? value) => value switch
    {
        null => "null",
        JsonElement { ValueKind: JsonValueKind.String } je => je.GetString() ?? "",
        _ => value.ToString() ?? ""
    };
}

/// <summary>
/// A tool call has finished executing.
/// </summary>
public sealed class ToolCallCompleted : AgentLoopEvent
{
    // ReSharper disable once UnusedAutoPropertyAccessor.Global — init-only; retained for event correlation and serialization
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

/// <summary>
/// A relay envelope that wraps an event emitted by a running sub-agent.
///
/// Architecture: rather than tagging every event type with an optional SubagentId field,
/// we wrap at the boundary. SubagentRunner produces these; the UI layer unwraps them
/// and renders with a visual prefix so the user can see what the sub-agent is doing.
/// The SubagentId is a short identifier that can later be used to group or indent
/// concurrent sub-agent streams without changing this envelope's shape.
/// </summary>
public sealed class SubagentEvent : AgentLoopEvent
{
    // The short ID (8-char hex) of the sub-agent that emitted this event.
    // Generated once per SubagentRunner.RunAsync call; consistent across all events from that run.
    public required string SubagentId { get; init; }

    // The inner event exactly as produced by the sub-agent's loop.
    public required AgentLoopEvent Inner { get; init; }
}
