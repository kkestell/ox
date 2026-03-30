using Ur.Terminal.Core;

namespace Ur.Terminal.Tests;

public class RectTests
{
    [Fact]
    public void Contains_InsidePoint_ReturnsTrue()
    {
        var rect = new Rect(5, 5, 10, 10);
        Assert.True(rect.Contains(7, 7));
    }

    [Fact]
    public void Contains_OutsidePoint_ReturnsFalse()
    {
        var rect = new Rect(5, 5, 10, 10);
        Assert.False(rect.Contains(20, 20));
    }

    [Fact]
    public void Intersect_Overlapping_ReturnsOverlap()
    {
        var a = new Rect(0, 0, 10, 10);
        var b = new Rect(5, 5, 10, 10);
        var result = a.Intersect(b);
        Assert.Equal(new Rect(5, 5, 5, 5), result);
    }

    [Fact]
    public void Intersect_NonOverlapping_ReturnsEmpty()
    {
        var a = new Rect(0, 0, 5, 5);
        var b = new Rect(10, 10, 5, 5);
        var result = a.Intersect(b);
        Assert.Equal(0, result.Width);
        Assert.Equal(0, result.Height);
    }
}
