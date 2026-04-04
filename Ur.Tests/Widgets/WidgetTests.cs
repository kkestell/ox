using Ur.Drawing;
using Ur.Widgets;
using Xunit;

namespace Ur.Tests.Widgets;

public class LabelTests
{
    [Fact]
    public void Constructor_SetsTextAndCalculatesSize()
    {
        var label = new Label("Hello");
        Assert.Equal("Hello", label.Text);
        Assert.Equal(5, label.PreferredWidth);
        Assert.Equal(1, label.PreferredHeight);
    }

    [Fact]
    public void Constructor_WithMultilineText_CalculatesCorrectSize()
    {
        var label = new Label("Line1\nLine2\nLine3");
        Assert.Equal(5, label.PreferredWidth); // Length of longest line
        Assert.Equal(3, label.PreferredHeight);
    }

    [Fact]
    public void Constructor_WithEmptyText_HasZeroSize()
    {
        var label = new Label("");
        Assert.Equal(0, label.PreferredWidth);
        Assert.Equal(1, label.PreferredHeight);
    }

    [Fact]
    public void Lines_SplitsOnNewlines()
    {
        var label = new Label("A\nB\nC");
        Assert.Equal(3, label.Lines.Length);
        Assert.Equal("A", label.Lines[0]);
        Assert.Equal("B", label.Lines[1]);
        Assert.Equal("C", label.Lines[2]);
    }

    [Fact]
    public void DefaultSizing_IsFit()
    {
        var label = new Label("Test");
        Assert.Equal(SizingMode.Fit, label.HorizontalSizing);
        Assert.Equal(SizingMode.Fit, label.VerticalSizing);
    }

    [Fact]
    public void CanSetSizingMode()
    {
        var label = new Label("Test")
        {
            HorizontalSizing = SizingMode.Grow,
            VerticalSizing = SizingMode.Fixed,
            FixedHeight = 5
        };
        Assert.Equal(SizingMode.Grow, label.HorizontalSizing);
        Assert.Equal(SizingMode.Fixed, label.VerticalSizing);
        Assert.Equal(5, label.FixedHeight);
    }

    [Fact]
    public void CanSetBoxModel()
    {
        var label = new Label("Test")
        {
            Margin = Margin.All(1),
            Padding = Padding.All(2)
        };
        Assert.Equal(Margin.All(1), label.Margin);
        Assert.Equal(Padding.All(2), label.Padding);
    }

    [Fact]
    public void Draw_DoesNotThrow()
    {
        var screen = new Screen(10, 5);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var label = new Label("Hi");

        var exception = Record.Exception(() => label.Draw(canvas));
        Assert.Null(exception);
    }
}

public class FlexTests
{
    [Fact]
    public void Vertical_CreatesVerticalFlex()
    {
        var flex = Flex.Vertical();
        Assert.Equal(LayoutDirection.Vertical, flex.Direction);
    }

    [Fact]
    public void Horizontal_CreatesHorizontalFlex()
    {
        var flex = Flex.Horizontal();
        Assert.Equal(LayoutDirection.Horizontal, flex.Direction);
    }

    [Fact]
    public void Constructor_DefaultsToVertical()
    {
        var flex = new Flex();
        Assert.Equal(LayoutDirection.Vertical, flex.Direction);
    }

    [Fact]
    public void Constructor_CanSetDirection()
    {
        var flex = new Flex(LayoutDirection.Horizontal);
        Assert.Equal(LayoutDirection.Horizontal, flex.Direction);
    }

    [Fact]
    public void AddChild_SetsParent()
    {
        var flex = new Flex();
        var label = new Label("Test");
        flex.AddChild(label);
        Assert.Equal(flex, label.Parent);
        Assert.Single(flex.Children);
    }

    [Fact]
    public void RemoveChild_ClearsParent()
    {
        var flex = new Flex();
        var label = new Label("Test");
        flex.AddChild(label);
        flex.RemoveChild(label);
        Assert.Null(label.Parent);
        Assert.Empty(flex.Children);
    }

    [Fact]
    public void MultipleChildren_AreAddedInOrder()
    {
        var flex = new Flex();
        var label1 = new Label("A");
        var label2 = new Label("B");
        var label3 = new Label("C");
        flex.AddChild(label1);
        flex.AddChild(label2);
        flex.AddChild(label3);

        Assert.Equal(3, flex.Children.Count);
        Assert.Equal(label1, flex.Children[0]);
        Assert.Equal(label2, flex.Children[1]);
        Assert.Equal(label3, flex.Children[2]);
    }

    [Fact]
    public void CanSetChildGap()
    {
        var flex = new Flex { ChildGap = 5 };
        Assert.Equal(5, flex.ChildGap);
    }

    [Fact]
    public void CanSetBoxModel()
    {
        var flex = new Flex
        {
            Margin = Margin.All(1),
            Padding = Padding.All(2)
        };
        Assert.Equal(Margin.All(1), flex.Margin);
        Assert.Equal(Padding.All(2), flex.Padding);
    }

    [Fact]
    public void Draw_DoesNotThrow()
    {
        var screen = new Screen(10, 5);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var flex = new Flex();

        var exception = Record.Exception(() => flex.Draw(canvas));
        Assert.Null(exception);
    }
}

public class WidgetTests
{
    private class TestWidget : Widget
    {
        public TestWidget(int preferredWidth, int preferredHeight)
        {
            PreferredWidth = preferredWidth;
            PreferredHeight = preferredHeight;
        }

        public override void Draw(ICanvas canvas) { }
    }

    [Fact]
    public void AddChild_ThrowsOnNull()
    {
        var widget = new TestWidget(10, 10);
        Assert.Throws<ArgumentNullException>(() => widget.AddChild(null!));
    }

    [Fact]
    public void RemoveChild_NotFound_DoesNothing()
    {
        var widget = new TestWidget(10, 10);
        var child = new TestWidget(5, 5);
        widget.RemoveChild(child); // No exception
        Assert.Empty(widget.Children);
    }

    [Fact]
    public void Parent_IsNullByDefault()
    {
        var widget = new TestWidget(10, 10);
        Assert.Null(widget.Parent);
    }

    [Fact]
    public void DefaultProperties_AreCorrect()
    {
        var widget = new TestWidget(10, 10);
        Assert.Equal(LayoutDirection.Vertical, widget.Direction);
        Assert.Equal(SizingMode.Fit, widget.HorizontalSizing);
        Assert.Equal(SizingMode.Fit, widget.VerticalSizing);
        Assert.Equal(Margin.None, widget.Margin);
        Assert.Equal(Padding.None, widget.Padding);
        Assert.False(widget.Focusable);
        Assert.False(widget.IsFocused);
    }

    [Fact]
    public void CanSetAllProperties()
    {
        var widget = new TestWidget(10, 10)
        {
            X = 5,
            Y = 10,
            Width = 100,
            Height = 50,
            Direction = LayoutDirection.Horizontal,
            HorizontalSizing = SizingMode.Grow,
            VerticalSizing = SizingMode.Fixed,
            FixedWidth = 80,
            FixedHeight = 24,
            MinWidth = 10,
            MaxWidth = 200,
            MinHeight = 5,
            MaxHeight = 100,
            ChildGap = 2,
            Margin = Margin.All(1),
            Padding = Padding.All(2),
            Focusable = true,
            IsFocused = true
        };

        Assert.Equal(5, widget.X);
        Assert.Equal(10, widget.Y);
        Assert.Equal(100, widget.Width);
        Assert.Equal(50, widget.Height);
        Assert.Equal(LayoutDirection.Horizontal, widget.Direction);
        Assert.Equal(SizingMode.Grow, widget.HorizontalSizing);
        Assert.Equal(SizingMode.Fixed, widget.VerticalSizing);
        Assert.Equal(80, widget.FixedWidth);
        Assert.Equal(24, widget.FixedHeight);
        Assert.Equal(10, widget.MinWidth);
        Assert.Equal(200, widget.MaxWidth);
        Assert.Equal(5, widget.MinHeight);
        Assert.Equal(100, widget.MaxHeight);
        Assert.Equal(2, widget.ChildGap);
        Assert.Equal(Margin.All(1), widget.Margin);
        Assert.Equal(Padding.All(2), widget.Padding);
        Assert.True(widget.Focusable);
        Assert.True(widget.IsFocused);
    }

    [Fact]
    public void NestedTree_ParentsAreCorrect()
    {
        var root = new TestWidget(1, 1);
        var child1 = new TestWidget(1, 1);
        var child2 = new TestWidget(1, 1);
        var grandchild = new TestWidget(1, 1);

        root.AddChild(child1);
        root.AddChild(child2);
        child1.AddChild(grandchild);

        Assert.Null(root.Parent);
        Assert.Equal(root, child1.Parent);
        Assert.Equal(root, child2.Parent);
        Assert.Equal(child1, grandchild.Parent);
    }

    [Fact]
    public void ReaddingChild_UpdatesParent()
    {
        var parent1 = new TestWidget(1, 1);
        var parent2 = new TestWidget(1, 1);
        var child = new TestWidget(1, 1);

        parent1.AddChild(child);
        Assert.Equal(parent1, child.Parent);

        parent2.AddChild(child);
        Assert.Equal(parent2, child.Parent); // Parent is updated to parent2
    }

}

public class BoxModelTests
{
    [Fact]
    public void Margin_All_CreatesUniformMargin()
    {
        var margin = Margin.All(5);
        Assert.Equal((ushort)5, margin.Top);
        Assert.Equal((ushort)5, margin.Right);
        Assert.Equal((ushort)5, margin.Bottom);
        Assert.Equal((ushort)5, margin.Left);
    }

    [Fact]
    public void Margin_None_IsAllZeros()
    {
        var margin = Margin.None;
        Assert.Equal((ushort)0, margin.Top);
        Assert.Equal((ushort)0, margin.Right);
        Assert.Equal((ushort)0, margin.Bottom);
        Assert.Equal((ushort)0, margin.Left);
    }

    [Fact]
    public void Margin_RecordEquality()
    {
        var m1 = new Margin(1, 2, 3, 4);
        var m2 = new Margin(1, 2, 3, 4);
        var m3 = new Margin(1, 2, 3, 5);
        Assert.Equal(m1, m2);
        Assert.NotEqual(m1, m3);
    }

    [Fact]
    public void Border_All_CreatesUniformBorder()
    {
        var border = Border.All(true);
        Assert.True(border.Top);
        Assert.True(border.Right);
        Assert.True(border.Bottom);
        Assert.True(border.Left);
    }

    [Fact]
    public void Border_None_IsAllFalse()
    {
        var border = Border.None;
        Assert.False(border.Top);
        Assert.False(border.Right);
        Assert.False(border.Bottom);
        Assert.False(border.Left);
    }

    [Fact]
    public void Border_RecordEquality()
    {
        var b1 = new Border(true, false, true, false);
        var b2 = new Border(true, false, true, false);
        var b3 = new Border(true, true, true, false);
        Assert.Equal(b1, b2);
        Assert.NotEqual(b1, b3);
    }

    [Fact]
    public void Padding_All_CreatesUniformPadding()
    {
        var padding = Padding.All(3);
        Assert.Equal((ushort)3, padding.Top);
        Assert.Equal((ushort)3, padding.Right);
        Assert.Equal((ushort)3, padding.Bottom);
        Assert.Equal((ushort)3, padding.Left);
    }

    [Fact]
    public void Padding_None_IsAllZeros()
    {
        var padding = Padding.None;
        Assert.Equal((ushort)0, padding.Top);
        Assert.Equal((ushort)0, padding.Right);
        Assert.Equal((ushort)0, padding.Bottom);
        Assert.Equal((ushort)0, padding.Left);
    }

    [Fact]
    public void Padding_RecordEquality()
    {
        var p1 = new Padding(1, 2, 3, 4);
        var p2 = new Padding(1, 2, 3, 4);
        var p3 = new Padding(1, 2, 3, 5);
        Assert.Equal(p1, p2);
        Assert.NotEqual(p1, p3);
    }
}
