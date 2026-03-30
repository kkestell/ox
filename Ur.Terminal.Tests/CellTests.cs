using Ur.Terminal.Core;

namespace Ur.Terminal.Tests;

public class CellTests
{
    [Fact]
    public void Transparent_IsTransparent()
    {
        Assert.True(Cell.Transparent.IsTransparent);
    }

    [Fact]
    public void WithContent_IsNotTransparent()
    {
        var cell = new Cell('A', Color.White, Color.Black);
        Assert.False(cell.IsTransparent);
    }
}
