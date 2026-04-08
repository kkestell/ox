using Te.Rendering;

namespace Ox.Rendering;

/// <summary>
/// Shared tree-drawing constants and chrome helpers used by <see cref="EventList"/>
/// and <see cref="SubagentRenderable"/> to render the conversation as a visual tree.
///
/// Extracted from EventList so that any renderable that needs to produce tree
/// connectors (e.g., SubagentRenderable's ellipsis row) references the same
/// canonical constants rather than hardcoding magic strings.
/// </summary>
internal static class TreeChrome
{
    // ● U+25CF BLACK CIRCLE — status indicator for Circle-style child nodes.
    public const char CircleChar = '●';

    // Tree-drawing box characters used to connect parent and child nodes.
    public const char BranchChar     = '├'; // U+251C — non-last child branch
    private const char LastBranchChar = '└'; // U+2514 — last child branch
    private const char VerticalChar   = '│'; // U+2502 — vertical continuation line
    public const char HorizontalChar = '─'; // U+2500 — horizontal branch connector

    // Chrome widths measured in columns. Content width = availableWidth - chrome.
    // No right-pad or right-margin — tree mode has no background fill.

    // Child chrome: `├─ ● ` or `└─ ● ` — branch + horizontal + space + circle + space.
    public const int ChildChrome = 5;

    // Nesting chrome: `│  ` or `   ` — the 3-column prefix prepended to nested children
    // (level-2 items under a User). The nested child's `├`/`└` aligns directly under
    // the parent User's `●` at column 3.
    public const int NestChrome = 3;

    /// <summary>
    /// First row of a tree child: ├─ ● (non-last) or └─ ● (last child in group).
    /// The branch and horizontal characters are dim (BrightBlack); the circle
    /// color reflects the child's live state (e.g., yellow → green as a tool completes).
    /// </summary>
    public static CellRow MakeChildRow(CellRow childRow, bool isLast, Color circleColor)
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
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Decorations);

        return row;
    }

    /// <summary>
    /// Continuation row of a tree child: │ + 4 spaces (non-last, the vertical trunk
    /// continues for siblings below) or 5 spaces (last child, no trunk needed).
    /// Keeps wrapped text aligned with the child's first-row content column.
    /// </summary>
    public static CellRow MakeChildContinuationRow(CellRow childRow, bool isLast)
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
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Decorations);

        return row;
    }

    /// <summary>
    /// Continuation row for a last-top-level User that has nested children.
    /// Produces `   │ ` (3 spaces + │ + space = 5 columns) so the vertical bar
    /// at column 3 aligns with the nested children's ├/└ connectors, rather than
    /// sitting at column 0 (which would falsely imply more top-level siblings).
    ///
    /// Compare with <see cref="MakeChildContinuationRow"/>: that method places │
    /// at column 0 for non-last top-level items whose trunk continues to siblings.
    /// This method is the last-parent counterpart where the trunk is the nesting
    /// trunk, not the top-level trunk.
    /// </summary>
    public static CellRow MakeLastParentContinuationRow(CellRow childRow)
    {
        var row = new CellRow();

        // 3-space indent (matches PrependNestPrefix for isLastParent = true)
        row.Append("   ", Color.Default, Color.Default);
        // Vertical bar at column 3: signals nested children follow below
        row.Append(VerticalChar, Color.BrightBlack, Color.Default);
        // Single space to align content at column 5 (same as ChildChrome)
        row.Append(' ', Color.Default, Color.Default);

        foreach (var cell in childRow.Cells)
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Decorations);

        return row;
    }

    /// <summary>
    /// Prepends a 3-column nesting prefix to an existing row. Used to indent nested
    /// children (level 2) under their parent User item. Non-last parents get `│  `
    /// (vertical bar + 2 spaces); last parents get `   ` (3 spaces). The vertical
    /// bar aligns with the parent's `├`/`└` at column 0.
    /// </summary>
    public static CellRow PrependNestPrefix(CellRow innerRow, bool isLastParent)
    {
        var row = new CellRow();

        if (isLastParent)
        {
            row.Append("   ", Color.Default, Color.Default);
        }
        else
        {
            row.Append(VerticalChar, Color.BrightBlack, Color.Default);
            row.Append("  ", Color.Default, Color.Default);
        }

        foreach (var cell in innerRow.Cells)
            row.Append(cell.Rune, cell.Foreground, cell.Background, cell.Decorations);

        return row;
    }
}
