namespace Ox.Views;

/// <summary>
/// Shared viewport math for the conversation view system.
///
/// Most viewport management (scrolling, clamping, content height) is now
/// handled by Terminal.Gui's built-in infrastructure. This class retains
/// only the Ox-specific calculations: horizontal padding and the
/// pin-to-bottom detection used for auto-scroll during streaming.
/// </summary>
internal static class ConversationViewportBehavior
{
    /// <summary>
    /// Number of columns reserved on each side of the conversation as a
    /// visual gutter. Applied by <see cref="ConversationEntryView"/> when
    /// drawing top-level (non-child) entries.
    /// </summary>
    public const int HorizontalPaddingColumns = 1;

    /// <summary>
    /// Returns the usable content width after subtracting horizontal padding
    /// on both sides.
    /// </summary>
    public static int GetContentWidth(int viewportWidth) =>
        Math.Max(1, viewportWidth - (HorizontalPaddingColumns * 2));

    /// <summary>
    /// Determines whether the viewport is currently scrolled to the bottom.
    /// Used by <see cref="ConversationView"/> to decide whether new content
    /// should trigger auto-scroll.
    /// </summary>
    public static bool IsPinnedToBottom(int viewportY, int contentHeight, int viewportHeight) =>
        viewportY >= Math.Max(0, contentHeight - viewportHeight);

    /// <summary>
    /// Determines whether a blank-line gap should be inserted before a new
    /// entry. Non-Plain entries (User messages, assistant text, tool calls)
    /// get a gap when preceded by another non-Plain entry. Plain entries
    /// (continuation content like tool results) never trigger spacing.
    /// </summary>
    public static bool NeedsSpacingBefore(EntryStyle style, bool hasEmittedNonPlain) =>
        style != EntryStyle.Plain && hasEmittedNonPlain;
}
