using Te.Rendering;

namespace Ox.Rendering;

/// <summary>
/// Sidebar section that displays the current context window usage as a
/// percentage bar and text label (e.g. "125,000 / 250,000 - 50%").
/// Sits at the top of the sidebar so the user always has a sense of how
/// much context remains.
///
/// Hidden until the first turn completes and token counts are available.
/// This prevents an empty placeholder from causing the sidebar to appear
/// before the user has interacted with the session.
/// </summary>
internal sealed class ContextSection : ISidebarSection
{
    private string? _usageText;

    /// <summary>
    /// Only visible once usage data has been received from a completed turn.
    /// </summary>
    public bool HasContent => _usageText is not null;

    public event Action? Changed;

    /// <summary>
    /// Updates the pre-formatted usage string (e.g. "125,000 / 250,000 - 50%")
    /// and notifies the sidebar to trigger a viewport redraw.
    /// </summary>
    public void SetUsage(string? text)
    {
        _usageText = text;
        Changed?.Invoke();
    }

    public IReadOnlyList<CellRow> Render(int availableWidth)
    {
        var rows = new List<CellRow>();

        // Header: bold bright white to stand out as a section title.
        rows.Add(CellRow.FromText("Context", Color.White, Color.Default, TextDecoration.Bold));

        // Usage text directly below the header, or a placeholder dash before
        // the first turn reports token counts.
        var displayText = _usageText ?? "—";
        rows.Add(CellRow.FromText(displayText, Color.BrightBlack, Color.Default));

        // Trailing blank line to visually separate from the next section.
        rows.Add(CellRow.Empty);

        return rows;
    }
}
