using System.Drawing;
using Terminal.Gui.Drawing;
using Terminal.Gui.ViewBase;
using Attribute = Terminal.Gui.Drawing.Attribute;
using Color = Terminal.Gui.Drawing.Color;

namespace Ox.Views;

/// <summary>
/// A Terminal.Gui View that draws a single <see cref="ConversationEntry"/>.
///
/// Each entry in the conversation stream gets its own ConversationEntryView.
/// The parent <see cref="ConversationView"/> stacks these vertically with
/// Pos.Bottom chains and lets Terminal.Gui handle scrolling and clipping.
///
/// Drawing logic: circle prefix for User/Circle styles, continuation indent
/// for wrapped lines, and styled spans via the text layout engine. Children
/// (subagent nested entries) become nested ConversationEntryView SubViews
/// indented under the parent.
///
/// Height is controlled via Dim.Auto(DimAutoStyle.Content): for entries without
/// child SubViews (the common case) we call SetContentSize so DimAuto reads
/// our row count directly. For subagent entries that DO have child SubViews,
/// DimAuto sums up the child frames — no manual height bookkeeping needed.
/// </summary>
internal sealed class ConversationEntryView : View
{
    // ● U+25CF BLACK CIRCLE — status indicator prefix.
    private static readonly Color Bg = new(ColorName16.Black);
    private const char CircleChar = '●';

    // Chrome width: "● " = 2 columns (circle + space).
    internal const int CircleChrome = 2;

    private readonly ConversationEntry _entry;

    // Tracks child entries already materialized as SubViews so we don't
    // duplicate them when the entry's Children list grows (streaming).
    private readonly List<ConversationEntryView> _childViews = [];

    // Whether this view is nested inside another ConversationEntryView
    // (subagent children). Nested views get indentation from their parent
    // and skip the horizontal gutter.
    private readonly bool _isChild;

    /// <summary>
    /// The visual style of this entry's data model. Used by the parent
    /// <see cref="ConversationView"/> to determine inter-entry spacing.
    /// </summary>
    public EntryStyle Style => _entry.Style;

    public ConversationEntryView(ConversationEntry entry, bool isChild = false)
    {
        _entry = entry;
        _isChild = isChild;
        CanFocus = false;

        // Subscribe to the data model's change notification so we relayout
        // and redraw when streaming tokens arrive or tool state changes.
        _entry.Changed += OnEntryChanged;
    }

    /// <summary>
    /// Computes the number of rows this entry needs and reports it via
    /// SetContentSize so that Dim.Auto(DimAutoStyle.Content) on our Height
    /// picks it up in the same layout pass — no two-pass lag.
    ///
    /// For entries with child SubViews (subagent blocks) we skip this: DimAuto
    /// computes height from the child frames directly, which is more accurate.
    /// </summary>
    private void UpdateContentSize()
    {
        // Without a positive width the row calculation would be meaningless;
        // the viewport callback will fire again once layout assigns a real width.
        int width = Viewport.Width;
        if (width <= 0)
            return;

        // Subagent entries have child SubViews — let Dim.Auto sum their frames.
        if (_childViews.Count > 0)
            return;

        int contentWidth = GetContentWidth(width);
        int rows = GetOwnRenderedLines(contentWidth).Count;
        SetContentSize(new Size(width, rows));
    }

    /// <summary>
    /// After layout assigns this view its Frame, recompute the content size
    /// from the actual width. This handles the initial sizing and any resize.
    /// </summary>
    protected override void OnViewportChanged(DrawEventArgs args)
    {
        base.OnViewportChanged(args);
        UpdateContentSize();
        SyncChildViews();
    }

    /// <inheritdoc/>
    protected override bool OnDrawingContent(DrawContext? context)
    {
        var width = Viewport.Width;
        var height = Viewport.Height;
        if (width <= 0 || height <= 0)
            return true;

        // Children are drawn by Terminal.Gui as SubViews — we only draw
        // this entry's own text lines here.
        var contentWidth = GetContentWidth(width);
        var lines = GetOwnRenderedLines(contentWidth);

        for (var row = 0; row < Math.Min(lines.Count, height); row++)
        {
            DrawRenderedLine(row, lines[row], width);
        }

        return true;
    }

    private void OnEntryChanged()
    {
        SyncChildViews();
        UpdateContentSize();
        SetNeedsDraw();
    }

    /// <summary>
    /// Returns the content width available for text, accounting for the
    /// horizontal gutter (top-level entries only) and circle chrome.
    /// </summary>
    private int GetContentWidth(int viewportWidth)
    {
        if (_isChild)
        {
            // Child views are already indented by the parent. Their full
            // width is available for content minus circle chrome.
            return _entry.Style == EntryStyle.Plain
                ? Math.Max(1, viewportWidth)
                : Math.Max(1, viewportWidth - CircleChrome);
        }

        // Top-level entries have horizontal padding on both sides.
        var paddedWidth = ConversationViewportBehavior.GetContentWidth(viewportWidth);
        return _entry.Style == EntryStyle.Plain
            ? Math.Max(1, paddedWidth)
            : Math.Max(1, paddedWidth - CircleChrome);
    }

    /// <summary>
    /// Gets the rendered lines for this entry's own segments (not children).
    /// Applies circle chrome for User/Circle styles.
    /// </summary>
    private List<RenderedLine> GetOwnRenderedLines(int contentWidth)
    {
        if (_entry.Style == EntryStyle.Plain)
            return LayoutPlainLines(contentWidth);

        return LayoutChromeLines(contentWidth);
    }

    /// <summary>
    /// Renders a Plain-style entry at full width with no prefix.
    /// </summary>
    private List<RenderedLine> LayoutPlainLines(int contentWidth)
    {
        var lines = new List<RenderedLine>();
        foreach (var segment in _entry.Segments)
        {
            var wrapped = TextLayout.WrapText(segment.Text, contentWidth);
            foreach (var line in wrapped)
                lines.Add(new RenderedLine([new RenderSpan(line, segment.Foreground, segment.Background, segment.Bold)]));
        }
        return lines;
    }

    /// <summary>
    /// Renders a User or Circle entry with the ● prefix and continuation indent.
    /// </summary>
    private List<RenderedLine> LayoutChromeLines(int contentWidth)
    {
        var circleColor = _entry.Style == EntryStyle.User
            ? new Color(ColorName16.Blue)
            : _entry.GetCircleColor?.Invoke() ?? new Color(ColorName16.White);

        var entryLines = ConversationEntryLayout.LayoutSegments(_entry.Segments, contentWidth);
        var lines = new List<RenderedLine>();

        for (var i = 0; i < entryLines.Count; i++)
        {
            var prefixSpans = i == 0
                ? MakeCirclePrefix(circleColor)
                : MakeContinuationPrefix();

            var combined = new List<RenderSpan>(prefixSpans);
            combined.AddRange(entryLines[i].Spans);
            lines.Add(new RenderedLine(combined));
        }

        return lines;
    }

    /// <summary>
    /// Renders a single pre-computed line at the given row in the viewport.
    /// </summary>
    private void DrawRenderedLine(int row, RenderedLine line, int width)
    {
        // Clear the row.
        Move(0, row);
        SetAttribute(new Attribute(Color.None, Bg));
        for (var col = 0; col < width; col++)
            AddRune(' ');

        // Top-level entries get a horizontal gutter; children don't (they're
        // already indented by their parent's layout).
        var leftInset = _isChild
            ? 0
            : (width > ConversationViewportBehavior.HorizontalPaddingColumns
                ? ConversationViewportBehavior.HorizontalPaddingColumns
                : 0);

        var col2 = leftInset;
        var contentEnd = width;
        foreach (var span in line.Spans)
        {
            if (col2 >= contentEnd) break;
            Move(col2, row);
            SetAttribute(new Attribute(span.Foreground, span.Background, span.Bold ? TextStyle.Bold : TextStyle.None));
            var text = span.Text;
            if (col2 + text.Length > contentEnd)
                text = text[..(contentEnd - col2)];
            AddStr(text);
            col2 += text.Length;
        }
    }

    /// <summary>
    /// Ensures each child in the data model has a corresponding SubView.
    /// New children (from streaming subagent events) are appended and
    /// positioned after the last existing child view.
    /// </summary>
    private void SyncChildViews()
    {
        if (_entry.Children.Count <= _childViews.Count)
            return;

        // The own-text rows determine where the first child starts.
        var ownContentWidth = GetContentWidth(Frame.Width > 0 ? Frame.Width : 80);
        var ownRows = GetOwnRenderedLines(ownContentWidth).Count;

        for (var i = _childViews.Count; i < _entry.Children.Count; i++)
        {
            var childEntry = _entry.Children[i];
            var childView = new ConversationEntryView(childEntry, isChild: true);

            // Children are indented by CircleChrome columns under the parent.
            childView.X = CircleChrome;
            childView.Width = Dim.Fill(Dim.Absolute(CircleChrome));

            if (_childViews.Count == 0)
            {
                // First child starts right after the parent's own text lines.
                childView.Y = Pos.Absolute(ownRows);
            }
            else
            {
                childView.Y = Pos.Bottom(_childViews[^1]);
            }

            // Dim.Auto(Content): when the child has no SubViews of its own, DimAuto
            // reads the child's content size (set by UpdateContentSize), so height
            // adjusts correctly as streaming text accumulates.
            childView.Height = Dim.Auto(DimAutoStyle.Content);

            _childViews.Add(childView);
            Add(childView);
        }
    }

    /// <summary>Creates the "● " prefix spans with the given circle color.</summary>
    private static List<RenderSpan> MakeCirclePrefix(Color circleColor) =>
    [
        new(CircleChar.ToString(), circleColor, Bg),
        new(" ", Bg, Bg)
    ];

    /// <summary>Creates the "  " continuation prefix (same width as circle chrome).</summary>
    private static List<RenderSpan> MakeContinuationPrefix() =>
    [
        new("  ", Bg, Bg)
    ];
}
