namespace Ur.Tui.Rendering;

/// <summary>
/// Groups all rendering output from a single subagent run into a visually
/// bounded box. Renders as a bordered, indented, tail-clipped EventList nested
/// inside the outer conversation EventList — the same bubble-chrome rendering
/// applies inside the subagent box as in the outer conversation.
///
/// Structure (one subagent box):
///
///   [indent]─── subagent {id} ──────────────────────  ← header
///   [indent]▎   assistant text                         ← inner EventList rows
///   [indent]▎   tool_call(…) → ok                      ←   (clipped to MaxInnerRows)
///   [indent]─────────────────────────────────────────  ← footer (always present)
///
/// The header and footer are structural — they appear regardless of completion
/// state. <see cref="SetCompleted"/> exists for the defensive-finalization
/// contract and may be used for future polish (e.g. dimming the header).
///
/// This mirrors the Viewport → EventList relationship one level deeper:
/// Viewport tail-clips the outer EventList; SubagentRenderable tail-clips
/// its inner EventList. Same pattern, one nesting level lower.
/// </summary>
internal sealed class SubagentRenderable : IRenderable
{
    // Space cells prepended to each inner row for visual nesting.
    private const int IndentWidth = 0;

    // Maximum number of inner rows shown between header and footer.
    // Older rows scroll off visually once this limit is reached — same
    // tail-clip logic as Viewport.Redraw().
    private const int MaxInnerRows = 20;

    // ─ U+2500 BOX DRAWINGS LIGHT HORIZONTAL — used for header/footer rules.
    private const char RuleChar = '─';

    // The inner EventList handles bubble chrome for the subagent's children.
    // This is private — EventRouter interacts only through AddChild().
    private readonly EventList _innerList = new();

    private bool _completed;

    private string SubagentId { get; }

    public event Action? Changed;

    public SubagentRenderable(string subagentId)
    {
        SubagentId = subagentId;
        // Forward inner list changes upward so the viewport redraws when
        // any subagent child is added or mutated.
        _innerList.Changed += () => Changed?.Invoke();
    }

    /// <summary>
    /// Appends a child renderable to the inner EventList with the given bubble style.
    /// The style determines what chrome (bar color, background) the child gets inside
    /// the subagent box — same styles as the outer conversation.
    /// </summary>
    public void AddChild(IRenderable child, BubbleStyle style)
    {
        // Delegate to the inner EventList; it subscribes to child.Changed and
        // fires its own Changed, which propagates to us via the constructor subscription.
        _innerList.Add(child, style);
    }

    /// <summary>
    /// Marks the subagent run as complete. The footer is structural (always rendered),
    /// so this method no longer controls row presence. It exists to fulfill the
    /// defensive-finalization contract — ToolCallCompleted calls it as a fallback
    /// in case TurnCompleted is not emitted — and may be used for future visual
    /// polish (e.g. dimming the subagent ID in the header).
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

        // Inner list renders at a narrower width to leave room for the indent.
        var innerWidth = Math.Max(1, availableWidth - IndentWidth);
        var innerRows  = _innerList.Render(innerWidth);

        // Tail-clip to MaxInnerRows — mirrors Viewport.Redraw()'s startIndex logic.
        var startIndex = Math.Max(0, innerRows.Count - MaxInnerRows);

        rows.Add(MakeHeaderRow(availableWidth));

        for (var i = startIndex; i < innerRows.Count; i++)
            rows.Add(IndentRow(innerRows[i]));

        rows.Add(MakeFooterRow(availableWidth));

        return rows;
    }

    /// <summary>
    /// Builds the header: [indent spaces] + "─── subagent {id} " + [─ fill to edge].
    /// The rule fills the full available width so the border is always terminal-wide.
    /// </summary>
    private CellRow MakeHeaderRow(int availableWidth)
    {
        var label     = $"─── subagent {SubagentId} ";
        var fillCount = Math.Max(0, availableWidth - IndentWidth - label.Length);
        var line      = label + new string(RuleChar, fillCount);

        var row = new CellRow();
        row.Append(new string(' ', IndentWidth), Color.Default,      Color.Default);
        row.Append(line,                          Color.BrightBlack, Color.Default);
        return row;
    }

    /// <summary>
    /// Builds the footer: [indent spaces] + [─ fill to edge].
    /// Always present (not gated on _completed) so the box has a structural bottom.
    /// </summary>
    private static CellRow MakeFooterRow(int availableWidth)
    {
        var fillCount = Math.Max(0, availableWidth - IndentWidth);

        var row = new CellRow();
        row.Append(new string(' ', IndentWidth),      Color.Default,      Color.Default);
        row.Append(new string(RuleChar, fillCount),   Color.BrightBlack, Color.Default);
        return row;
    }

    /// <summary>
    /// Prepends IndentWidth space cells to a child row. The child cells are copied
    /// verbatim — colors and styles are preserved. This pushes the inner EventList's
    /// own chrome (margin + bar + inner-pad) inward by IndentWidth columns.
    /// </summary>
    private static CellRow IndentRow(CellRow childRow)
    {
        var row = new CellRow();
        row.Append(new string(' ', IndentWidth), Color.Default, Color.Default);
        foreach (var cell in childRow.Cells)
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Style);
        return row;
    }
}
