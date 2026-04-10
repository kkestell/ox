using Ox.Views;

namespace Ur.Tests;

/// <summary>
/// Unit tests for the pure viewport math that ConversationView layers on top of
/// Terminal.Gui's scrolling APIs.
/// </summary>
public sealed class ConversationViewportBehaviorTests
{
    [Fact]
    public void HorizontalPadding_UsesSingleColumnPerSide()
    {
        Assert.Equal(1, ConversationViewportBehavior.HorizontalPaddingColumns);
    }

    [Theory]
    [InlineData(true, false, -1)]
    [InlineData(false, true, 1)]
    [InlineData(false, false, 0)]
    public void GetWheelDelta_MapsWheelDirectionToVerticalScrollDelta(
        bool wheeledUp,
        bool wheeledDown,
        int expectedDelta)
    {
        var delta = ConversationViewportBehavior.GetWheelDelta(wheeledUp, wheeledDown);

        Assert.Equal(expectedDelta, delta);
    }

    [Theory]
    [InlineData(true, false, true)]
    [InlineData(false, true, true)]
    [InlineData(false, false, false)]
    public void IsVerticalWheel_DetectsWheelEvents(bool wheeledUp, bool wheeledDown, bool expected)
    {
        var isWheel = ConversationViewportBehavior.IsVerticalWheel(wheeledUp, wheeledDown);

        Assert.Equal(expected, isWheel);
    }

    [Theory]
    [InlineData(10, 8)]
    [InlineData(2, 1)]
    [InlineData(1, 1)]
    public void GetContentWidth_ReservesOneColumnOnEachSide(int viewportWidth, int expectedWidth)
    {
        var contentWidth = ConversationViewportBehavior.GetContentWidth(viewportWidth);

        Assert.Equal(expectedWidth, contentWidth);
    }

    [Theory]
    [InlineData(7, 3, 7)]
    [InlineData(2, 3, 3)]
    public void GetContentHeight_IsAtLeastViewportHeight(int renderedLineCount, int viewportHeight, int expected)
    {
        var contentHeight = ConversationViewportBehavior.GetContentHeight(renderedLineCount, viewportHeight);

        Assert.Equal(expected, contentHeight);
    }

    [Theory]
    [InlineData(7, 3, 4)]
    [InlineData(2, 3, 0)]
    public void GetBottomViewportY_ReturnsLastReachableScrollOffset(int contentHeight, int viewportHeight, int expected)
    {
        var bottom = ConversationViewportBehavior.GetBottomViewportY(contentHeight, viewportHeight);

        Assert.Equal(expected, bottom);
    }

    [Theory]
    [InlineData(4, 7, 3, true)]
    [InlineData(3, 7, 3, false)]
    [InlineData(0, 2, 3, true)]
    public void IsPinnedToBottom_DetectsWhetherAutoScrollShouldRemainEnabled(
        int viewportY,
        int contentHeight,
        int viewportHeight,
        bool expected)
    {
        var pinned = ConversationViewportBehavior.IsPinnedToBottom(viewportY, contentHeight, viewportHeight);

        Assert.Equal(expected, pinned);
    }

    [Theory]
    [InlineData(-1, 7, 3, 0)]
    [InlineData(2, 7, 3, 2)]
    [InlineData(10, 7, 3, 4)]
    public void ClampViewportY_ClampsWithinScrollableBounds(int viewportY, int contentHeight, int viewportHeight, int expected)
    {
        var clamped = ConversationViewportBehavior.ClampViewportY(viewportY, contentHeight, viewportHeight);

        Assert.Equal(expected, clamped);
    }
}
