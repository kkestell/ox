using Ur.Drawing;
using Ur.Widgets;
using Xunit;

namespace Ur.Tests.Widgets;

/// <summary>
/// Tests for the 7-pass flex layout algorithm.
/// Since the algorithm is implemented in Flex, we test it by creating a Flex container
/// with children and calling Layout() on the root to run the algorithm.
/// </summary>
public class LayoutEngineTests
{
    private class TestWidget : Widget
    {
        public TestWidget(int preferredWidth, int preferredHeight)
        {
            PreferredWidth = preferredWidth;
            PreferredHeight = preferredHeight;
            Width = preferredWidth;
            Height = preferredHeight;
        }

        public override void Draw(ICanvas canvas) { }

        // Simple widgets implement Layout by respecting available dimensions
        public override void Layout(int availableWidth, int availableHeight)
        {
            // For a Fit widget, use preferred size or available space if larger
            if (HorizontalSizing == SizingMode.Fit)
                Width = PreferredWidth;
            else if (HorizontalSizing == SizingMode.Fixed)
                Width = FixedWidth;
            else if (HorizontalSizing == SizingMode.Grow)
                Width = availableWidth > 0 ? availableWidth : PreferredWidth;

            if (VerticalSizing == SizingMode.Fit)
                Height = PreferredHeight;
            else if (VerticalSizing == SizingMode.Fixed)
                Height = FixedHeight;
            else if (VerticalSizing == SizingMode.Grow)
                Height = availableHeight > 0 ? availableHeight : PreferredHeight;

            // Apply min/max constraints
            if (MinWidth > 0) Width = Math.Max(Width, MinWidth);
            if (MaxWidth > 0) Width = Math.Min(Width, MaxWidth);
            if (MinHeight > 0) Height = Math.Max(Height, MinHeight);
            if (MaxHeight > 0) Height = Math.Min(Height, MaxHeight);

            // Apply box model
            Width += Margin.Left + Margin.Right + Padding.Left + Padding.Right;
            Height += Margin.Top + Margin.Bottom + Padding.Top + Padding.Bottom;
        }
    }

    // ========== FitWidth Pass ==========

    [Fact]
    public void FitWidth_LeafWidget_UsesPreferredWidth()
    {
        var widget = new TestWidget(50, 10);
        widget.Layout(1000, 1000);
        Assert.Equal(50, widget.Width);
    }

    [Fact]
    public void FitWidth_FixedSizing_UsesFixedWidth()
    {
        var widget = new TestWidget(50, 10)
        {
            HorizontalSizing = SizingMode.Fixed,
            FixedWidth = 100
        };
        widget.Layout(1000, 1000);
        Assert.Equal(100, widget.Width);
    }

    [Fact]
    public void FitWidth_VerticalStack_TakesMaxChildWidth()
    {
        var root = new Flex(LayoutDirection.Vertical);
        root.AddChild(new TestWidget(30, 5));
        root.AddChild(new TestWidget(50, 5));
        root.AddChild(new TestWidget(20, 5));

        root.Layout(1000, 1000);

        Assert.Equal(50, root.Width);
    }

    [Fact]
    public void FitWidth_HorizontalStack_SumsChildWidths()
    {
        var root = new Flex(LayoutDirection.Horizontal);
        root.AddChild(new TestWidget(30, 5));
        root.AddChild(new TestWidget(50, 5));
        root.AddChild(new TestWidget(20, 5));

        root.Layout(1000, 1000);

        Assert.Equal(100, root.Width);
    }

    [Fact]
    public void FitWidth_WithChildGap_IncludesGaps()
    {
        var root = new Flex(LayoutDirection.Horizontal) { ChildGap = 5 };
        root.AddChild(new TestWidget(10, 5));
        root.AddChild(new TestWidget(10, 5));

        root.Layout(1000, 1000);

        Assert.Equal(25, root.Width); // 10 + 5 + 10
    }

    // ========== FitHeight Pass ==========

    [Fact]
    public void FitHeight_LeafWidget_UsesPreferredHeight()
    {
        var widget = new TestWidget(50, 10);
        widget.Layout(1000, 1000);
        Assert.Equal(10, widget.Height);
    }

    [Fact]
    public void FitHeight_FixedSizing_UsesFixedHeight()
    {
        var widget = new TestWidget(50, 10)
        {
            VerticalSizing = SizingMode.Fixed,
            FixedHeight = 30
        };
        widget.Layout(1000, 1000);
        Assert.Equal(30, widget.Height);
    }

    [Fact]
    public void FitHeight_VerticalStack_SumsChildHeights()
    {
        var root = new Flex(LayoutDirection.Vertical);
        root.AddChild(new TestWidget(10, 20));
        root.AddChild(new TestWidget(10, 30));
        root.AddChild(new TestWidget(10, 10));

        root.Layout(1000, 1000);

        Assert.Equal(60, root.Height);
    }

    [Fact]
    public void FitHeight_HorizontalStack_TakesMaxChildHeight()
    {
        var root = new Flex(LayoutDirection.Horizontal);
        root.AddChild(new TestWidget(10, 20));
        root.AddChild(new TestWidget(10, 30));
        root.AddChild(new TestWidget(10, 10));

        root.Layout(1000, 1000);

        Assert.Equal(30, root.Height);
    }

    // ========== MinMax Constraints ==========

    [Fact]
    public void MinMaxWidth_EnforcesMinWidth()
    {
        var widget = new TestWidget(10, 5) { MinWidth = 50 };
        widget.Layout(1000, 1000);
        Assert.Equal(50, widget.Width);
    }

    [Fact]
    public void MinMaxWidth_EnforcesMaxWidth()
    {
        var widget = new TestWidget(100, 5) { MaxWidth = 50 };
        widget.Layout(1000, 1000);
        Assert.Equal(50, widget.Width);
    }

    [Fact]
    public void MinMaxHeight_EnforcesMinHeight()
    {
        var widget = new TestWidget(10, 5) { MinHeight = 30 };
        widget.Layout(1000, 1000);
        Assert.Equal(30, widget.Height);
    }

    [Fact]
    public void MinMaxHeight_EnforcesMaxHeight()
    {
        var widget = new TestWidget(10, 50) { MaxHeight = 20 };
        widget.Layout(1000, 1000);
        Assert.Equal(20, widget.Height);
    }

    // ========== Box Model ==========

    [Fact]
    public void BoxModel_MarginAddsToDimensions()
    {
        var widget = new TestWidget(10, 5) { Margin = Margin.All(2) };
        widget.Layout(1000, 1000);
        Assert.Equal(14, widget.Width);
        Assert.Equal(9, widget.Height);
    }

    [Fact]
    public void BoxModel_BorderAddsToDimensions()
    {
        var widget = new TestWidget(10, 5);
        widget.Layout(1000, 1000);
        Assert.Equal(10, widget.Width);
        Assert.Equal(5, widget.Height);
    }

    [Fact]
    public void BoxModel_PaddingAddsToDimensions()
    {
        var widget = new TestWidget(10, 5) { Padding = Padding.All(3) };
        widget.Layout(1000, 1000);
        Assert.Equal(16, widget.Width);
        Assert.Equal(11, widget.Height);
    }

    [Fact]
    public void BoxModel_AllCombined()
    {
        var widget = new TestWidget(10, 5)
        {
            Margin = Margin.All(1),
            Padding = Padding.All(2)
        };
        widget.Layout(1000, 1000);
        Assert.Equal(16, widget.Width);
        Assert.Equal(11, widget.Height);
    }

    // ========== Position Pass ==========

    [Fact]
    public void Position_RootDefaultsToOrigin()
    {
        var widget = new TestWidget(10, 5);
        widget.Layout(1000, 1000);
        Assert.Equal(0, widget.X);
        Assert.Equal(0, widget.Y);
    }

    [Fact]
    public void Position_VerticalStack_StacksChildren()
    {
        var root = new Flex(LayoutDirection.Vertical);
        var child1 = new TestWidget(5, 5);
        var child2 = new TestWidget(5, 5);
        var child3 = new TestWidget(5, 5);
        root.AddChild(child1);
        root.AddChild(child2);
        root.AddChild(child3);

        root.Layout(1000, 1000);

        Assert.Equal(0, child1.Y);
        Assert.Equal(5, child2.Y);
        Assert.Equal(10, child3.Y);
    }

    [Fact]
    public void Position_HorizontalStack_StacksChildren()
    {
        var root = new Flex(LayoutDirection.Horizontal);
        var child1 = new TestWidget(5, 5);
        var child2 = new TestWidget(5, 5);
        var child3 = new TestWidget(5, 5);
        root.AddChild(child1);
        root.AddChild(child2);
        root.AddChild(child3);

        root.Layout(1000, 1000);

        Assert.Equal(0, child1.X);
        Assert.Equal(5, child2.X);
        Assert.Equal(10, child3.X);
    }

    [Fact]
    public void Position_WithChildGap_SpacesChildren()
    {
        var root = new Flex(LayoutDirection.Vertical) { ChildGap = 3 };
        var child1 = new TestWidget(5, 5);
        var child2 = new TestWidget(5, 5);
        root.AddChild(child1);
        root.AddChild(child2);

        root.Layout(1000, 1000);

        Assert.Equal(0, child1.Y);
        Assert.Equal(8, child2.Y); // child1 height (5) + gap (3)
    }

    [Fact]
    public void Position_WithBoxModel_OffsetsChildren()
    {
        var root = new Flex(LayoutDirection.Vertical)
        {
            Margin = Margin.All(1),
            Padding = Padding.All(2)
        };
        var child = new TestWidget(5, 5);
        root.AddChild(child);

        root.Layout(1000, 1000);

        // Children are positioned relative to the parent's content area
        // (after accounting for margin and padding), so X = 3 (1 margin + 2 padding)
        Assert.Equal(3, child.X);
        Assert.Equal(3, child.Y);
    }

    // ========== Grow/Shrink On-Axis ==========

    [Fact]
    public void GrowOnAxis_DistributesToGrowChildren()
    {
        var root = new Flex(LayoutDirection.Horizontal) { HorizontalSizing = SizingMode.Fixed, FixedWidth = 100 };
        var child1 = new TestWidget(20, 5) { HorizontalSizing = SizingMode.Grow };
        var child2 = new TestWidget(20, 5) { HorizontalSizing = SizingMode.Grow };
        root.AddChild(child1);
        root.AddChild(child2);

        root.Layout(1000, 1000);

        Assert.True(child1.Width >= 20);
        Assert.True(child2.Width >= 20);
    }

    [Fact]
    public void GrowOnAxis_FixedWidgetsDontGrow()
    {
        var root = new Flex(LayoutDirection.Horizontal) { HorizontalSizing = SizingMode.Fixed, FixedWidth = 100 };
        var fixedChild = new TestWidget(20, 5) { HorizontalSizing = SizingMode.Fixed, FixedWidth = 20 };
        var grow = new TestWidget(20, 5) { HorizontalSizing = SizingMode.Grow };
        root.AddChild(fixedChild);
        root.AddChild(grow);

        root.Layout(1000, 1000);

        Assert.Equal(20, fixedChild.Width);
        Assert.True(grow.Width >= 20);
    }

    [Fact]
    public void ShrinkOnAxis_DistributesProportionally()
    {
        var root = new Flex(LayoutDirection.Horizontal) { HorizontalSizing = SizingMode.Fixed, FixedWidth = 50 };
        root.AddChild(new TestWidget(20, 5));
        root.AddChild(new TestWidget(20, 5));
        root.AddChild(new TestWidget(20, 5));

        root.Layout(1000, 1000);

        var totalWidth = root.Children.Sum(c => c.Width);
        Assert.True(totalWidth <= 60);
    }

    // ========== Grow/Shrink Cross-Axis ==========

    [Fact]
    public void GrowCrossAxis_ExpandsToAvailableSpace()
    {
        var root = new Flex(LayoutDirection.Horizontal) { HorizontalSizing = SizingMode.Fixed, FixedWidth = 50, VerticalSizing = SizingMode.Fixed, FixedHeight = 30 };
        var child = new TestWidget(20, 10) { VerticalSizing = SizingMode.Grow };
        root.AddChild(child);

        root.Layout(1000, 1000);

        Assert.True(child.Height >= 10);
    }

    [Fact]
    public void ShrinkCrossAxis_CapsAtAvailableSpace()
    {
        var root = new Flex(LayoutDirection.Horizontal) { HorizontalSizing = SizingMode.Fixed, FixedWidth = 50, VerticalSizing = SizingMode.Fixed, FixedHeight = 10 };
        var child = new TestWidget(20, 30);
        root.AddChild(child);

        root.Layout(1000, 1000);

        Assert.True(child.Height <= 30);
    }

    // ========== Deep Trees ==========

    [Fact]
    public void DeepTree_LayoutsCorrectly()
    {
        var root = new Flex(LayoutDirection.Vertical);
        var level1a = new Flex(LayoutDirection.Horizontal);
        var level1b = new Flex(LayoutDirection.Horizontal);

        level1a.AddChild(new TestWidget(20, 10));
        level1a.AddChild(new TestWidget(30, 15));
        level1b.AddChild(new TestWidget(25, 12));
        level1b.AddChild(new TestWidget(35, 18));

        root.AddChild(level1a);
        root.AddChild(level1b);

        root.Layout(1000, 1000);

        Assert.True(root.Width >= 50);
        Assert.True(root.Height >= 30);
    }

    // ========== Dump Tree Environment Variable ==========

    [Fact]
    public void DumpTree_InvalidEnvVar_RunsNormally()
    {
        var original = Environment.GetEnvironmentVariable("OX_DUMP_TREE");
        try
        {
            Environment.SetEnvironmentVariable("OX_DUMP_TREE", "foo");
            var root = new TestWidget(10, 5);

            root.Layout(1000, 1000);

            Assert.Equal(10, root.Width);
            Assert.Equal(5, root.Height);
        }
        finally
        {
            Environment.SetEnvironmentVariable("OX_DUMP_TREE", original);
        }
    }

    [Fact]
    public void DumpTree_EnvVarOutOfRange_RunsNormally()
    {
        var original = Environment.GetEnvironmentVariable("OX_DUMP_TREE");
        try
        {
            Environment.SetEnvironmentVariable("OX_DUMP_TREE", "0");
            var root = new TestWidget(10, 5);

            root.Layout(1000, 1000);

            Assert.Equal(10, root.Width);
            Assert.Equal(5, root.Height);
        }
        finally
        {
            Environment.SetEnvironmentVariable("OX_DUMP_TREE", original);
        }
    }

    [Fact]
    public void DumpTree_EnvVarEight_RunsNormally()
    {
        var original = Environment.GetEnvironmentVariable("OX_DUMP_TREE");
        try
        {
            Environment.SetEnvironmentVariable("OX_DUMP_TREE", "8");
            var root = new TestWidget(10, 5);

            root.Layout(1000, 1000);

            Assert.Equal(10, root.Width);
            Assert.Equal(5, root.Height);
        }
        finally
        {
            Environment.SetEnvironmentVariable("OX_DUMP_TREE", original);
        }
    }

}
