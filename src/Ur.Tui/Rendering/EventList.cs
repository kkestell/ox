namespace Ur.Tui.Rendering;

/// <summary>
/// Visual style for a tree node in the conversation.
/// <see cref="User"/> items are top-level Circle children (blue ● prefix);
/// <see cref="Circle"/> items are nested tree children (● glyph prefix);
/// <see cref="Plain"/> items render as verbatim text with no chrome.
/// </summary>
internal enum BubbleStyle
{
    /// <summary>
    /// User messages: top-level Circle child with a blue ● glyph. Starts a
    /// new tree group in <see cref="EventList"/>. All subsequent Circle items
    /// become nested children underneath this item until the next User item.
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
/// The conversation is rendered as a single continuous tree hanging off the session
/// banner. <see cref="BubbleStyle.User"/> items are top-level Circle children (blue ●)
/// and their <see cref="BubbleStyle.Circle"/> items are nested one level deeper.
/// Tree-drawing characters (├─, └─, │) connect siblings and parents at both levels.
///
/// Target visual:
///
///   Session: 20260406-185135-171
///   ├─ ● User message
///   │    Continuation wraps here.
///   │  ├─ ● tool_call(arg: "value")
///   │  ├─ ● Assistant response text
///   │  │    wraps like this.
///   │  └─ ● Another assistant message.
///   └─ ● Second user message
///      └─ ● Response
///
/// Items before the first User form an "orphan" group rendered as top-level
/// children with no parent (e.g., the welcome message).
///
/// The viewport renders the EventList to get the full set of rows, then displays
/// the tail that fits on screen. Adding a child or mutating any existing child
/// automatically raises <see cref="IRenderable.Changed"/> so the viewport redraws.
/// </summary>
internal sealed class EventList : IRenderable
{
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
    /// <see cref="BubbleStyle.Circle"/>. It is called on every render pass (potentially
    /// from the timer thread) so implementations must be safe to call from any thread —
    /// returning an enum field (as all current callers do) satisfies this.
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
        // Each User item starts a new group; Circle items following it are nested
        // children. Plain items render verbatim. Circle items before the first User
        // form an "orphan" group rendered as top-level children. The entire
        // conversation forms one continuous tree — no blank separators between groups.

        // Pre-compute the index of the last top-level tree item (User or orphan Circle
        // that starts a group). This determines which item gets └─ vs ├─ at the
        // outermost level. We scan backwards: the last User wins; if there are no
        // Users, the last orphan Circle wins.
        var lastTopLevelIndex = FindLastTopLevelIndex();

        var i = 0;
        while (i < _children.Count)
        {
            switch (_children[i].Style)
            {
            case BubbleStyle.Plain:
                // Plain items render verbatim — no tree chrome, no grouping.
                rows.AddRange(_children[i].Child.Render(availableWidth));
                i++;
                break;

            case BubbleStyle.User:
            {
                // User group: user as a top-level Circle child (blue ●), followed
                // by zero or more nested Circle children indented one level deeper.
                var userIndex = i;
                var childStart = i + 1;

                // Scan ahead to find the end of this group (next User/Plain or end).
                var childEnd = childStart;
                while (childEnd < _children.Count && _children[childEnd].Style == BubbleStyle.Circle)
                    childEnd++;

                var isLastTopLevel = userIndex == lastTopLevelIndex;
                var hasNestedChildren = childEnd > childStart;
                RenderUserItem(rows, userIndex, isLastTopLevel, hasNestedChildren, availableWidth);

                for (var ci = childStart; ci < childEnd; ci++)
                    RenderNestedChild(rows, ci, isLastNested: ci == childEnd - 1,
                        isLastParent: isLastTopLevel, availableWidth);

                i = childEnd;
                break;
            }

            default:
            {
                // Orphan group: Circle items before the first User. Rendered as
                // top-level tree children with ├─/└─ connectors.
                var childStart = i;
                var childEnd = childStart;
                while (childEnd < _children.Count && _children[childEnd].Style == BubbleStyle.Circle)
                    childEnd++;

                for (var ci = childStart; ci < childEnd; ci++)
                {
                    var isLast = ci == lastTopLevelIndex;
                    RenderChild(rows, ci, isLast, availableWidth);
                }

                i = childEnd;
                break;
            }
            }
        }

        return rows;
    }

    /// <summary>
    /// Scans backwards to find the index of the last top-level tree item. A User
    /// item always counts as top-level. Orphan Circle items (before the first User)
    /// count as top-level only if no User exists after them. Returns -1 if the list
    /// contains only Plain items (no tree items at all).
    /// </summary>
    private int FindLastTopLevelIndex()
    {
        // A User item is always top-level. If any Users exist, the last one is
        // the last top-level item (all Circles after a User are nested, not
        // top-level). If no Users exist, orphan Circles are the only top-level
        // items and the last one wins. Scan backwards for efficiency.
        for (var i = _children.Count - 1; i >= 0; i--)
        {
            if (_children[i].Style == BubbleStyle.User)
                return i;
        }

        // No Users — find the last Circle (orphan).
        for (var i = _children.Count - 1; i >= 0; i--)
        {
            if (_children[i].Style == BubbleStyle.Circle)
                return i;
        }

        // Only Plain items — no tree items at all.
        return -1;
    }

    /// <summary>
    /// Renders a User item as a top-level Circle child with a blue ● glyph.
    /// Content wraps at <c>availableWidth - TreeChrome.ChildChrome</c> (same width as any
    /// other Circle child). Continuation rows use standard child continuation chrome.
    /// </summary>
    private void RenderUserItem(List<CellRow> target, int index, bool isLastTopLevel,
        bool hasNestedChildren, int availableWidth)
    {
        var contentWidth = Math.Max(1, availableWidth - TreeChrome.ChildChrome);
        var childRows = _children[index].Child.Render(contentWidth);

        for (var ri = 0; ri < childRows.Count; ri++)
        {
            if (ri == 0)
            {
                // First row: ├─ ● or └─ ● with a blue circle.
                target.Add(TreeChrome.MakeChildRow(childRows[ri], isLastTopLevel, Color.Blue));
            }
            else
            {
                // Continuation rows need a │ trunk when something follows below:
                // either nested children under this user, or sibling items after
                // it in the top-level tree. Only the last top-level user with no
                // nested children gets blank continuation (5 spaces).
                var showVertical = !isLastTopLevel || hasNestedChildren;
                target.Add(TreeChrome.MakeChildContinuationRow(childRows[ri], isLast: !showVertical));
            }
        }
    }

    /// <summary>
    /// Renders a Circle child item as a top-level tree node: ├─ ● or └─ ● prefix
    /// on the first row, then │ + padding or blank padding on continuation rows.
    /// Used for orphan Circles (before the first User).
    /// </summary>
    private void RenderChild(List<CellRow> target, int index, bool isLast, int availableWidth)
    {
        var (child, _, getCircleColor) = _children[index];
        var circleColor = getCircleColor?.Invoke() ?? Color.White;
        var contentWidth = Math.Max(1, availableWidth - TreeChrome.ChildChrome);
        var childRows = child.Render(contentWidth);

        target.AddRange(childRows.Select((row, ri) => ri == 0
            ? TreeChrome.MakeChildRow(row, isLast, circleColor)
            : TreeChrome.MakeChildContinuationRow(row, isLast)));
    }

    /// <summary>
    /// Renders a Circle item as a nested child (level 2) underneath a User item.
    /// The inner chrome (├─ ● / └─ ●) is produced by <c>TreeChrome.MakeChildRow</c>
    /// and <c>TreeChrome.MakeChildContinuationRow</c>, then wrapped with a 3-column
    /// nesting prefix via <c>TreeChrome.PrependNestPrefix</c>.
    /// Content width = availableWidth - TreeChrome.NestChrome - TreeChrome.ChildChrome.
    /// </summary>
    private void RenderNestedChild(List<CellRow> target, int index, bool isLastNested,
        bool isLastParent, int availableWidth)
    {
        var (child, _, getCircleColor) = _children[index];
        var circleColor = getCircleColor?.Invoke() ?? Color.White;
        var contentWidth = Math.Max(1, availableWidth - TreeChrome.NestChrome - TreeChrome.ChildChrome);
        var childRows = child.Render(contentWidth);

        target.AddRange(childRows.Select((row, ri) =>
        {
            var innerRow = ri == 0
                ? TreeChrome.MakeChildRow(row, isLastNested, circleColor)
                : TreeChrome.MakeChildContinuationRow(row, isLastNested);
            return TreeChrome.PrependNestPrefix(innerRow, isLastParent);
        }));
    }

}
