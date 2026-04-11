using System.Text.Json;
using Ur.Todo;

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

    // Friendly display names for built-in tools. Snake_case API names are mapped
    // to concise PascalCase names that read better in the TUI. Unknown tools
    // (extensions, MCP tools, etc.) fall back to auto-PascalCase conversion.
    private static readonly Dictionary<string, string> DisplayNames = new()
    {
        ["bash"]          = "Bash",
        ["read_file"]     = "Read",
        ["write_file"]    = "Write",
        ["update_file"]   = "Edit",
        ["glob"]          = "Glob",
        ["grep"]          = "Grep",
        ["run_subagent"]  = "Subagent",
        ["todo_write"]    = "Plan",
    };

    /// <summary>
    /// Maps a snake_case tool name to a friendly display name. Known tools use
    /// the <see cref="DisplayNames"/> dictionary; unknown tools are converted
    /// to PascalCase by capitalizing each underscore-separated segment.
    /// </summary>
    private static string GetDisplayName(string toolName)
    {
        if (DisplayNames.TryGetValue(toolName, out var display))
            return display;

        // Fallback: capitalize first letter of each _-separated word.
        return string.Concat(
            toolName.Split('_')
                .Where(s => s.Length > 0)
                .Select(s => char.ToUpperInvariant(s[0]) + s[1..]));
    }

    /// <summary>
    /// Formats the call for display. Most tools render as
    /// <c>DisplayName("val1", "val2")</c>; <c>todo_write</c> renders as a
    /// multi-line plan block so the user sees task state instead of raw JSON.
    /// </summary>
    public string FormatCall()
    {
        if (ToolName == "todo_write")
            return FormatTodoWriteCall();

        const int maxLen = 40;
        var name = GetDisplayName(ToolName);

        if (Arguments.Count == 0)
            return name;

        var parts = Arguments.Select(kvp =>
        {
            var val = FormatValue(kvp.Value);
            var display = val.Length > maxLen ? val[..maxLen] + "..." : val;
            return $"\"{display}\"";
        });

        return $"{name}({string.Join(", ", parts)})";
    }

    /// <summary>
    /// Renders the todo tool as a compact plan block so the conversation stream
    /// can replace the old sidebar without forcing the user to read raw JSON.
    /// </summary>
    private string FormatTodoWriteCall()
    {
        const string header = "Plan";
        if (!Arguments.TryGetValue("todos", out var todosArg) || todosArg is null)
            return header;

        if (!TryReadTodoLines(todosArg, out var lines))
            return header;

        if (lines.Count == 0)
            return $"{header}\n(cleared)";

        return string.Join("\n", new[] { header }.Concat(lines));
    }

    // Collapse newlines so tool args render on a single line in the TUI.
    private static string FormatValue(object? value)
    {
        var raw = value switch
        {
            null => "null",
            JsonElement { ValueKind: JsonValueKind.String } je => je.GetString() ?? "",
            _ => value.ToString() ?? ""
        };
        return raw.ReplaceLineEndings(" ");
    }

    private static bool TryReadTodoLines(object todosArg, out List<string> lines)
    {
        lines = [];

        if (todosArg is not JsonElement { ValueKind: JsonValueKind.Array } array)
            return false;

        foreach (var element in array.EnumerateArray())
        {
            if (!element.TryGetProperty("content", out var contentProp)
                || !element.TryGetProperty("status", out var statusProp))
            {
                return false;
            }

            var content = contentProp.GetString();
            var status = statusProp.GetString();
            if (string.IsNullOrWhiteSpace(content) || string.IsNullOrWhiteSpace(status))
                return false;

            var marker = status switch
            {
                "completed" => "\u2713",
                "in_progress" => "\u25cf",
                "pending" => "\u25cb",
                _ => "?"
            };

            lines.Add($"{marker} {content.ReplaceLineEndings(" ")}");
        }

        return true;
    }
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
/// A tool call is waiting for user permission before it can execute.
///
/// Emitted by the parallel dispatch pipeline right before calling
/// <see cref="Permissions.TurnCallbacks.RequestPermissionAsync"/>. The UI layer
/// uses this to transition the correct tool renderable to the "awaiting approval"
/// visual state — replacing the old <c>_lastStartedTool</c> side-channel that
/// assumed sequential execution.
/// </summary>
public sealed class ToolAwaitingApproval : AgentLoopEvent
{
    public required string CallId { get; init; }
}

/// <summary>
/// The turn completed (LLM returned a final response with no further tool calls).
/// Carries the last LLM call's input token count so the UI can display context fill.
/// Nullable because some providers don't report usage data.
/// </summary>
public sealed class TurnCompleted : AgentLoopEvent
{
    public long? InputTokens { get; init; }
}

/// <summary>
/// An error occurred during the turn (LLM API failure, etc.).
/// </summary>
public sealed class TurnError : AgentLoopEvent
{
    public required string Message { get; init; }
    public required bool IsFatal { get; init; }
}

/// <summary>
/// The conversation was compacted: older messages were summarized to reclaim
/// context window space. Yielded by UrSession (not AgentLoop) after a
/// successful autocompact so the UI can display an informational system message.
/// </summary>
public sealed class Compacted : AgentLoopEvent
{
    public required string Message { get; init; }
}

/// <summary>
/// The todo list was updated by the LLM via the <c>todo_write</c> tool.
///
/// This event bridges the Ur and Ox layers. Currently not emitted by the agent
/// loop; the conversation UI renders <c>todo_write</c> directly from the tool
/// call arguments instead. Defined
/// here as a forward-looking extension point for future event-driven wiring.
/// </summary>
public sealed class TodoUpdated : AgentLoopEvent
{
    public required IReadOnlyList<TodoItem> Items { get; init; }
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
