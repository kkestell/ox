using Microsoft.Extensions.AI;
using Ur.Permissions;

namespace Ur.Tools;

/// <summary>
/// Maintains available tools. Tools are exposed to the LLM as AITool instances
/// and dispatched by name.
/// </summary>
public sealed class ToolRegistry
{
    // Single dictionary keyed by tool name. Each entry holds the tool function and
    // its permission metadata together, eliminating the synchronization invariant
    // that two parallel dictionaries would require.
    private readonly Dictionary<string, (AIFunction Tool, PermissionMeta Meta)> _entries = new(StringComparer.Ordinal);

    // Cached tool list — invalidated on Register to avoid allocating a new list
    // on every call to All() (which runs each agent loop iteration).
    private IReadOnlyList<AITool>? _allCache;

    /// <summary>
    /// Registers a tool with optional permission metadata. When metadata is omitted,
    /// the tool is treated as Write (the conservative safe choice).
    /// </summary>
    public void Register(
        AIFunction tool,
        OperationType operationType = OperationType.Write,
        ITargetExtractor? targetExtractor = null)
    {
        _entries[tool.Name] = (tool, new PermissionMeta(operationType, targetExtractor));
        _allCache = null;
    }

    /// <summary>
    /// Returns the permission metadata for a tool, or null if none was registered.
    /// Callers should fall back to WriteInWorkspace when null is returned.
    /// </summary>
    internal PermissionMeta? GetPermissionMeta(string toolName) =>
        _entries.TryGetValue(toolName, out var entry) ? entry.Meta : null;

    /// <summary>
    /// Looks up a tool by name. Returns null if not found.
    /// </summary>
    public AIFunction? Get(string name) =>
        _entries.TryGetValue(name, out var entry) ? entry.Tool : null;

    /// <summary>
    /// Returns a new registry containing all tools from this one except those whose
    /// names appear in <paramref name="excludedNames"/>.
    ///
    /// Used by <c>SubagentRunner</c> to produce a child registry that omits
    /// <c>run_subagent</c>, preventing direct self-recursion without needing a
    /// depth counter. All permission metadata is preserved on the kept tools.
    /// </summary>
    internal ToolRegistry FilteredCopy(params string[] excludedNames)
    {
        var excluded = new HashSet<string>(excludedNames, StringComparer.Ordinal);
        var copy = new ToolRegistry();

        foreach (var (name, (tool, meta)) in _entries)
        {
            if (excluded.Contains(name))
                continue;

            copy.Register(tool, meta.OperationType, meta.TargetExtractor);
        }

        return copy;
    }

    /// <summary>
    /// Copies all tools (and their metadata) from this registry into <paramref name="target"/>.
    /// Last-write-wins: tools in this registry will overwrite same-named tools in the target.
    /// Used by tests to inject fake tools into session registries.
    /// </summary>
    internal void MergeInto(ToolRegistry target)
    {
        foreach (var (_, (tool, meta)) in _entries)
            target.Register(tool, meta.OperationType, meta.TargetExtractor);
    }

    /// <summary>
    /// Returns all registered tools as AITool instances for passing to ChatOptions.
    /// Returns a read-only list to prevent accidental mutation of the cached snapshot.
    /// </summary>
    public IReadOnlyList<AITool> All() => _allCache ??= new List<AITool>(_entries.Values.Select(e => e.Tool));
}
