using Ur.Terminal.Core;

namespace Ur.Terminal.Tests;

public class ThicknessTests
{
    [Fact]
    public void Constructor_SetsAllFourSides()
    {
        var t = new Thickness(1, 2, 3, 4);

        Assert.Equal(1, t.Top);
        Assert.Equal(2, t.Right);
        Assert.Equal(3, t.Bottom);
        Assert.Equal(4, t.Left);
    }

    [Fact]
    public void Uniform_SetsAllSidesEqual()
    {
        var t = Thickness.Uniform(5);

        Assert.Equal(5, t.Top);
        Assert.Equal(5, t.Right);
        Assert.Equal(5, t.Bottom);
        Assert.Equal(5, t.Left);
    }

    [Fact]
    public void Zero_IsAllZeros()
    {
        var t = Thickness.Zero;

        Assert.Equal(0, t.Top);
        Assert.Equal(0, t.Right);
        Assert.Equal(0, t.Bottom);
        Assert.Equal(0, t.Left);
    }

    [Fact]
    public void Horizontal_ReturnsSumOfLeftAndRight()
    {
        var t = new Thickness(1, 3, 1, 7);

        Assert.Equal(10, t.Horizontal);
    }

    [Fact]
    public void Vertical_ReturnsSumOfTopAndBottom()
    {
        var t = new Thickness(2, 1, 5, 1);

        Assert.Equal(7, t.Vertical);
    }

    [Fact]
    public void RecordEquality_WorksCorrectly()
    {
        var a = new Thickness(1, 2, 3, 4);
        var b = new Thickness(1, 2, 3, 4);
        var c = new Thickness(4, 3, 2, 1);

        Assert.Equal(a, b);
        Assert.NotEqual(a, c);
    }
}
