namespace Ur.Tui.Rendering;

/// <summary>
/// Visual style applied to a bubble's chrome (background color and left-bar color).
/// The chrome is rendered by <see cref="EventList"/>; child renderables are unaware
/// of it and always receive a clean content area to draw into.
/// </summary>
internal enum BubbleStyle
{
    /// <summary>User messages: dark-gray background, blue bar.</summary>
    User,
    /// <summary>System/tool/error messages: black background, invisible (black) bar.</summary>
    System,
    /// <summary>Assistant response messages: black background, yellow bar.</summary>
    Assistant,
}

/// <summary>
/// The root container for the conversation. Every visible element — assistant
/// messages, user messages, tool calls, subagent blocks — is a child of this list.
///
/// The viewport renders the EventList to get the full set of rows, then displays
/// the tail that fits on screen. Adding a child or mutating any existing child
/// automatically raises <see cref="IRenderable.Changed"/> so the viewport knows to redraw.
///
/// Each child is rendered as a distinct "bubble": a background-colored block
/// framed by a top/bottom padding row and a colored left-bar gutter (▎). The
/// background and bar colors are determined by the child's <see cref="BubbleStyle"/>.
/// Bubbles are separated by a single blank row. Layout per bubble row:
///
///   col 0:    1 space cell            (left margin, bubble bg)
///   col 1:    ▎ glyph                 (bar color fg, bubble bg)
///   col 2:    1 space cell            (inner left pad, bubble bg)
///   col 3…N:  child cells             (child fg, bubble bg if child bg is Default)
///   col N+1:  1 space cell (right pad, bubble bg), then pad to width with bubble bg
///
/// BubbleChrome = 3 (left-margin + bar + inner-left-pad). Right side is padded
/// explicitly so the background fills to the full available width without relying
/// on terminal clear-to-end-of-line behavior.
/// </summary>
internal sealed class EventList : IRenderable
{
    // ▎ U+258E LEFT ONE QUARTER BLOCK — used as the colored left bar in every bubble row.
    private const char BarChar = '▎';

    // Number of columns consumed by the left chrome: left-margin + bar + inner-left-pad.
    // Child renderables receive availableWidth - BubbleChrome - 1 (right-pad) columns.
    private const int BubbleChrome = 3;

    // Each child is stored with its style so Render() can apply per-bubble chrome.
    private readonly List<(IRenderable Child, BubbleStyle Style)> _children = [];

    public event Action? Changed;

    /// <summary>
    /// Appends a child renderable with the given visual style and subscribes to its
    /// <see cref="IRenderable.Changed"/> event so that mutations to any descendant
    /// bubble up to the viewport's redraw trigger.
    /// </summary>
    public void Add(IRenderable child, BubbleStyle style = BubbleStyle.User)
    {
        _children.Add((child, style));
        // Subscribe before invoking Changed so the viewport always sees the new
        // child's future updates. Order matters: add → subscribe → notify.
        child.Changed += () => Changed?.Invoke();
        Changed?.Invoke();
    }

    public IReadOnlyList<CellRow> Render(int availableWidth)
    {
        if (_children.Count == 0)
            return [];

        // Width available for child content: strip left chrome + 1 right-pad cell.
        // Clamp to 1 so degenerate terminal sizes don't crash.
        var contentWidth = Math.Max(1, availableWidth - BubbleChrome - 1);

        var rows  = new List<CellRow>();
        var first = true;

        foreach (var (child, style) in _children)
        {
            // One blank row between consecutive bubbles.
            if (!first)
                rows.Add(CellRow.Empty);
            first = false;

            var (bg, barFg) = StyleColors(style);

            // Top padding row — full-width colored block with bar gutter, no text.
            rows.Add(MakePaddingRow(availableWidth, bg, barFg));

            // Content rows — each child row gets the bar gutter and style background.
            foreach (var childRow in child.Render(contentWidth))
                rows.Add(MakeContentRow(childRow, availableWidth, bg, barFg));

            // Bottom padding row — mirrors the top.
            rows.Add(MakePaddingRow(availableWidth, bg, barFg));
        }

        return rows;
    }

    /// <summary>
    /// Returns the background and bar-foreground <see cref="Color"/> values for a style.
    /// System bubbles use a black bar on black bg, making the bar invisible while
    /// still preserving the same physical layout (the column is occupied but blank).
    /// </summary>
    private static (Color Bg, Color BarFg) StyleColors(BubbleStyle style) => style switch
    {
        BubbleStyle.Assistant => (Color.Black,         Color.Yellow),
        BubbleStyle.System    => (Color.Black,          Color.Black),
        _                     => (Color.FromIndex(236), Color.Blue),   // User
    };

    /// <summary>
    /// Builds a padding row (top or bottom of a bubble): left-margin, bar glyph,
    /// then space cells filled to the full available width — all in the bubble background.
    /// </summary>
    private static CellRow MakePaddingRow(int availableWidth, Color bg, Color barFg)
    {
        var row = new CellRow();
        row.Append(' ',     Color.Default, bg);   // left margin
        row.Append(BarChar, barFg,         bg);   // bar glyph
        row.PadRight(availableWidth, bg);          // fill remainder with bubble bg
        return row;
    }

    /// <summary>
    /// Wraps a child-rendered row in bubble chrome: left-margin, bar glyph, inner-left-pad,
    /// then the child cells (overriding Default backgrounds to the bubble bg so inline
    /// style resets don't punch holes in the bubble fill), then right padding.
    ///
    /// Child cells that carry an explicit non-Default background are left unchanged —
    /// a child could legitimately use a background color for emphasis and we preserve that.
    /// The cell-based approach eliminates the VisibleLength() ANSI-stripping hack that
    /// was required when renderables produced strings with embedded escape sequences.
    /// </summary>
    private static CellRow MakeContentRow(
        CellRow childRow, int availableWidth, Color bg, Color barFg)
    {
        var row = new CellRow();

        // Left chrome.
        row.Append(' ',     Color.Default, bg);   // left margin
        row.Append(BarChar, barFg,         bg);   // bar glyph
        row.Append(' ',     Color.Default, bg);   // inner left pad

        // Child cells: inherit child foreground and style; swap Default background
        // to the bubble background so the bubble fill is seamless even after a child
        // style reset (which previously required re-applying background ANSI codes).
        foreach (var cell in childRow.Cells)
        {
            var cellBg = cell.Background == Color.Default ? bg : cell.Background;
            row.Append(cell.Rune, cell.Foreground, cellBg, cell.Style);
        }

        // Right pad: one explicit space then fill the rest with the bubble bg.
        row.Append(' ', Color.Default, bg);   // inner right pad
        row.PadRight(availableWidth, bg);

        return row;
    }
}
