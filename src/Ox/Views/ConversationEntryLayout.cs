namespace Ox.Views;

/// <summary>
/// Pure layout helpers for a single conversation entry.
///
/// The view owns chrome, scrolling, and drawing, but the transformation from
/// styled text segments into wrapped render lines is a separate concern. Keeping
/// that logic here lets tests lock down screen-dump shaping without loading the
/// full Terminal.Gui view stack.
/// </summary>
internal static class ConversationEntryLayout
{
    /// <summary>
    /// Wraps the entry's styled segments into render lines that still preserve
    /// span-level styling.
    /// </summary>
    public static List<RenderedLine> LayoutSegments(IReadOnlyList<StyledSegment> segments, int width)
    {
        var laidOutLines = ConversationTextLayout.LayoutSegments(
            segments.Select(static segment => new LayoutFragment<SegmentStyle>(
                segment.Text,
                new SegmentStyle(segment.Foreground, segment.Background, segment.Bold)))
                .ToList(),
            width);

        return laidOutLines
            .Select(static line => new RenderedLine(
                line.Select(static fragment => new RenderSpan(
                    fragment.Text,
                    fragment.Style.Foreground,
                    fragment.Style.Background,
                    fragment.Style.Bold))
                    .ToList()))
            .ToList();
    }

    private readonly record struct SegmentStyle(
        Terminal.Gui.Drawing.Color Foreground,
        Terminal.Gui.Drawing.Color Background,
        bool Bold);
}
