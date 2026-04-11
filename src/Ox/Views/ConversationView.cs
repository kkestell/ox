using Ox.Conversation;
using Te.Rendering;

namespace Ox.Views;

/// <summary>
/// Renders the conversation area: either the splash logo (when empty) or
/// a scrollable list of conversation entries with word wrapping, circle
/// prefixes, and a vertical scrollbar.
///
/// All entry styling follows §4 and §11 of the functional requirements:
/// user messages get blue circles, assistant text gets white circles, tool
/// calls get lifecycle-colored circles, errors get red circles, and
/// cancellation entries are plain text.
/// </summary>
public sealed class ConversationView
{
    // The Ox splash logo — centered when the conversation is empty.
    private static readonly string[] SplashLines =
    [
        "▒█▀▀▀█ ▀▄▒▄▀",
        "▒█░░▒█ ░▒█░░",
        "▒█▄▄▄█ ▄▀▒▀▄",
    ];

    private readonly OxThemePalette _theme = OxThemePalette.Ox;
    private readonly List<ConversationEntry> _entries = [];

    // Scroll state: offset from the top of content. 0 = showing the start.
    private int _scrollOffset;
    private bool _autoScroll = true;

    // Cached from the last Render pass so scroll handlers can clamp correctly.
    private int _lastContentHeight;
    private int _lastViewportHeight;

    /// <summary>Read-only access to the entry list.</summary>
    public IReadOnlyList<ConversationEntry> Entries => _entries;

    /// <summary>Add an entry to the conversation.</summary>
    public void AddEntry(ConversationEntry entry) => _entries.Add(entry);

    /// <summary>Find a tool call entry by its call ID, or null if not found.</summary>
    public ToolCallEntry? FindToolCall(string callId) =>
        _entries.OfType<ToolCallEntry>().FirstOrDefault(e => e.CallId == callId);

    /// <summary>Find a subagent container entry by its call ID, or null if not found.</summary>
    public SubagentContainerEntry? FindSubagentContainer(string callId) =>
        _entries.OfType<SubagentContainerEntry>().FirstOrDefault(e => e.CallId == callId);

    /// <summary>
    /// Scroll up by the specified number of rows. Disables auto-scroll.
    /// </summary>
    public void ScrollUp(int rows)
    {
        _scrollOffset = Math.Max(0, _scrollOffset - rows);
        _autoScroll = false;
    }

    /// <summary>
    /// Scroll down by the specified number of rows. Uses the content and
    /// viewport dimensions cached from the last render pass to clamp correctly.
    /// Re-engages auto-scroll if the viewport reaches the bottom.
    /// </summary>
    public void ScrollDown(int rows)
    {
        _scrollOffset = Math.Min(
            Math.Max(0, _lastContentHeight - _lastViewportHeight),
            _scrollOffset + rows);

        if (ConversationViewportBehavior.IsPinnedToBottom(_scrollOffset, _lastContentHeight, _lastViewportHeight))
            _autoScroll = true;
    }

    /// <summary>
    /// Render the conversation area into the buffer at the given region.
    /// </summary>
    /// <param name="buffer">Target rendering buffer.</param>
    /// <param name="x">Left column of the region.</param>
    /// <param name="y">Top row of the region.</param>
    /// <param name="width">Width of the region in columns.</param>
    /// <param name="height">Height of the region in rows.</param>
    public void Render(ConsoleBuffer buffer, int x, int y, int width, int height)
    {
        if (_entries.Count == 0)
        {
            RenderSplash(buffer, x, y, width, height);
            return;
        }

        // Lay out all entries into a flat list of rendered rows.
        var contentWidth = ConversationViewportBehavior.GetContentWidth(width);
        var rows = LayoutAllEntries(contentWidth);
        var contentHeight = rows.Count;

        // Cache dimensions for scroll handlers outside the render pass.
        _lastContentHeight = contentHeight;
        _lastViewportHeight = height;

        // Auto-scroll: pin to the bottom when new content arrives.
        if (_autoScroll)
            _scrollOffset = Math.Max(0, contentHeight - height);

        // Render visible rows.
        for (var row = 0; row < height; row++)
        {
            var contentRow = _scrollOffset + row;
            if (contentRow < 0 || contentRow >= contentHeight) continue;

            var renderedRow = rows[contentRow];
            var drawX = x + ConversationViewportBehavior.HorizontalPaddingColumns;

            // Draw indentation (for sub-agent entries).
            drawX += renderedRow.Indent;

            // Draw circle prefix if present.
            if (renderedRow.CircleColor is { } circleColor)
            {
                buffer.SetCell(drawX, y + row, '●', circleColor, Color.Default);
                drawX += ConversationEntryView.CircleChrome;
            }
            else if (renderedRow.IsCircleEntry)
            {
                // Continuation line of a circle entry — indent past the chrome.
                drawX += ConversationEntryView.CircleChrome;
            }

            // Draw text fragments.
            foreach (var fragment in renderedRow.Fragments)
            {
                for (var i = 0; i < fragment.Text.Length; i++)
                {
                    if (drawX < x + width - 1) // Reserve right column for scrollbar.
                        buffer.SetCell(drawX, y + row, fragment.Text[i], fragment.Color, Color.Default);
                    drawX++;
                }
            }
        }

        // Render scrollbar on the right edge.
        if (contentHeight > height)
            RenderScrollbar(buffer, x + width - 1, y, height, contentHeight);
    }

    /// <summary>Render the splash logo centered in the region.</summary>
    private void RenderSplash(ConsoleBuffer buffer, int x, int y, int width, int height)
    {
        var logoWidth = SplashLines.Max(l => l.Length);
        var logoHeight = SplashLines.Length;
        var startX = x + Math.Max(0, (width - logoWidth) / 2);
        var startY = y + Math.Max(0, (height - logoHeight) / 2);

        for (var row = 0; row < logoHeight; row++)
        {
            var line = SplashLines[row];
            for (var col = 0; col < line.Length; col++)
            {
                if (startX + col < x + width && startY + row < y + height)
                    buffer.SetCell(startX + col, startY + row, line[col], _theme.SplashLogo, Color.Default);
            }
        }
    }

    /// <summary>
    /// Lay out all entries into a flat list of rendered rows, applying
    /// wrapping, spacing, circle prefixes, and indentation.
    /// </summary>
    private List<RenderedRow> LayoutAllEntries(int contentWidth)
    {
        var rows = new List<RenderedRow>();
        var hasEmittedNonPlain = false;

        foreach (var entry in _entries)
            LayoutEntry(entry, contentWidth, 0, rows, ref hasEmittedNonPlain);

        return rows;
    }

    /// <summary>
    /// Lay out a single entry (possibly with children for subagent containers).
    /// </summary>
    private void LayoutEntry(
        ConversationEntry entry,
        int contentWidth,
        int indent,
        List<RenderedRow> rows,
        ref bool hasEmittedNonPlain)
    {
        // Skip assistant entries that have no visible text (empty streaming chunks).
        if (entry is AssistantTextEntry { Text.Length: 0 })
            return;

        var style = GetEntryStyle(entry);
        var circleColor = GetCircleColor(entry);

        // Inter-entry spacing.
        if (ConversationViewportBehavior.NeedsSpacingBefore(style, hasEmittedNonPlain))
            rows.Add(RenderedRow.Blank);

        if (style != EntryStyle.Plain)
            hasEmittedNonPlain = true;

        // Compute text width: subtract circle chrome for non-plain entries.
        var textWidth = style == EntryStyle.Plain
            ? contentWidth - indent
            : contentWidth - indent - ConversationEntryView.CircleChrome;

        // Build segments and lay them out.
        var segments = BuildSegments(entry);
        var lines = ConversationTextLayout.LayoutSegments(segments, textWidth);
        var isCircleEntry = style != EntryStyle.Plain;

        for (var i = 0; i < lines.Count; i++)
        {
            // Carry the laid-out text into the ColorFragment for rendering.
            var fragments = lines[i].Select(f => f.Style with { Text = f.Text }).ToList();
            rows.Add(new RenderedRow
            {
                CircleColor = i == 0 ? circleColor : null,
                IsCircleEntry = isCircleEntry,
                Fragments = fragments,
                Indent = indent,
            });
        }

        // Recurse into subagent children.
        if (entry is SubagentContainerEntry container)
        {
            var childHasEmittedNonPlain = false;
            foreach (var child in container.Children)
                LayoutEntry(child, contentWidth, indent + 2, rows, ref childHasEmittedNonPlain);
        }
    }

    /// <summary>Determine the visual style for an entry.</summary>
    private static EntryStyle GetEntryStyle(ConversationEntry entry) => entry switch
    {
        UserMessageEntry => EntryStyle.User,
        CancellationEntry => EntryStyle.Plain,
        _ => EntryStyle.Circle,
    };

    /// <summary>Get the circle color for the first line of an entry, or null for plain entries.</summary>
    private Color? GetCircleColor(ConversationEntry entry) => entry switch
    {
        UserMessageEntry => _theme.UserCircle,
        AssistantTextEntry => _theme.Text,
        ToolCallEntry tc => tc.Status switch
        {
            ToolCallStatus.Started or ToolCallStatus.AwaitingApproval => _theme.ToolCircleStarted,
            ToolCallStatus.Succeeded => _theme.ToolCircleSuccess,
            ToolCallStatus.Failed => _theme.ToolCircleError,
            _ => _theme.ToolCircleStarted,
        },
        PlanEntry => _theme.ToolCircleStarted,
        SubagentContainerEntry sc => sc.Status switch
        {
            ToolCallStatus.Succeeded => _theme.ToolCircleSuccess,
            _ => _theme.ToolCircleStarted,
        },
        ErrorEntry => _theme.ToolCircleError,
        _ => null,
    };

    /// <summary>
    /// Build the styled text segments for an entry. Each segment carries its
    /// text and a ColorFragment for rendering.
    /// </summary>
    private List<LayoutFragment<ColorFragment>> BuildSegments(ConversationEntry entry)
    {
        var segments = new List<LayoutFragment<ColorFragment>>();

        switch (entry)
        {
            case UserMessageEntry user:
                segments.Add(new(user.Text.TrimEnd('\n', '\r'), new ColorFragment(_theme.Text)));
                break;

            case AssistantTextEntry assistant:
                segments.Add(new(assistant.Text.Trim('\n', '\r'), new ColorFragment(_theme.Text)));
                break;

            case ToolCallEntry tool:
                segments.Add(new(tool.FormattedSignature, new ColorFragment(_theme.ToolSignature)));
                if (tool.Result is not null)
                {
                    // Suppress successful todo_write results (the plan block is sufficient).
                    var suppressResult = tool.ToolName == "todo_write" && !tool.IsError;
                    if (!suppressResult)
                    {
                        var resultLines = tool.Result.Split('\n');
                        var maxLines = 5;
                        var displayLines = resultLines.Take(maxLines).ToArray();
                        var resultText = "└─ " + string.Join("\n   ", displayLines);
                        segments.Add(new("\n" + resultText, new ColorFragment(_theme.ToolSignature)));

                        if (resultLines.Length > maxLines)
                        {
                            var remaining = resultLines.Length - maxLines;
                            segments.Add(new($"\n   ({remaining} more lines)", new ColorFragment(_theme.ToolSignature)));
                        }
                    }
                }
                break;

            case PlanEntry plan:
                segments.Add(new("Plan", new ColorFragment(_theme.ToolSignature)));
                foreach (var item in plan.Items)
                {
                    var marker = item.Status switch
                    {
                        PlanItemStatus.Completed => "✓",
                        PlanItemStatus.InProgress => "●",
                        _ => "○",
                    };
                    var markerColor = item.Status switch
                    {
                        PlanItemStatus.Completed => _theme.ToolCircleSuccess,
                        PlanItemStatus.InProgress => _theme.ToolCircleStarted,
                        _ => _theme.ToolSignature,
                    };
                    segments.Add(new($"\n  {marker} {item.Content}", new ColorFragment(markerColor)));
                }
                break;

            case SubagentContainerEntry subagent:
                segments.Add(new(subagent.FormattedSignature, new ColorFragment(_theme.ToolSignature)));
                break;

            case ErrorEntry error:
                segments.Add(new($"[error] {error.Message}", new ColorFragment(_theme.ToolCircleError)));
                break;

            case CancellationEntry:
                segments.Add(new("[cancelled]", new ColorFragment(_theme.ToolSignature)));
                break;
        }

        return segments;
    }

    /// <summary>Render a vertical scrollbar on the right edge.</summary>
    private void RenderScrollbar(ConsoleBuffer buffer, int x, int y, int viewportHeight, int contentHeight)
    {
        if (viewportHeight <= 0 || contentHeight <= 0) return;

        // Compute thumb position and size.
        var thumbHeight = Math.Max(1, viewportHeight * viewportHeight / contentHeight);
        var thumbTop = contentHeight > viewportHeight
            ? _scrollOffset * (viewportHeight - thumbHeight) / (contentHeight - viewportHeight)
            : 0;

        for (var row = 0; row < viewportHeight; row++)
        {
            var isThumb = row >= thumbTop && row < thumbTop + thumbHeight;
            var color = isThumb ? _theme.Border : _theme.Divider;
            buffer.SetCell(x, y + row, '│', color, Color.Default);
        }
    }
}

/// <summary>
/// A single rendered row in the flat layout, ready to be drawn.
/// </summary>
internal sealed class RenderedRow
{
    public static readonly RenderedRow Blank = new();

    /// <summary>Circle color for the first line of an entry, or null.</summary>
    public Color? CircleColor { get; init; }

    /// <summary>Whether this row belongs to a circle-prefixed entry (for continuation indent).</summary>
    public bool IsCircleEntry { get; init; }

    /// <summary>Styled text fragments for this row.</summary>
    public IReadOnlyList<ColorFragment> Fragments { get; init; } = [];

    /// <summary>Horizontal indent in columns (for sub-agent nesting).</summary>
    public int Indent { get; init; }
}

/// <summary>
/// A text fragment with its display color, used as the style type in
/// <see cref="ConversationTextLayout"/> when rendering to a ConsoleBuffer.
/// </summary>
public sealed record ColorFragment(Color Color)
{
    /// <summary>The text content — set during layout.</summary>
    public string Text { get; init; } = string.Empty;
}
