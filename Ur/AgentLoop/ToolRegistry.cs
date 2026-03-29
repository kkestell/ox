using Microsoft.Extensions.AI;

namespace Ur.AgentLoop;

/// <summary>
/// Maintains available tools. Core provides the registry; extensions register tools into it.
/// Tools are exposed to the LLM as AITool instances and dispatched by name.
/// </summary>
public sealed class ToolRegistry
{
    private readonly Dictionary<string, AIFunction> _tools = new(StringComparer.Ordinal);

    /// <summary>
    /// Registers a tool. Overwrites any existing tool with the same name.
    /// </summary>
    public void Register(AIFunction tool)
    {
        _tools[tool.Name] = tool;
    }

    /// <summary>
    /// Removes a tool by name. Returns true if it was found and removed.
    /// </summary>
    public bool Remove(string name) => _tools.Remove(name);

    /// <summary>
    /// Looks up a tool by name. Returns null if not found.
    /// </summary>
    public AIFunction? Get(string name) =>
        _tools.GetValueOrDefault(name);

    /// <summary>
    /// Returns all registered tools as AITool instances for passing to ChatOptions.
    /// </summary>
    public IList<AITool> All() => _tools.Values.ToList<AITool>();
}
