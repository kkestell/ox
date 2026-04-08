namespace Ox.Rendering;

/// <summary>
/// Generic right sidebar that renders a vertical stack of <see cref="ISidebarSection"/>
/// instances. Hidden (zero width) when no section has content. Raises
/// <see cref="Changed"/> when any child section changes so the viewport redraws.
///
/// The sidebar is designed to host multiple sections — the todo list is the first
/// concrete implementation. Future sections (e.g., session info, file watches) plug
/// in via <see cref="AddSection"/> without changing this infrastructure.
/// </summary>
internal sealed class Sidebar : IRenderable
{
    private readonly List<ISidebarSection> _sections = [];

    public event Action? Changed;

    /// <summary>
    /// Always true — the sidebar is permanently visible so the layout remains
    /// stable even when no section has content yet.
    /// </summary>
    public bool IsVisible => true;

    /// <summary>
    /// Registers a section and subscribes to its <see cref="IRenderable.Changed"/>
    /// event so changes propagate to the viewport.
    /// </summary>
    public void AddSection(ISidebarSection section)
    {
        _sections.Add(section);
        section.Changed += () => Changed?.Invoke();
    }

    /// <summary>
    /// Renders all visible sections top-to-bottom into a single list of rows.
    /// Sections where <see cref="ISidebarSection.HasContent"/> is false are skipped.
    /// </summary>
    public IReadOnlyList<CellRow> Render(int availableWidth)
    {
        var rows = new List<CellRow>();

        foreach (var section in _sections)
        {
            if (!section.HasContent)
                continue;

            rows.AddRange(section.Render(availableWidth));
        }

        return rows;
    }
}
