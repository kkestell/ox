using System.Drawing;
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
/// Height management follows the idiomatic Terminal.Gui v2 pattern:
///   - Each entry uses Dim.Auto(DimAutoStyle.Content) on its Height.
///   - We subscribe to SubViewsLaidOut (fires after all frames are set) and
///     call SetContentSize there so GetHeightRequiredForSubViews() is accurate.
/// This means the content size is always updated with real Frame values, not
/// speculative pre-layout values, eliminating the one-pass-behind rendering bug.
/// </summary>
internal sealed class ConversationView : View
{
    private readonly List<ConversationEntryView> _entryViews = [];
    private bool _autoScrollPinnedToBottom = true;

    // Tracks whether a non-Plain entry has been emitted, so we know to
    // insert a blank-line gap before the next non-Plain entry.
    private bool _emittedNonPlainEntry;

    /// <summary>Raised when content changes (entries added/mutated).</summary>
    public event Action? ContentChanged;

    public ConversationView()
    {
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

        // Dim.Auto(Content): when the entry has no SubViews, DimAuto reads the
        // entry's SetContentSize value as the height — no manual height tracking.
        entryView.Height = Dim.Auto(DimAutoStyle.Content);

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

        _entryViews.Add(entryView);
        Add(entryView);

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

        // GetContentSize() returns the explicitly-set size from UpdateContentSizeAndScroll,
        // which is accurate after any prior SubViewsLaidOut cycle.
        _autoScrollPinnedToBottom = ConversationViewportBehavior.IsPinnedToBottom(
            Viewport.Y, GetContentSize().Height, Viewport.Height);
    }

    /// <summary>
    /// Called by Terminal.Gui after all SubViews have been laid out and their
    /// Frames are up-to-date. This is the correct hook to update the scrollable
    /// content size — unlike calling SetContentSize mid-layout, the values here
    /// reflect the actual computed heights, not speculative estimates.
    /// </summary>
    protected override void OnSubViewsLaidOut(LayoutEventArgs args)
    {
        base.OnSubViewsLaidOut(args);
        UpdateContentSizeAndScroll();
    }

    /// <summary>
    /// Recomputes the total content height and updates the scroll position
    /// if auto-scroll is pinned to bottom.
    /// </summary>
    private void UpdateContentSizeAndScroll()
    {
        var viewportHeight = Viewport.Height;
        if (viewportHeight <= 0)
            return;

        // GetHeightRequiredForSubViews() uses the same DimAuto calculation
        // that Terminal.Gui's layout uses, so it agrees with the actual frame
        // heights after layout — no stale values.
        var totalHeight = GetHeightRequiredForSubViews();
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
