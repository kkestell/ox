using System.Text.Json;
using Microsoft.Extensions.AI;
using Ox.Agent.Todo;

namespace Ox.Agent.Tools;

/// <summary>
/// Built-in tool that lets the LLM maintain a task list during a conversation.
///
/// Each call replaces the entire todo list atomically (same semantics as
/// OpenCode's <c>todowrite</c>). The LLM sends a full array of items; there
/// are no add/remove/patch operations. This simplifies both the schema and
/// the store — the LLM always has the canonical list.
///
/// Permission: Read (no filesystem or execution side effects). Auto-allowed,
/// never prompts the user.
/// </summary>
internal sealed class TodoWriteTool(TodoStore? store) : AIFunction
{
    private static readonly JsonElement Schema = JsonDocument.Parse("""
        {
            "type": "object",
            "properties": {
                "todos": {
                    "type": "array",
                    "description": "The complete todo list. Every call replaces the entire list.",
                    "items": {
                        "type": "object",
                        "properties": {
                            "content": {
                                "type": "string",
                                "description": "Brief task description in imperative form."
                            },
                            "status": {
                                "type": "string",
                                "enum": ["pending", "in_progress", "completed"],
                                "description": "pending = not started, in_progress = currently working, completed = done."
                            }
                        },
                        "required": ["content", "status"],
                        "additionalProperties": false
                    }
                }
            },
            "required": ["todos"],
            "additionalProperties": false
        }
        """).RootElement.Clone();

    public override string Name => "todo_write";

    public override string Description =>
        """
        Update the task list displayed in the conversation. Use this for multi-step tasks
        (3+ steps) to track progress. Each call replaces the entire list — always send
        all items, not just changed ones. Mark items "in_progress" before starting and
        "completed" when done. Keep at most one item "in_progress" at a time.
        """;

    public override JsonElement JsonSchema => Schema;

    protected override ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        // Graceful degradation: if no store was provided (e.g. test harness
        // without a session), return an error string rather than throwing.
        if (store is null)
            return new ValueTask<object?>("Todo store not available.");

        if (!arguments.TryGetValue("todos", out var todosArg) || todosArg is null)
            return new ValueTask<object?>("Missing required parameter: todos");

        var items = TodoJson.Parse(todosArg);
        store.Update(items);

        return new ValueTask<object?>($"Todo list updated ({items.Count} items).");
    }
}
