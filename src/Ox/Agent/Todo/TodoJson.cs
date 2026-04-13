using System.Text.Json;

namespace Ox.Agent.Todo;

/// <summary>
/// Parses the JSON "todos" argument shape sent by the LLM into concrete
/// <see cref="TodoItem"/> values.
///
/// Lives here rather than on <see cref="Tools.TodoWriteTool"/> because the
/// knowledge of how todos are shaped on the wire belongs alongside the domain
/// type it produces. The tool only needs to own the schema it advertises and
/// the side effect of calling <c>store.Update</c>; parsing is a pure
/// <c>object → List&lt;TodoItem&gt;</c> transformation and does not need tool context.
/// </summary>
internal static class TodoJson
{
    /// <summary>
    /// Parses the "todos" argument into a list of <see cref="TodoItem"/>.
    /// The LLM sends a JSON array with <c>content</c> and <c>status</c> on
    /// each item; unknown statuses and missing fields throw so the tool can
    /// surface the validation error back to the LLM as a tool result.
    /// </summary>
    public static List<TodoItem> Parse(object todosArg)
    {
        var items = new List<TodoItem>();

        var array = todosArg switch
        {
            JsonElement { ValueKind: JsonValueKind.Array } je => je.EnumerateArray(),
            _ => throw new ArgumentException("Expected a JSON array for 'todos'.")
        };

        foreach (var element in array)
        {
            var content = element.GetProperty("content").GetString()
                ?? throw new ArgumentException("Todo item missing 'content'.");
            var statusStr = element.GetProperty("status").GetString()
                ?? throw new ArgumentException("Todo item missing 'status'.");

            var status = statusStr switch
            {
                "pending" => TodoStatus.Pending,
                "in_progress" => TodoStatus.InProgress,
                "completed" => TodoStatus.Completed,
                _ => throw new ArgumentException($"Unknown todo status: '{statusStr}'.")
            };

            items.Add(new TodoItem(content, status));
        }

        return items;
    }
}
