using Te.Rendering;

namespace Ox.Rendering;

/// <summary>
/// Visual style for an item in the conversation event list.
/// <see cref="User"/> items get a blue ● prefix; <see cref="Circle"/> items
/// get a ● prefix whose color is supplied at add-time; <see cref="Plain"/>
/// items render verbatim with no chrome.
/// </summary>
internal enum BubbleStyle
{
    /// <summary>
    /// User messages: rendered with a blue ● prefix. Visually identical to
    /// Circle items except the circle is always blue.
    /// </summary>
    User,
    /// <summary>
    /// Circle-prefixed items (tool calls, assistant messages, subagent blocks):
    /// a ● glyph whose color is supplied by a <c>Func&lt;Color&gt;</c> passed
    /// to <see cref="EventList.Add"/>.
    /// </summary>
    Circle,
    /// <summary>
    /// Plain text — rows are emitted verbatim at full available width with no
    /// circle prefix. Used for informational messages (e.g., the session banner).
    /// </summary>
    Plain
}

/// <summary>
/// The root container for the conversation. Every visible element — assistant
/// messages, user messages, tool calls, subagent blocks — is a child of this list.
///
/// The conversation is rendered as a flat list. Each User or Circle item gets a
/// <c>● </c> prefix (2 columns: circle + space). Plain items render verbatim.
/// A blank line separates each top-level item for visual breathing room. No tree
/// connectors (├─, └─, │) are used between events — the <c>└─</c> subordination
/// for tool output is handled inside <see cref="ToolRenderable"/> itself.
///
/// Target visual:
///
///   Session: 20260406-185135-171
///
///   ● User message
///     Continuation wraps here.
///
///   ● Bash("dotnet test")
///     └─ Exit code: 0
///
///   ● Assistant response text
///     wraps like this.
///
/// The viewport renders the EventList to get the full set of rows, then displays
/// the tail that fits on screen. Adding a child or mutating any existing child
/// automatically raises <see cref="IRenderable.Changed"/> so the viewport redraws.
/// </summary>
internal sealed class EventList : IRenderable
{
    // Each child is stored with its style and an optional circle-color supplier.
    // The Func<Color>? is only consulted for Circle entries; for User items the
    // circle is always blue. Storing a Func rather than a Color snapshot allows
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
    /// <see cref="BubbleStyle.Circle"/>. It is called on every render pass so
    /// implementations must be thread-safe — returning an enum field satisfies this.
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
        var emittedItem = false; // tracks whether we need a blank separator

        for (var i = 0; i < _children.Count; i++)
        {
            var (child, style, getCircleColor) = _children[i];

            if (style == BubbleStyle.Plain)
            {
                // Plain items render verbatim — no circle prefix, no separator.
                rows.AddRange(child.Render(availableWidth));
                continue;
            }

            // Insert a blank line between top-level items for visual separation.
            if (emittedItem)
                rows.Add(new CellRow());

            var circleColor = style == BubbleStyle.User
                ? Color.Blue
                : getCircleColor?.Invoke() ?? Color.White;

            // Content width accounts for the 2-column "● " prefix.
            var contentWidth = Math.Max(1, availableWidth - TreeChrome.CircleChrome);
            var childRows = child.Render(contentWidth);

            for (var ri = 0; ri < childRows.Count; ri++)
            {
                rows.Add(ri == 0
                    ? TreeChrome.MakeCircleRow(childRows[ri], circleColor)
                    : TreeChrome.MakeContinuationRow(childRows[ri]));
            }

            emittedItem = true;
        }

        return rows;
    }
}
