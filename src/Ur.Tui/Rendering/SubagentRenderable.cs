namespace Ur.Tui.Rendering;

/// <summary>
/// Groups all rendering output from a single subagent run into a visually
/// bounded block. Replaces the >>>> prefix convention with explicit header/footer
/// lines, making it obvious which events belong to which subagent.
///
/// Like <see cref="EventList"/>, a SubagentRenderable is a container: it holds
/// its own sequence of child renderables (the subagent's text, tools, etc.) and
/// propagates their <see cref="IRenderable.Changed"/> events upward.
///
/// The subagent-specific child list lives here rather than in a shared base class
/// because subagent rendering has its own indentation and header/footer logic that
/// does not generalize to the root EventList.
/// </summary>
internal sealed class SubagentRenderable : IRenderable
{
    private const string DarkGray = "\e[90m";
    private const string Reset    = "\e[0m";

    // The indent prefix applied to every child line so subagent output is
    // visually nested inside the header/footer markers.
    private const string Indent = "  ";

    private readonly List<IRenderable> _children = [];
    private bool _completed;

    public string SubagentId { get; }

    public event Action? Changed;

    public SubagentRenderable(string subagentId)
    {
        SubagentId = subagentId;
    }

    /// <summary>
    /// Appends a child renderable (text block, tool call, etc.) to this subagent's
    /// output and subscribes to its <see cref="IRenderable.Changed"/> event so that
    /// any update from the subagent bubbles up to the viewport.
    /// </summary>
    public void AddChild(IRenderable child)
    {
        _children.Add(child);
        // Propagate child changes upward — the viewport only subscribes to the
        // root EventList's Changed event, so changes must bubble all the way up.
        child.Changed += () => Changed?.Invoke();
        Changed?.Invoke();
    }

    /// <summary>
    /// Marks the subagent run as complete and adds a footer line to bound the block.
    /// Idempotent — safe to call multiple times (e.g. via both SubagentEvent and
    /// the parent ToolCallCompleted handler as a defensive fallback).
    /// </summary>
    public void SetCompleted()
    {
        if (_completed)
            return;
        _completed = true;
        Changed?.Invoke();
    }

    public IReadOnlyList<string> Render(int availableWidth)
    {
        var lines = new List<string>();

        // Header line — visually opens the subagent block.
        lines.Add($"{DarkGray}--- subagent {SubagentId} ---{Reset}");

        // Render each child with a 2-space indent. Reduce availableWidth to
        // account for the indent so children still word-wrap correctly.
        var childWidth = Math.Max(1, availableWidth - Indent.Length);
        foreach (var child in _children)
        {
            foreach (var line in child.Render(childWidth))
            {
                lines.Add(Indent + line);
            }
        }

        // Footer only appears once the subagent run is complete.
        if (_completed)
            lines.Add($"{DarkGray}--- subagent complete ---{Reset}");

        return lines;
    }
}
