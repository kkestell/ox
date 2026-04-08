namespace Ox.Rendering;

/// <summary>
/// A section within the right sidebar. Each section renders independently and
/// reports whether it currently has content to display. The sidebar hides
/// itself (zero width) when all sections report <see cref="HasContent"/> = false.
///
/// Sections implement <see cref="IRenderable"/> for their visual output and
/// raise <see cref="IRenderable.Changed"/> when their content changes so the
/// sidebar can propagate the notification to the viewport.
/// </summary>
internal interface ISidebarSection : IRenderable
{
    /// <summary>
    /// True when this section has content worth displaying. The sidebar
    /// skips sections where this is false during rendering.
    /// </summary>
    bool HasContent { get; }
}
