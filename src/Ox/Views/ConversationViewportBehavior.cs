namespace Ox.Views;

/// <summary>
/// Pure helper methods for conversation viewport math — content width
/// calculation, auto-scroll detection, and inter-entry spacing rules.
///
/// These are extracted as static methods so they can be unit-tested without
/// instantiating any views or rendering infrastructure.
/// </summary>
public static class ConversationViewportBehavior
{
    /// <summary>
    /// Number of columns reserved on each side of the viewport for horizontal
    /// padding. Top-level entries are inset by this many columns.
    /// </summary>
    public const int HorizontalPaddingColumns = 1;

    /// <summary>
    /// Compute the usable content width given the total viewport width.
    /// Reserves <see cref="HorizontalPaddingColumns"/> on each side.
    /// </summary>
    public static int GetContentWidth(int viewportWidth)
    {
        // Ensure at least 1 column of content even in degenerate viewports.
        return Math.Max(1, viewportWidth - (HorizontalPaddingColumns * 2));
    }

    /// <summary>
    /// Returns true if the viewport is scrolled to (or past) the bottom of
    /// the content, meaning auto-scroll should remain engaged.
    /// </summary>
    /// <param name="viewportY">Current scroll offset (top row of viewport in content coordinates).</param>
    /// <param name="contentHeight">Total height of all laid-out content in rows.</param>
    /// <param name="viewportHeight">Height of the visible viewport in rows.</param>
    public static bool IsPinnedToBottom(int viewportY, int contentHeight, int viewportHeight)
    {
        // If the content fits entirely within the viewport, we're always pinned.
        if (contentHeight <= viewportHeight)
            return true;

        // Pinned when the viewport's bottom edge reaches the content's bottom edge.
        return viewportY + viewportHeight >= contentHeight;
    }

    /// <summary>
    /// Determines whether a blank spacing line should be inserted before an
    /// entry of the given <paramref name="style"/>.
    /// </summary>
    /// <param name="style">The visual style of the entry about to be rendered.</param>
    /// <param name="hasEmittedNonPlain">
    /// True if at least one non-Plain entry has already been rendered above this one.
    /// </param>
    /// <returns>True if a blank line should precede this entry.</returns>
    public static bool NeedsSpacingBefore(EntryStyle style, bool hasEmittedNonPlain)
    {
        // Plain entries are continuation content — they never get spacing.
        if (style == EntryStyle.Plain)
            return false;

        // Non-plain entries get spacing only if there's a preceding non-plain entry.
        return hasEmittedNonPlain;
    }
}
