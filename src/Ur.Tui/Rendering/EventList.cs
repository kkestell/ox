namespace Ur.Tui.Rendering;

/// <summary>
/// Visual style for a tree node in the conversation.
/// <see cref="User"/> items are tree roots (❯ prefix);
/// <see cref="Circle"/> items are tree children (● glyph prefix);
/// <see cref="Plain"/> items render as verbatim text with no chrome.
/// </summary>
internal enum BubbleStyle
{
    /// <summary>
    /// User messages: tree root with ❯ glyph. Starts a new tree group in
    /// <see cref="EventList"/>. All subsequent Circle items become children
    /// of this root until the next User item.
    /// </summary>
    User,
    /// <summary>
    /// Circle-prefixed items (tool calls, assistant messages, subagent blocks):
    /// a ● glyph whose color is supplied by a <c>Func&lt;Color&gt;</c> passed
    /// to <see cref="EventList.Add"/>. Rendered as children of the nearest
    /// preceding User root, or as orphan children if no root precedes them.
    /// </summary>
    Circle,
    /// <summary>
    /// Plain text — child rows are emitted verbatim at full available width
    /// with no tree chrome. Used for informational messages (e.g., the session
    /// banner) that should appear as regular lines of text in the conversation.
    /// </summary>
    Plain
}

/// <summary>
/// The root container for the conversation. Every visible element — assistant
/// messages, user messages, tool calls, subagent blocks — is a child of this list.
///
/// The conversation is rendered as a tree where <see cref="BubbleStyle.User"/>
/// items are roots and all <see cref="BubbleStyle.Circle"/> items are children.
/// Tree-drawing characters (├─, └─, │) connect siblings and parents.
///
/// Target visual:
///
///   ❯ User message
///   │ Continuation wraps here.
///   ├─ ● tool_call(arg: "value")
///   ├─ ● Assistant response text
///   │    wraps like this.
///   └─ ● Another assistant message.
///
/// Items before the first User form an "orphan" group rendered as children
/// with no root (e.g., the welcome message). One blank row separates groups.
///
/// The viewport renders the EventList to get the full set of rows, then displays
/// the tail that fits on screen. Adding a child or mutating any existing child
/// automatically raises <see cref="IRenderable.Changed"/> so the viewport redraws.
/// </summary>
internal sealed class EventList : IRenderable
{
    // ● U+25CF BLACK CIRCLE — status indicator for Circle-style child nodes.
    private const char CircleChar = '●';

    // ❯ U+276F HEAVY RIGHT-POINTING ANGLE QUOTATION MARK — user message root.
    private const char PromptChar = '❯';

    // Tree-drawing box characters used to connect parent and child nodes.
    private const char BranchChar     = '├'; // U+251C — non-last child branch
    private const char LastBranchChar = '└'; // U+2514 — last child branch
    private const char VerticalChar   = '│'; // U+2502 — vertical continuation line
    private const char HorizontalChar = '─'; // U+2500 — horizontal branch connector

    // Chrome widths measured in columns. Content width = availableWidth - chrome.
    // No right-pad or right-margin — tree mode has no background fill.

    // Root chrome: `❯ ` or `│ ` — prompt/vertical char + space.
    private const int RootChrome = 2;

    // Child chrome: `├─ ● ` or `└─ ● ` — branch + horizontal + space + circle + space.
    private const int ChildChrome = 5;

    // Each child is stored with its style and an optional circle-color supplier.
    // The Func<Color>? is only consulted for BubbleStyle.Circle entries; for User
    // items it is ignored. Storing a Func rather than a Color snapshot allows
    // ToolRenderable to return its current state color on every render call, so the
    // circle updates in-place as the tool call progresses.
    private readonly List<(IRenderable Child, BubbleStyle Style, Func<Color>? GetCircleColor)> _children = [];

    public event Action? Changed;

    /// <summary>
    /// Appends a child renderable with the given visual style and subscribes to its
    /// <see cref="IRenderable.Changed"/> event so that mutations to any descendant
    /// bubble up to the viewport's redraw trigger.
    ///
    /// <paramref name="getCircleColor"/> is only used when <paramref name="style"/> is
    /// <see cref="BubbleStyle.Circle"/>. It is called on every render pass so live
    /// objects (like <see cref="ToolRenderable"/>) can return their current state color.
    /// Pass <c>null</c> to use the default white circle.
    /// </summary>
    public void Add(IRenderable child, BubbleStyle style = BubbleStyle.User, Func<Color>? getCircleColor = null)
    {
        _children.Add((child, style, getCircleColor));
        // Subscribe before invoking Changed so the viewport always sees the new
        // child's future updates. Order matters: add → subscribe → notify.
        child.Changed += () => Changed?.Invoke();
        Changed?.Invoke();
    }

    public IReadOnlyList<CellRow> Render(int availableWidth)
    {
        if (_children.Count == 0)
            return [];

        var rows = new List<CellRow>();

        // Walk through _children, partitioning into tree groups at render time.
        // Each User item starts a new group and becomes the root; Circle items
        // following it are its children. Plain items render verbatim wherever they
        // appear. Circle items before the first User form an "orphan" group with
        // tree connectors but no root. Groups are rendered consecutively with no
        // blank separators between them.
        var i = 0;
        while (i < _children.Count)
        {
            if (_children[i].Style == BubbleStyle.Plain)
            {
                // Plain items render verbatim — no tree chrome, no grouping.
                rows.AddRange(_children[i].Child.Render(availableWidth));
                i++;
            }
            else if (_children[i].Style == BubbleStyle.User)
            {
                // Root group: User root followed by zero or more Circle children.
                var rootIndex = i;
                var childStart = i + 1;

                // Scan ahead to find the end of this group (next User/Plain or end of list).
                var childEnd = childStart;
                while (childEnd < _children.Count && _children[childEnd].Style == BubbleStyle.Circle)
                    childEnd++;

                var hasChildren = childEnd > childStart;
                RenderRoot(rows, rootIndex, hasChildren, availableWidth);

                for (var ci = childStart; ci < childEnd; ci++)
                    RenderChild(rows, ci, isLast: ci == childEnd - 1, availableWidth);

                i = childEnd;
            }
            else
            {
                // Orphan group: Circle items that appear before the first User item.
                // Rendered as tree children with ├─/└─ connectors but no root row.
                var childStart = i;
                var childEnd = childStart;
                while (childEnd < _children.Count && _children[childEnd].Style == BubbleStyle.Circle)
                    childEnd++;

                for (var ci = childStart; ci < childEnd; ci++)
                    RenderChild(rows, ci, isLast: ci == childEnd - 1, availableWidth);

                i = childEnd;
            }
        }

        return rows;
    }

    /// <summary>
    /// Renders a User root item: ❯ prefix on the first row, then │ or blank on
    /// continuation rows depending on whether the root has children below it.
    /// </summary>
    private void RenderRoot(List<CellRow> target, int index, bool hasChildren, int availableWidth)
    {
        var contentWidth = Math.Max(1, availableWidth - RootChrome);
        var childRows = _children[index].Child.Render(contentWidth);

        for (var ri = 0; ri < childRows.Count; ri++)
            target.Add(ri == 0
                ? MakeRootRow(childRows[ri])
                : MakeRootContinuationRow(childRows[ri], hasChildren));
    }

    /// <summary>
    /// Renders a Circle child item: ├─ ● or └─ ● prefix on the first row,
    /// then │ + padding or blank padding on continuation rows.
    /// </summary>
    private void RenderChild(List<CellRow> target, int index, bool isLast, int availableWidth)
    {
        var (child, _, getCircleColor) = _children[index];
        var circleColor = getCircleColor?.Invoke() ?? Color.White;
        var contentWidth = Math.Max(1, availableWidth - ChildChrome);
        var childRows = child.Render(contentWidth);

        for (var ri = 0; ri < childRows.Count; ri++)
            target.Add(ri == 0
                ? MakeChildRow(childRows[ri], isLast, circleColor)
                : MakeChildContinuationRow(childRows[ri], isLast));
    }

    // --- Tree chrome helpers ---
    //
    // Each helper builds a CellRow by prepending the appropriate tree connector
    // characters to a child-produced content row. The child cells are copied
    // verbatim — colors and styles are preserved.

    /// <summary>
    /// First row of a tree root: ❯ (white) + space + child content.
    /// </summary>
    private static CellRow MakeRootRow(CellRow childRow)
    {
        var row = new CellRow();
        row.Append(PromptChar, Color.White, Color.Default);
        row.Append(' ', Color.Default, Color.Default);

        foreach (var cell in childRow.Cells)
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Style);

        return row;
    }

    /// <summary>
    /// Continuation row of a tree root: │ + space (when children follow, signaling
    /// the vertical trunk continues) or two spaces (when the root has no children,
    /// so there's nothing to connect to below).
    /// </summary>
    private static CellRow MakeRootContinuationRow(CellRow childRow, bool hasChildren)
    {
        var row = new CellRow();

        if (hasChildren)
        {
            row.Append(VerticalChar, Color.BrightBlack, Color.Default);
            row.Append(' ', Color.Default, Color.Default);
        }
        else
        {
            row.Append("  ", Color.Default, Color.Default);
        }

        foreach (var cell in childRow.Cells)
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Style);

        return row;
    }

    /// <summary>
    /// First row of a tree child: ├─ ● (non-last) or └─ ● (last child in group).
    /// The branch and horizontal characters are dim (BrightBlack); the circle
    /// color reflects the child's live state (e.g., yellow → green as a tool completes).
    /// </summary>
    private static CellRow MakeChildRow(CellRow childRow, bool isLast, Color circleColor)
    {
        var row = new CellRow();

        // Branch connector: ├─ or └─
        row.Append(isLast ? LastBranchChar : BranchChar, Color.BrightBlack, Color.Default);
        row.Append(HorizontalChar, Color.BrightBlack, Color.Default);
        row.Append(' ', Color.Default, Color.Default);

        // Status circle with live color
        row.Append(CircleChar, circleColor, Color.Default);
        row.Append(' ', Color.Default, Color.Default);

        foreach (var cell in childRow.Cells)
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Style);

        return row;
    }

    /// <summary>
    /// Continuation row of a tree child: │ + 4 spaces (non-last, the vertical trunk
    /// continues for siblings below) or 5 spaces (last child, no trunk needed).
    /// Keeps wrapped text aligned with the child's first-row content column.
    /// </summary>
    private static CellRow MakeChildContinuationRow(CellRow childRow, bool isLast)
    {
        var row = new CellRow();

        if (isLast)
        {
            row.Append("     ", Color.Default, Color.Default);
        }
        else
        {
            row.Append(VerticalChar, Color.BrightBlack, Color.Default);
            row.Append("    ", Color.Default, Color.Default);
        }

        foreach (var cell in childRow.Cells)
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Style);

        return row;
    }
}
