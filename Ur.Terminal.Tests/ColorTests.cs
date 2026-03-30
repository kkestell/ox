using Ur.Terminal.Core;

namespace Ur.Terminal.Tests;

public class ColorTests
{
    [Fact]
    public void Dim_HalvesChannels()
    {
        var color = new Color(100, 200, 50);
        var dimmed = color.Dim(0.5f);
        Assert.Equal(new Color(50, 100, 25), dimmed);
    }

    [Fact]
    public void Dim_ClampsToZero()
    {
        var color = new Color(100, 200, 50);
        var dimmed = color.Dim(0f);
        Assert.Equal(new Color(0, 0, 0), dimmed);
    }

    [Fact]
    public void Dim_ClampsToMax()
    {
        var color = new Color(100, 200, 50);
        var dimmed = color.Dim(1f);
        Assert.Equal(new Color(100, 200, 50), dimmed);
    }
}
