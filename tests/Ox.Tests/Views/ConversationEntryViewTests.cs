using Ox.Views;

namespace Ox.Tests.Views;

/// <summary>
/// Tests for the layout logic used by <see cref="ConversationEntryView"/>.
///
/// These tests exercise the pure text layout paths through
/// <see cref="ConversationEntryView"/> and <see cref="ConversationTextLayout"/>
/// without requiring Terminal.Gui types (which need an application context).
/// The layout engine is the same one ConversationEntryView calls internally —
/// it just uses generic LayoutFragment{string} instead of StyledSegment.
/// </summary>
public sealed class ConversationEntryViewTests
{
    // --- Text wrapping ---

    [Fact]
    public void LayoutSegments_ShortText_SingleLine()
    {
        var lines = ConversationTextLayout.LayoutSegments(
            [new LayoutFragment<string>("hello", "style")],
            width: 40);

        Assert.Single(lines);
        Assert.Equal("hello", string.Concat(lines[0].Select(f => f.Text)));
    }

    [Fact]
    public void LayoutSegments_LongTextWrapsAtWidth()
    {
        // "hello world" at width 6: "hello" fits on line 1, "world" wraps.
        var lines = ConversationTextLayout.LayoutSegments(
            [new LayoutFragment<string>("hello world", "style")],
            width: 6);

        Assert.Equal(2, lines.Count);
        Assert.Equal("hello", string.Concat(lines[0].Select(f => f.Text)));
        Assert.Equal("world", string.Concat(lines[1].Select(f => f.Text)));
    }

    [Fact]
    public void LayoutSegments_ExplicitNewlines_CreateSeparateLines()
    {
        var lines = ConversationTextLayout.LayoutSegments(
            [new LayoutFragment<string>("line1\nline2\nline3", "style")],
            width: 40);

        Assert.Equal(3, lines.Count);
        Assert.Equal("line1", string.Concat(lines[0].Select(f => f.Text)));
        Assert.Equal("line2", string.Concat(lines[1].Select(f => f.Text)));
        Assert.Equal("line3", string.Concat(lines[2].Select(f => f.Text)));
    }

    [Fact]
    public void LayoutSegments_WordBoundaryRespected()
    {
        // "abc def ghi" at width 8: "abc def" fits (7 chars), "ghi" wraps.
        var lines = ConversationTextLayout.LayoutSegments(
            [new LayoutFragment<string>("abc def ghi", "style")],
            width: 8);

        Assert.Equal(2, lines.Count);
        Assert.Equal("abc def", string.Concat(lines[0].Select(f => f.Text)));
        Assert.Equal("ghi", string.Concat(lines[1].Select(f => f.Text)));
    }

    // --- Height calculation scenarios ---

    [Fact]
    public void HeightCalculation_CircleEntry_SubtractsChromeWidth()
    {
        // ConversationEntryView computes content width as:
        //   paddedWidth - CircleChrome  (for non-Plain entries)
        // With viewport=20, padding=1 each side, chrome=2:
        //   contentWidth = (20 - 2) - 2 = 16
        // "abcdefghijklmnopqrstuvwxyz" (26 chars) at width 16 = 2 lines.
        var contentWidth = 20 - (ConversationViewportBehavior.HorizontalPaddingColumns * 2)
                           - ConversationEntryView.CircleChrome;

        var lines = ConversationTextLayout.LayoutSegments(
            [new LayoutFragment<string>("abcdefghijklmnopqrstuvwxyz", "s")],
            width: contentWidth);

        Assert.Equal(16, contentWidth);
        Assert.Equal(2, lines.Count);
    }

    [Fact]
    public void HeightCalculation_PlainEntry_UsesFullPaddedWidth()
    {
        // Plain entries don't subtract CircleChrome.
        //   contentWidth = 20 - 2 = 18
        var contentWidth = 20 - (ConversationViewportBehavior.HorizontalPaddingColumns * 2);

        var lines = ConversationTextLayout.LayoutSegments(
            [new LayoutFragment<string>("short text", "s")],
            width: contentWidth);

        Assert.Single(lines);
    }

    // --- Streaming text growth ---

    [Fact]
    public void StreamingText_GrowthIncreasesLineCount()
    {
        // Start with "hi" (1 line at width 10), grow to a longer string.
        var text = "hi";
        var linesBefore = ConversationTextLayout.LayoutSegments(
            [new LayoutFragment<string>(text, "s")],
            width: 10).Count;

        Assert.Equal(1, linesBefore);

        text = "hi there world and then some more words";
        var linesAfter = ConversationTextLayout.LayoutSegments(
            [new LayoutFragment<string>(text, "s")],
            width: 10).Count;

        Assert.Equal(4, linesAfter);
    }

    // --- Tool result newline-prefixed segments ---

    [Fact]
    public void ToolResult_NewlinePrefixedSegments_LayoutCorrectly()
    {
        // Tool results append segments prefixed with \n (e.g., "\n└─ result").
        // This must not create phantom blank rows.
        var lines = ConversationTextLayout.LayoutSegments(
            [
                new LayoutFragment<string>("Read(\"file.txt\")", "sig"),
                new LayoutFragment<string>("\n└─ contents here", "result")
            ],
            width: 60);

        Assert.Equal(2, lines.Count);
        Assert.Equal("Read(\"file.txt\")", string.Concat(lines[0].Select(f => f.Text)));
        Assert.Equal("└─ contents here", string.Concat(lines[1].Select(f => f.Text)));
    }

    // --- Horizontal padding ---

    [Fact]
    public void ContentWidth_AccountsForHorizontalPadding()
    {
        Assert.Equal(18, ConversationViewportBehavior.GetContentWidth(20));
        Assert.Equal(8, ConversationViewportBehavior.GetContentWidth(10));
        Assert.Equal(1, ConversationViewportBehavior.GetContentWidth(1));
    }
}

/// <summary>
/// Tests for the inter-entry spacing decision logic extracted into
/// <see cref="ConversationViewportBehavior.NeedsSpacingBefore"/>.
/// </summary>
public sealed class EntrySpacingTests
{
    [Fact]
    public void FirstNonPlainEntry_NoSpacing()
    {
        // The very first non-Plain entry has no predecessor — no spacing.
        Assert.False(ConversationViewportBehavior.NeedsSpacingBefore(
            EntryStyle.Circle, hasEmittedNonPlain: false));
    }

    [Fact]
    public void ConsecutiveNonPlainEntries_GetSpacing()
    {
        // Two Circle entries in a row should have a blank line between them.
        Assert.True(ConversationViewportBehavior.NeedsSpacingBefore(
            EntryStyle.Circle, hasEmittedNonPlain: true));
    }

    [Fact]
    public void UserAfterCircle_GetsSpacing()
    {
        Assert.True(ConversationViewportBehavior.NeedsSpacingBefore(
            EntryStyle.User, hasEmittedNonPlain: true));
    }

    [Fact]
    public void PlainEntry_NeverGetsSpacing()
    {
        // Plain entries are continuation content — no gap.
        Assert.False(ConversationViewportBehavior.NeedsSpacingBefore(
            EntryStyle.Plain, hasEmittedNonPlain: true));
    }

    [Fact]
    public void PlainEntry_DoesNotTriggerSpacingRegardlessOfHistory()
    {
        Assert.False(ConversationViewportBehavior.NeedsSpacingBefore(
            EntryStyle.Plain, hasEmittedNonPlain: false));
    }
}
