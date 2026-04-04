using Ur.Drawing;
using Ur.Widgets;
using Xunit;

namespace Ur.Tests.Widgets;

public class RendererTests
{
    [Fact]
    public void Render_SingleLabel_DrawsTextAtCorrectPosition()
    {
        var label = new Label("Hi")
        {
            X = 0, Y = 0,
            Width = 2, Height = 1,
            Style = Style.Default,
        };

        var screen = Renderer.Render(label);

        Assert.Equal('H', screen.Get(0, 0).Rune);
        Assert.Equal('i', screen.Get(1, 0).Rune);
    }

    [Fact]
    public void Render_LabelAtOffset_DrawsTextAtCorrectScreenPosition()
    {
        // A child widget with non-zero X/Y should draw at the right screen coords.
        var root = Flex.Vertical();
        root.X = 0;
        root.Y = 0;
        root.Width = 10;
        root.Height = 5;

        var label = new Label("AB") { Style = Style.Default };
        label.X = 3;
        label.Y = 2;
        label.Width = 2;
        label.Height = 1;
        root.AddChild(label);

        var screen = Renderer.Render(root);

        // (3,2) and (4,2) should contain the text; (0,0) should be blank
        Assert.Equal('A', screen.Get(3, 2).Rune);
        Assert.Equal('B', screen.Get(4, 2).Rune);
        Assert.Equal(' ', screen.Get(0, 0).Rune);
    }

    [Fact]
    public void Render_ScreenDimensionsMatchRoot()
    {
        var root = new Label("Hello");
        root.X = 0; root.Y = 0;
        root.Width = 10; root.Height = 3;

        var screen = Renderer.Render(root);

        Assert.Equal(10, screen.Width);
        Assert.Equal(3, screen.Height);
    }

    [Fact]
    public void Render_ChildrenAreRecursed()
    {
        var root = Flex.Vertical();
        root.X = 0; root.Y = 0;
        root.Width = 20; root.Height = 5;

        var child1 = new Label("AA") { Style = Style.Default };
        child1.X = 0; child1.Y = 0; child1.Width = 2; child1.Height = 1;

        var child2 = new Label("BB") { Style = Style.Default };
        child2.X = 0; child2.Y = 1; child2.Width = 2; child2.Height = 1;

        root.AddChild(child1);
        root.AddChild(child2);

        var screen = Renderer.Render(root);

        Assert.Equal('A', screen.Get(0, 0).Rune);
        Assert.Equal('A', screen.Get(1, 0).Rune);
        Assert.Equal('B', screen.Get(0, 1).Rune);
        Assert.Equal('B', screen.Get(1, 1).Rune);
    }

    [Fact]
    public void Render_ZeroSizeWidget_IsSkipped()
    {
        var root = new Label("X");
        root.X = 0; root.Y = 0;
        root.Width = 5; root.Height = 1;

        var zero = new Label("Z") { Style = Style.Default };
        zero.X = 1; zero.Y = 0;
        zero.Width = 0; zero.Height = 0;
        root.AddChild(zero);

        // Should not throw; zero-size child is silently skipped
        var screen = Renderer.Render(root);
        Assert.Equal('X', screen.Get(0, 0).Rune);
    }

    [Fact]
    public void Render_GrandchildIsRenderedAtAbsolutePosition()
    {
        // Widget X/Y are parent-relative coordinates set by the layout engine.
        // The Renderer accumulates them as it descends the tree, so the grandchild's
        // absolute screen position is (parent.X + grandchild.X, parent.Y + grandchild.Y).
        var root = Flex.Vertical();
        root.X = 0; root.Y = 0; root.Width = 20; root.Height = 10;

        var parent = Flex.Vertical();
        parent.X = 2; parent.Y = 3; parent.Width = 15; parent.Height = 5;
        root.AddChild(parent);

        var grandchild = new Label("GC") { Style = Style.Default };
        // Parent-relative position (2, 2) → absolute screen position (2+2, 3+2) = (4, 5).
        grandchild.X = 2; grandchild.Y = 2; grandchild.Width = 2; grandchild.Height = 1;
        parent.AddChild(grandchild);

        var screen = Renderer.Render(root);

        Assert.Equal('G', screen.Get(4, 5).Rune);
        Assert.Equal('C', screen.Get(5, 5).Rune);
    }

    [Fact]
    public void Render_StyleIsAppliedToCell()
    {
        var style = new Style(Color.BrightRed, Color.Blue);
        var label = new Label("X") { Style = style };
        label.X = 0; label.Y = 0; label.Width = 1; label.Height = 1;

        var screen = Renderer.Render(label);

        var cell = screen.Get(0, 0);
        Assert.Equal(Color.BrightRed, cell.Style.Fg);
        Assert.Equal(Color.Blue, cell.Style.Bg);
    }
}
