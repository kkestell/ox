using System.Drawing;
using Terminal.Gui.App;
using Terminal.Gui.Drawing;
using Terminal.Gui.ViewBase;
using Attribute = Terminal.Gui.Drawing.Attribute;
using Color = Terminal.Gui.Drawing.Color;

namespace Ox.Views;

/// <summary>
/// Custom View that renders the entire conversation stream. All messages, tool calls,
/// and subagent blocks are drawn in a single <see cref="OnDrawingContent"/> override.
///
/// This is Option A from the migration plan: one View managing its own content layout
/// and drawing, with Terminal.Gui handling position/size within the window and providing
/// scrollbar infrastructure via SetContentSize/Viewport.
///
/// The conversation is a flat list of <see cref="ConversationEntry"/> data models.
/// Each entry gets a circle prefix (● for User/Circle style) or renders verbatim (Plain).
/// Word-wrapping, tree chrome, and auto-scroll are computed here.
/// </summary>
internal sealed class ConversationView : View
{
    // ● U+25CF BLACK CIRCLE — status indicator prefix.
    private static readonly Color Bg = new(ColorName16.Black);
    private const char CircleChar = '●';

    // Chrome width: "● " = 2 columns (circle + space).
    private const int CircleChrome = 2;

    // ASCII art displayed centered when the conversation is empty.
    private static readonly string[] SplashLines =
    [
        "▒█▀▀▀█ ▀▄▒▄▀",
        "▒█░░▒█ ░▒█░░",
        "▒█▄▄▄█ ▄▀▒▀▄"
    ];

    private readonly List<ConversationEntry> _entries = [];

    // Pre-rendered lines cache. Invalidated when entries change or width changes.
    private List<RenderedLine>? _cachedLines;
    private int _cachedWidth = -1;

    private readonly IApplication _app;

    /// <summary>Raised when content changes (entries added/mutated).</summary>
    public event Action? ContentChanged;

    public ConversationView(IApplication app)
    {
        _app = app;
        CanFocus = false;
    }

    /// <summary>
    /// Adds a top-level entry to the conversation and wires its Changed event.
    /// </summary>
    public void AddEntry(ConversationEntry entry)
    {
        _entries.Add(entry);
        entry.Changed += OnEntryChanged;
        InvalidateCache();
    }

    /// <summary>
    /// Returns the number of top-level entries. Used to detect empty state for splash.
    /// </summary>
    public int EntryCount => _entries.Count;

    private void OnEntryChanged()
    {
        InvalidateCache();
    }

    private void InvalidateCache()
    {
        _cachedLines = null;
        // Auto-scroll: tell Terminal.Gui the content size changed, then move viewport to bottom.
        _app.Invoke(() =>
        {
            UpdateContentSize();
            SetNeedsDraw();
            ContentChanged?.Invoke();
        });
    }

    /// <summary>
    /// Recalculates the total content height and updates Terminal.Gui's content size
    /// so scrolling works correctly. Also scrolls to the bottom.
    /// </summary>
    private void UpdateContentSize()
    {
        var width = Frame.Width;
        if (width <= 0) return;

        var lines = GetRenderedLines(width);
        var totalHeight = lines.Count > 0 ? lines.Count : Frame.Height;
        SetContentSize(new Size(width, totalHeight));

        // Auto-scroll to bottom: set viewport to show the last screenful.
        var viewportY = Math.Max(0, totalHeight - Frame.Height);
        Viewport = Viewport with { Y = viewportY };
    }

    /// <inheritdoc/>
    protected override bool OnDrawingContent(DrawContext? context)
    {
        var width = Viewport.Width;
        var height = Viewport.Height;
        if (width <= 0 || height <= 0)
            return true;

        // If no entries, draw the splash art centered.
        if (_entries.Count == 0)
        {
            DrawSplash(width, height);
            return true;
        }

        var lines = GetRenderedLines(Frame.Width);

        // Terminal.Gui handles viewport offset — we draw relative to Viewport.Y.
        var startLine = Viewport.Y;
        for (var row = 0; row < height; row++)
        {
            var lineIndex = startLine + row;
            if (lineIndex >= 0 && lineIndex < lines.Count)
            {
                DrawRenderedLine(row, lines[lineIndex], width);
            }
        }

        return true;
    }

    /// <summary>
    /// Draws the splash art centered in the viewport when no entries exist.
    /// </summary>
    private void DrawSplash(int width, int height)
    {
        var artWidth = SplashLines.Max(l => l.Length);
        var startRow = Math.Max(0, (height - SplashLines.Length) / 2);
        var startCol = Math.Max(0, (width - artWidth) / 2);

        for (var i = 0; i < SplashLines.Length; i++)
        {
            Move(startCol, startRow + i);
            SetAttribute(new Attribute(new Color(ColorName16.DarkGray), Bg));
            AddStr(SplashLines[i]);
        }
    }

    /// <summary>
    /// Renders a single pre-computed line at the given row in the viewport.
    /// </summary>
    private void DrawRenderedLine(int row, RenderedLine line, int width)
    {
        // Clear the row first by writing spaces
        Move(0, row);
        SetAttribute(new Attribute(Color.None, Bg));
        for (var col = 0; col < width; col++)
            AddRune(' ');

        // Now draw the actual content
        var col2 = 0;
        foreach (var span in line.Spans)
        {
            if (col2 >= width) break;
            Move(col2, row);
            SetAttribute(new Attribute(span.Foreground, span.Background, span.Bold ? TextStyle.Bold : TextStyle.None));
            var text = span.Text;
            if (col2 + text.Length > width)
                text = text[..(width - col2)];
            AddStr(text);
            col2 += text.Length;
        }
    }

    /// <summary>
    /// Gets or rebuilds the cached list of rendered lines for the given width.
    /// Each ConversationEntry is laid out with word-wrapping and circle chrome.
    /// </summary>
    private List<RenderedLine> GetRenderedLines(int width)
    {
        if (_cachedLines is not null && _cachedWidth == width)
            return _cachedLines;

        _cachedLines = [];
        _cachedWidth = width;
        var emittedItem = false;

        foreach (var entry in _entries)
        {
            if (entry.Style == EntryStyle.Plain)
            {
                RenderEntryPlain(entry, width, _cachedLines);
                continue;
            }

            // Blank line between top-level items for visual separation.
            if (emittedItem)
                _cachedLines.Add(RenderedLine.Empty);

            RenderEntryWithChrome(entry, width, _cachedLines);
            emittedItem = true;
        }

        return _cachedLines;
    }

    /// <summary>
    /// Renders a Plain-style entry at full width with no prefix.
    /// </summary>
    private static void RenderEntryPlain(ConversationEntry entry, int width, List<RenderedLine> lines)
    {
        foreach (var segment in entry.Segments)
        {
            var wrapped = TextLayout.WrapText(segment.Text, width);
            foreach (var line in wrapped)
                lines.Add(new RenderedLine([new RenderSpan(line, segment.Foreground, segment.Background, segment.Bold)]));
        }
    }

    /// <summary>
    /// Renders a User or Circle entry with the ● prefix and continuation indent.
    /// Also handles nested children (subagent blocks).
    /// </summary>
    private static void RenderEntryWithChrome(ConversationEntry entry, int totalWidth, List<RenderedLine> lines)
    {
        var circleColor = entry.Style == EntryStyle.User
            ? new Color(ColorName16.Blue)
            : entry.GetCircleColor?.Invoke() ?? new Color(ColorName16.White);

        var contentWidth = Math.Max(1, totalWidth - CircleChrome);

        // Render the entry's own segments with word-wrapping.
        var entryLines = new List<RenderedLine>();
        foreach (var segment in entry.Segments)
        {
            var wrapped = TextLayout.WrapText(segment.Text, contentWidth);
            foreach (var line in wrapped)
                entryLines.Add(new RenderedLine([new RenderSpan(line, segment.Foreground, segment.Background, segment.Bold)]));
        }

        // Apply circle/continuation chrome to the wrapped lines.
        for (var i = 0; i < entryLines.Count; i++)
        {
            var prefixSpans = i == 0
                ? MakeCirclePrefix(circleColor)
                : MakeContinuationPrefix();

            var combined = new List<RenderSpan>(prefixSpans);
            combined.AddRange(entryLines[i].Spans);
            lines.Add(new RenderedLine(combined));
        }

        // Render children (subagent inner events) with their own circle chrome.
        if (entry.Children.Count > 0)
        {
            var childLines = new List<RenderedLine>();
            var childEmitted = false;

            foreach (var child in entry.Children)
            {
                if (child.Style == EntryStyle.Plain)
                {
                    RenderEntryPlain(child, contentWidth, childLines);
                    continue;
                }

                if (childEmitted)
                    childLines.Add(RenderedLine.Empty);

                RenderEntryWithChrome(child, contentWidth, childLines);
                childEmitted = true;
            }

            // Tail-clip to MaxChildRows if needed.
            var maxRows = entry.MaxChildRows;
            if (childLines.Count > maxRows)
            {
                var startIndex = childLines.Count - maxRows;
                // Prepend ellipsis row.
                var ellipsis = new RenderedLine([
                    new RenderSpan("  ", Bg, Bg),
                    new RenderSpan($"{CircleChar} ...", new Color(ColorName16.DarkGray), Bg)
                ]);
                lines.Add(ellipsis);

                for (var i = startIndex; i < childLines.Count; i++)
                {
                    // Indent child lines under the parent's continuation.
                    var indented = IndentLine(childLines[i]);
                    lines.Add(indented);
                }
            }
            else
            {
                foreach (var childLine in childLines)
                    lines.Add(IndentLine(childLine));
            }
        }
    }

    /// <summary>
    /// Indents a line by the circle chrome width (2 spaces) for subagent children.
    /// </summary>
    private static RenderedLine IndentLine(RenderedLine line)
    {
        var spans = new List<RenderSpan> { new("  ", Bg, Bg) };
        spans.AddRange(line.Spans);
        return new RenderedLine(spans);
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

/// <summary>
/// A single pre-rendered line of the conversation, composed of styled spans.
/// </summary>
internal sealed class RenderedLine
{
    public static RenderedLine Empty { get; } = new([]);
    public List<RenderSpan> Spans { get; }

    public RenderedLine(List<RenderSpan> spans) => Spans = spans;
}

/// <summary>
/// A contiguous run of text with uniform styling within a rendered line.
/// </summary>
internal readonly record struct RenderSpan(string Text, Color Foreground, Color Background, bool Bold = false);
