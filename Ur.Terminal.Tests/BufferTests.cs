using Ur.Terminal.Core;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Tests;

public class BufferTests
{
    [Fact]
    public void Set_WithinBounds_StoresCell()
    {
        var buf = new Buffer(10, 10);
        var cell = new Cell('A', Color.White, Color.Black);
        buf.Set(3, 4, cell);
        Assert.Equal(cell, buf.Get(3, 4));
    }

    [Fact]
    public void Set_OutOfBounds_SilentlyClips()
    {
        var buf = new Buffer(10, 10);
        var cell = new Cell('A', Color.White, Color.Black);
        buf.Set(20, 20, cell);
        Assert.True(buf.Get(20, 20).IsTransparent);
    }

    [Fact]
    public void Get_OutOfBounds_ReturnsTransparent()
    {
        var buf = new Buffer(10, 10);
        Assert.True(buf.Get(-1, -1).IsTransparent);
    }

    [Fact]
    public void Fill_SetsRegion()
    {
        var buf = new Buffer(10, 10);
        var cell = new Cell('X', Color.White, Color.Black);
        buf.Fill(new Rect(2, 2, 3, 3), cell);

        for (var y = 2; y < 5; y++)
        for (var x = 2; x < 5; x++)
            Assert.Equal(cell, buf.Get(x, y));

        Assert.True(buf.Get(0, 0).IsTransparent);
    }

    [Fact]
    public void WriteString_WritesChars()
    {
        var buf = new Buffer(20, 5);
        buf.WriteString(0, 0, "Hello", Color.White, Color.Black);

        Assert.Equal('H', buf.Get(0, 0).Char);
        Assert.Equal('e', buf.Get(1, 0).Char);
        Assert.Equal('l', buf.Get(2, 0).Char);
        Assert.Equal('l', buf.Get(3, 0).Char);
        Assert.Equal('o', buf.Get(4, 0).Char);
    }

    [Fact]
    public void WriteString_ClipsAtEdge()
    {
        var buf = new Buffer(3, 1);
        buf.WriteString(0, 0, "Hello", Color.White, Color.Black);

        Assert.Equal('H', buf.Get(0, 0).Char);
        Assert.Equal('e', buf.Get(1, 0).Char);
        Assert.Equal('l', buf.Get(2, 0).Char);
        Assert.True(buf.Get(3, 0).IsTransparent);
    }

    [Fact]
    public void DrawBox_DrawsBorder()
    {
        var buf = new Buffer(10, 5);
        buf.DrawBox(new Rect(0, 0, 5, 3), Color.White, Color.Black);

        Assert.Equal('┌', buf.Get(0, 0).Char);
        Assert.Equal('─', buf.Get(1, 0).Char);
        Assert.Equal('┐', buf.Get(4, 0).Char);
        Assert.Equal('│', buf.Get(0, 1).Char);
        Assert.Equal('│', buf.Get(4, 1).Char);
        Assert.Equal('└', buf.Get(0, 2).Char);
        Assert.Equal('─', buf.Get(1, 2).Char);
        Assert.Equal('┘', buf.Get(4, 2).Char);
    }

    [Fact]
    public void Clear_AllTransparent()
    {
        var buf = new Buffer(5, 5);
        buf.Set(2, 2, new Cell('A', Color.White, Color.Black));
        buf.Clear();

        for (var y = 0; y < 5; y++)
        for (var x = 0; x < 5; x++)
            Assert.True(buf.Get(x, y).IsTransparent);
    }
}
