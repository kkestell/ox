using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Tests;

public class WidgetTests
{
    /// <summary>Minimal concrete Widget for testing the base class behavior.</summary>
    private sealed class TestWidget : Widget
    {
        public Rect? LastContentRect { get; private set; }
        public int RenderContentCallCount { get; private set; }
        public int? ContentHeightToReport { get; set; }

        protected override void RenderContent(Buffer buffer, Rect area)
        {
            LastContentRect = area;
            RenderContentCallCount++;
        }

        public override bool HandleKey(KeyEvent key) => false;

        protected override int? MeasureContentHeight(int availableWidth) => ContentHeightToReport;
    }

    // --- ContentRect ---

    [Fact]
    public void ContentRect_NoBorderNoPadding_ReturnsFullArea()
    {
        var widget = new TestWidget();
        var outer = new Rect(5, 10, 40, 20);

        var inner = widget.ContentRect(outer);

        Assert.Equal(outer, inner);
    }

    [Fact]
    public void ContentRect_WithBorder_ShrinksByOne()
    {
        var widget = new TestWidget { Border = true };
        var outer = new Rect(0, 0, 20, 10);

        var inner = widget.ContentRect(outer);

        Assert.Equal(new Rect(1, 1, 18, 8), inner);
    }

    [Fact]
    public void ContentRect_WithPadding_ShrinksAndOffsets()
    {
        var widget = new TestWidget { Padding = new Thickness(2, 3, 4, 5) };
        var outer = new Rect(0, 0, 30, 20);

        var inner = widget.ContentRect(outer);

        Assert.Equal(5, inner.X);        // Left padding
        Assert.Equal(2, inner.Y);        // Top padding
        Assert.Equal(22, inner.Width);   // 30 - 5 - 3
        Assert.Equal(14, inner.Height);  // 20 - 2 - 4
    }

    [Fact]
    public void ContentRect_WithBorderAndPadding_CombinesBoth()
    {
        var widget = new TestWidget
        {
            Border = true,
            Padding = new Thickness(1, 1, 1, 1),
        };
        var outer = new Rect(0, 0, 20, 10);

        var inner = widget.ContentRect(outer);

        // Border: 1 each side. Padding: 1 each side. Total: 2 each side.
        Assert.Equal(new Rect(2, 2, 16, 6), inner);
    }

    [Fact]
    public void ContentRect_DegenerateRect_ClampsToZero()
    {
        var widget = new TestWidget { Border = true };
        var outer = new Rect(0, 0, 1, 1); // Too small for border

        var inner = widget.ContentRect(outer);

        Assert.Equal(0, inner.Width);
        Assert.Equal(0, inner.Height);
    }

    // --- Render chrome ---

    [Fact]
    public void Render_WithBackground_FillsEntireArea()
    {
        var bg = new Color(50, 50, 50);
        var widget = new TestWidget { Background = bg };
        var buffer = new Buffer(20, 10);
        var area = new Rect(2, 1, 10, 5);

        widget.Render(buffer, area);

        Assert.Equal(bg, buffer.Get(2, 1).Bg);
        Assert.Equal(bg, buffer.Get(11, 5).Bg);
    }

    [Fact]
    public void Render_WithBorder_DrawsBoxCorners()
    {
        var widget = new TestWidget { Border = true };
        var buffer = new Buffer(20, 10);
        var area = new Rect(0, 0, 10, 5);

        widget.Render(buffer, area);

        Assert.Equal('┌', buffer.Get(0, 0).Char);
        Assert.Equal('┐', buffer.Get(9, 0).Char);
        Assert.Equal('└', buffer.Get(0, 4).Char);
        Assert.Equal('┘', buffer.Get(9, 4).Char);
    }

    [Fact]
    public void Render_CallsRenderContent_WithInnerRect()
    {
        var widget = new TestWidget { Border = true };
        var buffer = new Buffer(20, 10);
        var area = new Rect(0, 0, 20, 10);

        widget.Render(buffer, area);

        Assert.Equal(1, widget.RenderContentCallCount);
        Assert.Equal(new Rect(1, 1, 18, 8), widget.LastContentRect);
    }

    [Fact]
    public void Render_DegenerateRect_SkipsRenderContent()
    {
        var widget = new TestWidget { Border = true };
        var buffer = new Buffer(10, 10);
        var area = new Rect(0, 0, 1, 1);

        widget.Render(buffer, area);

        Assert.Equal(0, widget.RenderContentCallCount);
    }

    // --- MeasureHeight ---

    [Fact]
    public void MeasureHeight_NoOverride_ReturnsNull()
    {
        var widget = new TestWidget();

        Assert.Null(widget.MeasureHeight(40));
    }

    [Fact]
    public void MeasureHeight_WithContentHeight_ReturnsContentOnly()
    {
        var widget = new TestWidget { ContentHeightToReport = 3 };

        Assert.Equal(3, widget.MeasureHeight(40));
    }

    [Fact]
    public void MeasureHeight_WithBorder_AddsTwo()
    {
        var widget = new TestWidget
        {
            Border = true,
            ContentHeightToReport = 3,
        };

        Assert.Equal(5, widget.MeasureHeight(40));
    }

    [Fact]
    public void MeasureHeight_WithBorderAndPadding_AddsBoth()
    {
        var widget = new TestWidget
        {
            Border = true,
            Padding = new Thickness(2, 0, 2, 0),
            ContentHeightToReport = 3,
        };

        // 3 content + 2 border + 4 padding = 9
        Assert.Equal(9, widget.MeasureHeight(40));
    }
}
