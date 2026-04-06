namespace Ur.Tui.Rendering;

/// <summary>
/// Groups all rendering output from a single subagent run into a visually
/// bounded block. Emits a header row before the children and an optional footer
/// row once the run completes.
///
/// Like <see cref="EventList"/>, a SubagentRenderable is a container: it holds
/// its own sequence of child renderables (the subagent's text, tools, etc.) and
/// propagates their <see cref="IRenderable.Changed"/> events upward.
///
/// Indentation is applied here as prepended space cells on each child row.
/// Children are unaware of the indent — they render at their own content width
/// (availableWidth minus indent size) and SubagentRenderable wraps each row.
/// </summary>
internal sealed class SubagentRenderable : IRenderable
{
    // Number of space cells prepended to each child row to create visual nesting.
    private const int IndentWidth = 2;

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
    /// Marks the subagent run as complete and adds a footer row to bound the block.
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

    public IReadOnlyList<CellRow> Render(int availableWidth)
    {
        var rows = new List<CellRow>();

        // Header row — visually opens the subagent block.
        rows.Add(CellRow.FromText($"--- subagent {SubagentId} ---", Color.BrightBlack, Color.Default));

        // Render each child with an IndentWidth-space prefix. Reduce availableWidth to
        // account for the indent so children still word-wrap correctly.
        var childWidth = Math.Max(1, availableWidth - IndentWidth);
        foreach (var child in _children)
        {
            foreach (var childRow in child.Render(childWidth))
            {
                // Build a new row with the indent cells prepended to the child's cells.
                var indented = new CellRow();
                indented.Append(new string(' ', IndentWidth), Color.Default, Color.Default);
                foreach (var cell in childRow.Cells)
                    indented.Append(cell.Rune, cell.Foreground, cell.Background, cell.Style);
                rows.Add(indented);
            }
        }

        // Footer appears once the subagent run is complete.
        if (_completed)
            rows.Add(CellRow.FromText("--- subagent complete ---", Color.BrightBlack, Color.Default));

        return rows;
    }
}
