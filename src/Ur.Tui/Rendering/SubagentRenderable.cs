namespace Ur.Tui.Rendering;

/// <summary>
/// Groups all rendering output from a single subagent run into a tree-renderable
/// block. The outer EventList treats this as a regular <see cref="BubbleStyle.Circle"/>
/// child — it adds tree connectors (├─ ● or └─ ●) to the first row and continuation
/// chrome (│ or spaces) to subsequent rows.
///
/// Structure as seen after outer EventList applies tree chrome:
///
///   ├─ ● run_subagent(prompt: "...")      ← row 0: tool call signature
///   │    ├─ ● read_file(path: "foo.txt")  ← rows 1+: inner EventList tree output
///   │    └─ ● write_file(path: "bar.txt")
///
/// Row 0 is the tool call signature (dark gray, same convention as ToolRenderable).
/// Rows 1+ come from the inner EventList, which renders the subagent's children
/// with its own tree connectors. The inner list is tail-clipped to
/// <see cref="MaxInnerRows"/>; when clipping occurs, a ├─ ● ... ellipsis row
/// is prepended to signal that older events were dropped.
///
/// This mirrors the Viewport → EventList relationship one level deeper:
/// Viewport tail-clips the outer EventList; SubagentRenderable tail-clips
/// its inner EventList. Same pattern, one nesting level lower.
/// </summary>
internal sealed class SubagentRenderable : IRenderable
{
    // Maximum number of inner rows shown below the tool signature.
    // Older rows scroll off visually once this limit is reached — same
    // tail-clip logic as Viewport.Redraw().
    private const int MaxInnerRows = 20;

    // The inner EventList handles tree chrome for the subagent's children.
    // This is private — EventRouter interacts only through AddChild().
    private readonly EventList _innerList = new();

    // The formatted tool call string shown as the first row (e.g., "run_subagent(prompt: '...')").
    private readonly string _formattedCall;

    private bool _completed;

    public event Action? Changed;

    /// <summary>
    /// The color to use for the ● glyph when this subagent is rendered as a
    /// Circle child in the outer EventList. Evaluated on every render pass so
    /// the circle updates in-place: yellow while running, green on completion.
    /// </summary>
    public Color CircleColor => _completed ? Color.Green : Color.Yellow;

    public SubagentRenderable(string subagentId, string formattedCall)
    {
        // subagentId is the 8-char hex ID from SubagentRunner. Not rendered, but
        // kept in the constructor signature because EventRouter identifies subagents
        // by this ID and it may be useful for future diagnostics.
        _ = subagentId;
        _formattedCall = formattedCall;

        // Forward inner list changes upward so the viewport redraws when
        // any subagent child is added or mutated.
        _innerList.Changed += () => Changed?.Invoke();
    }

    /// <summary>
    /// Appends a child renderable to the inner EventList with the given bubble style.
    /// The style determines what tree chrome the child gets inside the subagent's
    /// inner tree — same styles as the outer conversation.
    ///
    /// <paramref name="getCircleColor"/> is only consulted when <paramref name="style"/>
    /// is <see cref="BubbleStyle.Circle"/>; pass null for a default white circle.
    /// </summary>
    public void AddChild(IRenderable child, BubbleStyle style, Func<Color>? getCircleColor = null)
    {
        // Delegate to the inner EventList; it subscribes to child.Changed and
        // fires its own Changed, which propagates to us via the constructor subscription.
        _innerList.Add(child, style, getCircleColor);
    }

    /// <summary>
    /// Marks the subagent run as complete. Transitions <see cref="CircleColor"/>
    /// from yellow to green. Exists for the defensive-finalization contract —
    /// ToolCallCompleted calls it as a fallback in case TurnCompleted is not emitted.
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

        // Row 0: tool call signature in dark gray — always present.
        var sigRow = new CellRow();
        sigRow.Append(_formattedCall, Color.BrightBlack, Color.Default);
        rows.Add(sigRow);

        // Rows 1+: inner EventList tree output, tail-clipped to MaxInnerRows.
        // The inner list renders at the full availableWidth because the outer
        // EventList's child chrome (5 cols) is already subtracted before calling
        // this Render() — we don't double-indent.
        var innerRows = _innerList.Render(availableWidth);
        var startIndex = Math.Max(0, innerRows.Count - MaxInnerRows);

        // When tail-clipping drops older events, prepend an ellipsis row to signal
        // that the visible output is not the complete history. The ellipsis uses
        // ├─ (never └─) because clipped events are by definition not the last child.
        if (startIndex > 0)
            rows.Add(MakeEllipsisRow());

        for (var i = startIndex; i < innerRows.Count; i++)
            rows.Add(innerRows[i]);

        return rows;
    }

    /// <summary>
    /// Builds a static ellipsis row in the same tree-child format as a regular
    /// Circle child: ├─ ● ... in dim BrightBlack to indicate truncation.
    /// Uses <see cref="TreeChrome"/> constants so the characters stay in sync
    /// with the rest of the tree rendering.
    /// </summary>
    private static CellRow MakeEllipsisRow()
    {
        var row = new CellRow();
        row.Append(TreeChrome.BranchChar, Color.BrightBlack, Color.Default);
        row.Append(TreeChrome.HorizontalChar, Color.BrightBlack, Color.Default);
        row.Append(' ', Color.BrightBlack, Color.Default);
        row.Append(TreeChrome.CircleChar, Color.BrightBlack, Color.Default);
        row.Append(" ...", Color.BrightBlack, Color.Default);
        return row;
    }
}
