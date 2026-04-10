using System.Drawing;
using Terminal.Gui.App;
using Terminal.Gui.ViewBase;

namespace Ox.Views;

/// <summary>
/// Scrollable container that holds one <see cref="ConversationEntryView"/> per
/// conversation entry, stacked vertically with Pos.Bottom chains.
///
/// Terminal.Gui handles layout, clipping, and the built-in scrollbar.
/// This view manages the entry-to-SubView lifecycle and auto-scroll
/// pin-to-bottom behavior (keeping the user at the tail during streaming).
///
/// The conversation data model (<see cref="ConversationEntry"/>) is unchanged.
/// The <see cref="EventRouter"/> creates entries and calls <see cref="AddEntry"/>
/// exactly as before — the internal change from canvas to composite is invisible
/// to callers.
/// </summary>
internal sealed class ConversationView : View
{
    private readonly List<ConversationEntryView> _entryViews = [];
    private readonly IApplication _app;
    private bool _autoScrollPinnedToBottom = true;

    // Tracks whether a non-Plain entry has been emitted, so we know to
    // insert a blank-line gap before the next non-Plain entry.
    private bool _emittedNonPlainEntry;

    /// <summary>Raised when content changes (entries added/mutated).</summary>
    public event Action? ContentChanged;

    public ConversationView(IApplication app)
    {
        _app = app;
        CanFocus = false;

        // Enable the built-in vertical scrollbar. Terminal.Gui shows it
        // automatically when content height exceeds viewport height.
        ViewportSettings |= ViewportSettingsFlags.HasVerticalScrollBar;
    }

    /// <summary>
    /// Returns the number of top-level entries. Used to detect empty state
    /// for splash screen toggling.
    /// </summary>
    public int EntryCount => _entryViews.Count;

    /// <summary>
    /// Adds a top-level entry to the conversation. Creates a
    /// <see cref="ConversationEntryView"/> SubView, positions it after the
    /// last entry, and wires change notifications for auto-scroll.
    /// </summary>
    public void AddEntry(ConversationEntry entry)
    {
        var entryView = new ConversationEntryView(entry);
        entryView.Width = Dim.Fill();
        entryView.Height = Dim.Absolute(1); // Initial; recalculated on layout

        // Insert a blank-line gap between consecutive non-Plain entries
        // (User messages, assistant text, tool calls) for visual separation.
        // Plain entries (continuation content) get no spacing.
        var needsSpacing = ConversationViewportBehavior.NeedsSpacingBefore(
            entry.Style, _emittedNonPlainEntry);

        if (entry.Style != EntryStyle.Plain)
            _emittedNonPlainEntry = true;

        // Stack vertically: first entry at top, subsequent entries chain
        // below the previous one. Add 1-row margin for spacing when needed.
        if (_entryViews.Count == 0)
        {
            entryView.Y = Pos.Absolute(0);
        }
        else
        {
            entryView.Y = needsSpacing
                ? Pos.Bottom(_entryViews[^1]) + 1
                : Pos.Bottom(_entryViews[^1]);
        }

        // When any entry's height changes (streaming text, tool results),
        // recalculate total content size and auto-scroll if pinned.
        entryView.EntryHeightChanged += OnEntryHeightChanged;

        _entryViews.Add(entryView);
        Add(entryView);

        UpdateContentSizeAndScroll();
        ContentChanged?.Invoke();
    }

    /// <summary>
    /// Detects manual scrolling by the user. If the user scrolls away from
    /// the bottom, auto-scroll is disabled. If they scroll back to the
    /// bottom, auto-scroll is re-enabled.
    /// </summary>
    protected override void OnViewportChanged(DrawEventArgs args)
    {
        base.OnViewportChanged(args);

        if (_entryViews.Count == 0)
            return;

        var contentHeight = GetTotalContentHeight();
        var viewportHeight = Viewport.Height;

        _autoScrollPinnedToBottom = ConversationViewportBehavior.IsPinnedToBottom(
            Viewport.Y, contentHeight, viewportHeight);
    }

    /// <summary>
    /// Recomputes the total content height from all entry views and updates
    /// the scroll position if auto-scroll is pinned to bottom.
    /// </summary>
    private void UpdateContentSizeAndScroll()
    {
        var viewportHeight = Viewport.Height;
        if (viewportHeight <= 0)
            return;

        var totalHeight = GetTotalContentHeight();

        // Ensure content size is at least the viewport height so Terminal.Gui
        // doesn't produce negative scroll offsets.
        var effectiveHeight = Math.Max(totalHeight, viewportHeight);
        SetContentSize(new Size(Viewport.Width, effectiveHeight));

        if (_autoScrollPinnedToBottom)
        {
            var bottomY = Math.Max(0, effectiveHeight - viewportHeight);
            if (Viewport.Y != bottomY)
            {
                Viewport = Viewport with { Y = bottomY };
            }
            _autoScrollPinnedToBottom = true;
        }

        SetNeedsDraw();
    }

    private void OnEntryHeightChanged()
    {
        _app.Invoke(() =>
        {
            UpdateContentSizeAndScroll();
            ContentChanged?.Invoke();
        });
    }

    /// <summary>
    /// Computes total scrollable content height by examining Frame.Y + Frame.Height
    /// of the last entry view. This accounts for both entry heights and the spacing
    /// gaps inserted via Pos.Bottom + 1.
    /// </summary>
    private int GetTotalContentHeight()
    {
        if (_entryViews.Count == 0)
            return 0;

        var lastView = _entryViews[^1];
        return lastView.Frame.Y + (lastView.Frame.Height > 0 ? lastView.Frame.Height : 1);
    }
}

/// <summary>
/// A single pre-rendered line of the conversation, composed of styled spans.
/// Used by <see cref="ConversationEntryView"/> for its own drawing.
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
internal readonly record struct RenderSpan(string Text, Terminal.Gui.Drawing.Color Foreground, Terminal.Gui.Drawing.Color Background, bool Bold = false);
