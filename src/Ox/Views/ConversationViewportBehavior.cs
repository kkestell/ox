namespace Ox.Views;

/// <summary>
/// Pure viewport math shared by <see cref="ConversationView"/>.
///
/// The actual view uses Terminal.Gui's scrolling APIs, but its horizontal gutter
/// and stick-to-bottom behavior are deterministic calculations that are easier to
/// lock down in unit tests without requiring a live terminal stack.
/// </summary>
internal static class ConversationViewportBehavior
{
    public const int HorizontalPaddingColumns = 1;

    public static int GetContentWidth(int viewportWidth) =>
        Math.Max(1, viewportWidth - (HorizontalPaddingColumns * 2));

    public static bool IsVerticalWheel(bool wheeledUp, bool wheeledDown) =>
        wheeledUp || wheeledDown;

    public static int GetWheelDelta(bool wheeledUp, bool wheeledDown)
    {
        if (wheeledUp)
            return -1;

        if (wheeledDown)
            return 1;

        return 0;
    }

    public static int GetContentHeight(int renderedLineCount, int viewportHeight) =>
        Math.Max(viewportHeight, renderedLineCount);

    public static int GetBottomViewportY(int contentHeight, int viewportHeight) =>
        Math.Max(0, contentHeight - viewportHeight);

    public static bool IsPinnedToBottom(int viewportY, int contentHeight, int viewportHeight) =>
        viewportY >= GetBottomViewportY(contentHeight, viewportHeight);

    public static int ClampViewportY(int viewportY, int contentHeight, int viewportHeight)
    {
        var maxY = GetBottomViewportY(contentHeight, viewportHeight);
        return Math.Clamp(viewportY, 0, maxY);
    }
}
