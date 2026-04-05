using Microsoft.Extensions.AI;
using Ur.Permissions;

namespace Ur.Tools;

/// <summary>
/// Maintains available tools. Core provides the registry; extensions register tools into it.
/// Tools are exposed to the LLM as AITool instances and dispatched by name.
/// </summary>
public sealed class ToolRegistry
{
    private readonly Dictionary<string, AIFunction> _tools = new(StringComparer.Ordinal);
    // Parallel metadata dictionary — same key as _tools. Not every tool needs an entry;
    // absence means "treat as Write" (conservative default).
    private readonly Dictionary<string, PermissionMeta> _meta = new(StringComparer.Ordinal);

    // Cached tool list — invalidated on Register/Remove to avoid allocating
    // a new list on every call to All() (which runs each agent loop iteration).
    private IReadOnlyList<AITool>? _allCache;

    /// <summary>
    /// Registers a tool with optional permission metadata. When metadata is omitted,
    /// the tool is treated as Write (the conservative safe choice).
    ///
    /// A single overload replaces the former public/internal pair — optional parameters
    /// cover both "just the tool" and "tool with explicit permission metadata" use cases.
    /// </summary>
    public void Register(
        AIFunction tool,
        OperationType operationType = OperationType.Write,
        string? extensionId = null,
        ITargetExtractor? targetExtractor = null)
    {
        _tools[tool.Name] = tool;
        _meta[tool.Name] = new PermissionMeta(operationType, extensionId, targetExtractor);
        _allCache = null;
    }

    /// <summary>
    /// Returns the permission metadata for a tool, or null if none was registered.
    /// Callers should fall back to WriteInWorkspace when null is returned.
    /// </summary>
    internal PermissionMeta? GetPermissionMeta(string toolName) =>
        _meta.GetValueOrDefault(toolName);

    /// <summary>
    /// Looks up a tool by name. Returns null if not found.
    /// </summary>
    public AIFunction? Get(string name) =>
        _tools.GetValueOrDefault(name);

    /// <summary>
    /// Copies all tools (and their metadata) from this registry into <paramref name="target"/>.
    /// Last-write-wins: tools in this registry will overwrite same-named tools in the target.
    /// Used by tests to inject fake tools into session registries.
    /// </summary>
    internal void MergeInto(ToolRegistry target)
    {
        foreach (var (name, tool) in _tools)
        {
            var meta = _meta.GetValueOrDefault(name);
            if (meta is not null)
                target.Register(tool, meta.OperationType, meta.ExtensionId, meta.TargetExtractor);
            else
                target.Register(tool);
        }
    }

    /// <summary>
    /// Returns all registered tools as AITool instances for passing to ChatOptions.
    /// Returns a read-only list to prevent accidental mutation of the cached snapshot.
    /// </summary>
    public IReadOnlyList<AITool> All() => _allCache ??= _tools.Values.ToList<AITool>();
}
