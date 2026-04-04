using Ur.Console;
using Ur.Drawing;
using Ur.Widgets;
using Xunit;

namespace Ur.Tests.Widgets;

public class ScrollViewTests
{
    // Creates a ScrollView with a stack of n labels, each 1 row tall.
    private static (ScrollView scrollView, ListView<int> listView) MakeScrollView(
        int itemCount,
        int viewportWidth,
        int viewportHeight)
    {
        var listView = new ListView<int>(n => new Label($"Item {n}"));
        for (var i = 0; i < itemCount; i++)
            listView.Items.Add(i);

        var scrollView = new ScrollView(listView)
        {
            X = 0,
            Y = 0,
            Width = viewportWidth,
            Height = viewportHeight
        };

        return (scrollView, listView);
    }

    // Renders a ScrollView backed by a real Screen so we can inspect output.
    // This uses the Renderer to properly handle content layout and drawing through the widget tree.
    private static Screen DrawScrollView(ScrollView sv)
    {
        // Run layout to measure content and set OffsetY
        sv.Layout(sv.Width, sv.Height);

        // Then render the entire widget tree (which draws content + scrollbar)
        return Renderer.Render(sv);
    }

    // Reads a row of the screen as a string for easy assertions.
    private static string RowText(Screen screen, int row)
    {
        var chars = new char[screen.Width];
        for (ushort col = 0; col < screen.Width; col++)
            chars[col] = screen.Get(col, (ushort)row).Rune;
        return new string(chars).TrimEnd();
    }

    // -------------------------------------------------------------------------
    // Scroll offset clamping
    // -------------------------------------------------------------------------

    [Fact]
    public void ScrollOffset_ClampedToZeroAtTop()
    {
        // Simulate pressing Up repeatedly past the top — offset must not go negative.
        // Draw first to populate _contentHeight (matches real app flow: Draw runs each frame).
        var (sv, _) = MakeScrollView(10, 20, 5);
        sv.AutoScroll = false;
        DrawScrollView(sv); // populate _contentHeight

        for (var i = 0; i < 20; i++)
            sv.HandleInput(new KeyEvent(Key.Up));

        var screen = DrawScrollView(sv);
        // If offset were negative we'd see blank rows at top; instead we see Item 0.
        Assert.Contains("Item 0", RowText(screen, 0));
    }

    [Fact]
    public void ScrollOffset_ClampedToMaxAtBottom()
    {
        // Scrolling Down past the bottom should not go beyond the last row.
        // Draw first to populate _contentHeight (matches real app flow: Draw runs each frame).
        var (sv, _) = MakeScrollView(10, 20, 5);
        sv.AutoScroll = false;
        DrawScrollView(sv); // populate _contentHeight

        for (var i = 0; i < 20; i++)
            sv.HandleInput(new KeyEvent(Key.Down));

        // With 10 items and viewport height 5, maxScroll = 5.
        // The last item (Item 9) should be visible after scrolling to the bottom.
        var screen = DrawScrollView(sv);
        var visibleRows = string.Join(" ", Enumerable.Range(0, sv.Height).Select(r => RowText(screen, r)));
        Assert.Contains("Item 9", visibleRows);
    }

    // -------------------------------------------------------------------------
    // AutoScroll behavior
    // -------------------------------------------------------------------------

    [Fact]
    public void AutoScroll_PinsViewportToBottom_AsContentGrows()
    {
        var (sv, listView) = MakeScrollView(0, 20, 5);
        sv.AutoScroll = true;

        // Add items one at a time and verify we always see the latest.
        for (var i = 0; i < 10; i++)
        {
            listView.Items.Add(i);
            var screen = DrawScrollView(sv);
            var visibleRows = string.Join(" ", Enumerable.Range(0, sv.Height).Select(r => RowText(screen, r)));
            Assert.Contains($"Item {i}", visibleRows);
        }
    }

    [Fact]
    public void AutoScroll_DisabledAfterScrollingUp()
    {
        var (sv, listView) = MakeScrollView(10, 20, 5);
        sv.AutoScroll = true;

        // Render once to pin auto-scroll to bottom, then scroll up.
        DrawScrollView(sv);
        sv.HandleInput(new KeyEvent(Key.Up));

        // Add a new item — if auto-scroll were still active, the viewport would jump.
        listView.Items.Add(99);
        var screen = DrawScrollView(sv);

        // "Item 99" should NOT be visible because auto-scroll was paused.
        var visibleRows = string.Join(" ", Enumerable.Range(0, sv.Height).Select(r => RowText(screen, r)));
        Assert.DoesNotContain("Item 99", visibleRows);
    }

    [Fact]
    public void AutoScroll_ReenabledAfterScrollingBackToBottom()
    {
        var (sv, listView) = MakeScrollView(10, 20, 5);
        sv.AutoScroll = true;

        // Render to pin, scroll up one, then back down to the bottom.
        DrawScrollView(sv);
        sv.HandleInput(new KeyEvent(Key.Up));

        // Press Down enough times to reach the bottom.
        for (var i = 0; i < 20; i++)
            sv.HandleInput(new KeyEvent(Key.Down));

        // Add a new item — auto-scroll should have re-engaged.
        listView.Items.Add(99);
        var screen = DrawScrollView(sv);

        var visibleRows = string.Join(" ", Enumerable.Range(0, sv.Height).Select(r => RowText(screen, r)));
        Assert.Contains("Item 99", visibleRows);
    }

    // -------------------------------------------------------------------------
    // Blit / viewport content correctness
    // -------------------------------------------------------------------------

    [Fact]
    public void Draw_ShowsCorrectRowsForScrollOffset()
    {
        var (sv, _) = MakeScrollView(10, 20, 5);
        sv.AutoScroll = false;

        // Render once at offset=0 so _contentHeight is populated.
        DrawScrollView(sv);

        // Scroll down 3 rows manually — we should see items 3..7.
        sv.HandleInput(new KeyEvent(Key.Down));
        sv.HandleInput(new KeyEvent(Key.Down));
        sv.HandleInput(new KeyEvent(Key.Down));

        var screen = DrawScrollView(sv);

        Assert.Contains("Item 3", RowText(screen, 0));
        Assert.Contains("Item 4", RowText(screen, 1));
        Assert.Contains("Item 5", RowText(screen, 2));
        Assert.Contains("Item 6", RowText(screen, 3));
        Assert.Contains("Item 7", RowText(screen, 4));
    }

    [Fact]
    public void Draw_ContentSmallerThanViewport_ShowsFromTop()
    {
        var (sv, _) = MakeScrollView(3, 20, 10);
        sv.AutoScroll = false;

        var screen = DrawScrollView(sv);

        Assert.Contains("Item 0", RowText(screen, 0));
        Assert.Contains("Item 1", RowText(screen, 1));
        Assert.Contains("Item 2", RowText(screen, 2));
    }

    // -------------------------------------------------------------------------
    // Scrollbar thumb position
    // -------------------------------------------------------------------------

    [Fact]
    public void Scrollbar_ThumbAtTop_WhenScrolledToTop()
    {
        var (sv, _) = MakeScrollView(20, 20, 5);
        sv.AutoScroll = false;

        // Force offset to 0 by not drawing (no auto-scroll).
        var screen = DrawScrollView(sv);

        // The scrollbar column is Width-1 = 19.
        // At the top the thumb should start at row 0.
        var topCell = screen.Get((ushort)(sv.Width - 1), 0).Rune;
        Assert.Equal('█', topCell);
    }

    [Fact]
    public void Scrollbar_ThumbAtBottom_WhenScrolledToBottom()
    {
        var (sv, _) = MakeScrollView(20, 20, 5);
        sv.AutoScroll = true; // auto-scroll pins to bottom on first Draw

        var screen = DrawScrollView(sv);

        // At the bottom the thumb should be in the last row(s).
        var bottomCell = screen.Get((ushort)(sv.Width - 1), (ushort)(sv.Height - 1)).Rune;
        Assert.Equal('█', bottomCell);
    }

    [Fact]
    public void Scrollbar_TrackRendered_WhenContentFits()
    {
        // When content fits in the viewport, only the track (│) should appear.
        var (sv, _) = MakeScrollView(2, 20, 10);

        var screen = DrawScrollView(sv);

        var scrollbarX = (ushort)(sv.Width - 1);
        for (ushort row = 0; row < sv.Height; row++)
        {
            var ch = screen.Get(scrollbarX, row).Rune;
            Assert.Equal('│', ch);
        }
    }

    // -------------------------------------------------------------------------
    // HandleInput
    // -------------------------------------------------------------------------

    [Fact]
    public void HandleInput_Up_DecreasesScrollOffset()
    {
        var (sv, _) = MakeScrollView(10, 20, 5);
        sv.AutoScroll = false;

        // Scroll down twice so there is room to scroll up.
        DrawScrollView(sv); // populate _contentHeight
        sv.HandleInput(new KeyEvent(Key.Down));
        sv.HandleInput(new KeyEvent(Key.Down));

        var screenBefore = DrawScrollView(sv);
        var topRowBefore = RowText(screenBefore, 0);

        sv.HandleInput(new KeyEvent(Key.Up));

        var screenAfter = DrawScrollView(sv);
        var topRowAfter = RowText(screenAfter, 0);

        // After pressing Up the top row should show an earlier item.
        Assert.NotEqual(topRowBefore, topRowAfter);
    }

    [Fact]
    public void HandleInput_Down_IncreasesScrollOffset()
    {
        var (sv, _) = MakeScrollView(10, 20, 5);
        sv.AutoScroll = false;

        DrawScrollView(sv); // populate _contentHeight

        var screenBefore = DrawScrollView(sv);
        var topRowBefore = RowText(screenBefore, 0);

        sv.HandleInput(new KeyEvent(Key.Down));

        var screenAfter = DrawScrollView(sv);
        var topRowAfter = RowText(screenAfter, 0);

        Assert.NotEqual(topRowBefore, topRowAfter);
    }

    [Fact]
    public void Draw_DoesNotThrow_WithEmptyContent()
    {
        var (sv, _) = MakeScrollView(0, 20, 5);

        var ex = Record.Exception(() => DrawScrollView(sv));
        Assert.Null(ex);
    }

    // -------------------------------------------------------------------------
    // Scrollbar thumb at middle position
    // -------------------------------------------------------------------------

    [Fact]
    public void Scrollbar_ThumbNotAtTopOrBottom_WhenScrolledToMiddle()
    {
        // 20 items in a 10-row viewport → maxScroll = 10.
        // Scroll to offset 5 (the middle). Thumb should be neither at row 0 nor row 9.
        var (sv, _) = MakeScrollView(20, 20, 10);
        sv.AutoScroll = false;

        DrawScrollView(sv); // populate _contentHeight

        // Scroll to the middle (5 out of 10 max)
        for (var i = 0; i < 5; i++)
            sv.HandleInput(new KeyEvent(Key.Down));

        var screen = DrawScrollView(sv);
        var scrollbarX = (ushort)(sv.Width - 1);

        // First and last rows should be track (│), not thumb (█), at mid-scroll.
        Assert.Equal('│', screen.Get(scrollbarX, 0).Rune);
        Assert.Equal('│', screen.Get(scrollbarX, (ushort)(sv.Height - 1)).Rune);

        // Some row in the middle must be a thumb cell.
        var hasMidThumb = Enumerable.Range(1, sv.Height - 2)
            .Any(row => screen.Get(scrollbarX, (ushort)row).Rune == '█');
        Assert.True(hasMidThumb);
    }

    // -------------------------------------------------------------------------
    // Content height == viewport height (exact fit boundary)
    // -------------------------------------------------------------------------

    [Fact]
    public void ContentExactlyFillsViewport_ShowsAllItemsNoScroll()
    {
        // 5 items, 5-row viewport — content fits exactly; no scrollbar thumb.
        var (sv, _) = MakeScrollView(5, 20, 5);

        var screen = DrawScrollView(sv);

        // All five items visible.
        for (var i = 0; i < 5; i++)
            Assert.Contains($"Item {i}", RowText(screen, i));

        // Scrollbar column should be all track (│) since no overflow.
        var scrollbarX = (ushort)(sv.Width - 1);
        for (ushort row = 0; row < sv.Height; row++)
            Assert.Equal('│', screen.Get(scrollbarX, row).Rune);
    }

    // -------------------------------------------------------------------------
    // Blit includes scrollbar column
    // -------------------------------------------------------------------------

    [Fact]
    public void Blit_ScrollbarColumnIsPresent_WhenContentOverflows()
    {
        // With overflowing content, the rightmost column should contain scrollbar
        // characters (either '█' or '│'), not spaces.
        var (sv, _) = MakeScrollView(20, 20, 5);
        sv.AutoScroll = true;

        var screen = DrawScrollView(sv);

        var scrollbarX = (ushort)(sv.Width - 1);
        var allScrollbarChars = Enumerable.Range(0, sv.Height)
            .Select(row => screen.Get(scrollbarX, (ushort)row).Rune)
            .ToList();

        Assert.All(allScrollbarChars, ch => Assert.True(ch == '█' || ch == '│',
            $"Expected '█' or '│' in scrollbar column, got '{ch}'"));
    }
}
