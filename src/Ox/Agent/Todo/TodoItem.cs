using System.Text.Json.Serialization;

namespace Ox.Agent.Todo;

/// <summary>
/// Status of a single todo item. Values use snake_case JSON serialization to
/// match the schema the LLM sends (e.g. "in_progress"), avoiding a custom
/// JSON converter.
/// </summary>
[JsonConverter(typeof(JsonStringEnumConverter<TodoStatus>))]
public enum TodoStatus
{
    [JsonStringEnumMemberName("pending")]
    Pending,

    [JsonStringEnumMemberName("in_progress")]
    InProgress,

    [JsonStringEnumMemberName("completed")]
    Completed
}

/// <summary>
/// A single task entry tracked during a conversation. Immutable — the LLM
/// replaces the entire list on every <c>todo_write</c> call rather than
/// patching individual items.
/// </summary>
public record TodoItem(string Content, TodoStatus Status);
