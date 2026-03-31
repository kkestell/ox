using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Terminal.Layout;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Tests;

public class VerticalStackTests
{
    /// <summary>Test widget that records the rect it was rendered into.</summary>
    private sealed class SpyWidget : Widget
    {
        public Rect? RenderedRect { get; private set; }
        public int? ContentHeightToReport { get; set; }

        protected override void RenderContent(Buffer buffer, Rect area)
        {
            RenderedRect = area;
        }

        public override bool HandleKey(KeyEvent key) => false;

        protected override int? MeasureContentHeight(int availableWidth) => ContentHeightToReport;
    }

    [Fact]
    public void SingleFill_TakesAllSpace()
    {
        var child = new SpyWidget();
        var stack = new VerticalStack(
            new VerticalStack.Entry(child, new SizeConstraint.Fill()));
        var buffer = new Buffer(40, 20);

        stack.Render(buffer, new Rect(0, 0, 40, 20));

        Assert.Equal(new Rect(0, 0, 40, 20), child.RenderedRect);
    }

    [Fact]
    public void FixedAndFill_FixedClaimsSpace()
    {
        var fixedChild = new SpyWidget();
        var fillChild = new SpyWidget();
        var stack = new VerticalStack(
            new VerticalStack.Entry(fixedChild, new SizeConstraint.Fixed(3)),
            new VerticalStack.Entry(fillChild, new SizeConstraint.Fill()));
        var buffer = new Buffer(40, 20);

        stack.Render(buffer, new Rect(0, 0, 40, 20));

        Assert.Equal(new Rect(0, 0, 40, 3), fixedChild.RenderedRect);
        Assert.Equal(new Rect(0, 3, 40, 17), fillChild.RenderedRect);
    }

    [Fact]
    public void ContentAndFill_ContentMeasured()
    {
        var contentChild = new SpyWidget { ContentHeightToReport = 5 };
        var fillChild = new SpyWidget();
        var stack = new VerticalStack(
            new VerticalStack.Entry(fillChild, new SizeConstraint.Fill()),
            new VerticalStack.Entry(contentChild, new SizeConstraint.Content()));
        var buffer = new Buffer(40, 20);

        stack.Render(buffer, new Rect(0, 0, 40, 20));

        // Content child measures to 5, fill gets the remaining 15.
        Assert.Equal(new Rect(0, 0, 40, 15), fillChild.RenderedRect);
        Assert.Equal(new Rect(0, 15, 40, 5), contentChild.RenderedRect);
    }

    [Fact]
    public void MultipleFills_SplitByWeight()
    {
        var fill1 = new SpyWidget();
        var fill2 = new SpyWidget();
        var stack = new VerticalStack(
            new VerticalStack.Entry(fill1, new SizeConstraint.Fill(1)),
            new VerticalStack.Entry(fill2, new SizeConstraint.Fill(3)));
        var buffer = new Buffer(40, 20);

        stack.Render(buffer, new Rect(0, 0, 40, 20));

        // 1:3 ratio of 20 = 5 and 15.
        Assert.Equal(new Rect(0, 0, 40, 5), fill1.RenderedRect);
        Assert.Equal(new Rect(0, 5, 40, 15), fill2.RenderedRect);
    }

    [Fact]
    public void MultipleFills_RoundingRemainderGoesToLastFill()
    {
        var fill1 = new SpyWidget();
        var fill2 = new SpyWidget();
        var stack = new VerticalStack(
            new VerticalStack.Entry(fill1, new SizeConstraint.Fill(1)),
            new VerticalStack.Entry(fill2, new SizeConstraint.Fill(1)));
        var buffer = new Buffer(40, 21);

        stack.Render(buffer, new Rect(0, 0, 40, 21));

        // 21 / 2 = 10 each, 1 remainder to last fill.
        Assert.Equal(new Rect(0, 0, 40, 10), fill1.RenderedRect);
        Assert.Equal(new Rect(0, 10, 40, 11), fill2.RenderedRect);
    }

    [Fact]
    public void Degenerate_ClaimedExceedsAvailable_Clamps()
    {
        var fixed1 = new SpyWidget();
        var fixed2 = new SpyWidget();
        var fillChild = new SpyWidget();
        var stack = new VerticalStack(
            new VerticalStack.Entry(fixed1, new SizeConstraint.Fixed(8)),
            new VerticalStack.Entry(fixed2, new SizeConstraint.Fixed(8)),
            new VerticalStack.Entry(fillChild, new SizeConstraint.Fill()));
        var buffer = new Buffer(40, 10);

        stack.Render(buffer, new Rect(0, 0, 40, 10));

        // 8 + 8 = 16, but only 10 available. Fill gets 0.
        // Clamping shrinks from the end: fill (0→0), fixed2 (8→4).
        Assert.Equal(new Rect(0, 0, 40, 8), fixed1.RenderedRect);
        Assert.Equal(new Rect(0, 8, 40, 2), fixed2.RenderedRect);
        Assert.Null(fillChild.RenderedRect); // 0 height, skipped
    }

    [Fact]
    public void HandleKey_AlwaysReturnsFalse()
    {
        var stack = new VerticalStack();

        Assert.False(stack.HandleKey(new KeyEvent(Key.Up, Modifiers.None, null)));
    }

    [Fact]
    public void ContentChild_NullMeasure_TreatedAsZero()
    {
        var contentChild = new SpyWidget(); // ContentHeightToReport defaults to null
        var fillChild = new SpyWidget();
        var stack = new VerticalStack(
            new VerticalStack.Entry(contentChild, new SizeConstraint.Content()),
            new VerticalStack.Entry(fillChild, new SizeConstraint.Fill()));
        var buffer = new Buffer(40, 20);

        stack.Render(buffer, new Rect(0, 0, 40, 20));

        // Content child measures null → 0, fill gets all 20.
        Assert.Null(contentChild.RenderedRect); // 0 height, skipped
        Assert.Equal(new Rect(0, 0, 40, 20), fillChild.RenderedRect);
    }
}
