using Ox.App.Views;

namespace Ox.Tests.App.Views;

/// <summary>
/// Unit tests for the surviving viewport math in <see cref="ConversationViewportBehavior"/>.
/// Most viewport management is now handled by Terminal.Gui's built-in scrolling;
/// these tests cover the Ox-specific helpers that remain.
/// </summary>
public sealed class ConversationViewportBehaviorTests
{
    [Fact]
    public void HorizontalPadding_UsesSingleColumnPerSide()
    {
        Assert.Equal(1, ConversationViewportBehavior.HorizontalPaddingColumns);
    }

    [Fact]
    public void VerticalPadding_UsesSingleRowPerSide()
    {
        Assert.Equal(1, ConversationViewportBehavior.VerticalPaddingRows);
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
    [InlineData(10, 8)]
    [InlineData(2, 1)]
    [InlineData(1, 1)]
    public void GetContentHeight_ReservesOneRowOnTopAndBottom(int viewportHeight, int expectedHeight)
    {
        var contentHeight = ConversationViewportBehavior.GetContentHeight(viewportHeight);

        Assert.Equal(expectedHeight, contentHeight);
    }

    [Theory]
    [InlineData(4, 7, 3, true)]   // At bottom
    [InlineData(3, 7, 3, false)]  // One row above bottom
    [InlineData(0, 2, 3, true)]   // Content smaller than viewport — always pinned
    public void IsPinnedToBottom_DetectsWhetherAutoScrollShouldRemainEnabled(
        int viewportY,
        int contentHeight,
        int viewportHeight,
        bool expected)
    {
        var pinned = ConversationViewportBehavior.IsPinnedToBottom(viewportY, contentHeight, viewportHeight);

        Assert.Equal(expected, pinned);
    }
}
