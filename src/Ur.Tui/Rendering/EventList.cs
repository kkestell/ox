namespace Ur.Tui.Rendering;

/// <summary>
/// The root container for the conversation. Every visible element — assistant
/// messages, user messages, tool calls, subagent blocks — is a child of this list.
///
/// The viewport renders the EventList to get the full set of lines, then displays
/// the tail that fits on screen. Adding a child or mutating any existing child
/// automatically raises <see cref="Changed"/> so the viewport knows to redraw.
///
/// EventList itself has no styling — it is purely an ordered aggregation of its
/// children's rendered output.
/// </summary>
internal sealed class EventList : IRenderable
{
    private readonly List<IRenderable> _children = [];

    public event Action? Changed;

    /// <summary>
    /// Appends a child renderable and subscribes to its <see cref="IRenderable.Changed"/>
    /// event so that mutations to any descendant bubble up to the viewport's redraw trigger.
    /// </summary>
    public void Add(IRenderable child)
    {
        _children.Add(child);
        // Subscribe before invoking Changed so the viewport always sees the new
        // child's future updates. Order matters: add → subscribe → notify.
        child.Changed += () => Changed?.Invoke();
        Changed?.Invoke();
    }

    public IReadOnlyList<string> Render(int availableWidth)
    {
        if (_children.Count == 0)
            return [];

        var lines = new List<string>();
        foreach (var child in _children)
        {
            lines.AddRange(child.Render(availableWidth));
        }
        return lines;
    }
}
