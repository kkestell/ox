using System.Buffers;
using System.Text;
using Ur.Terminal.Core;
using Ur.Terminal.Rendering;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Tests;

public class ScreenTests
{
    [Fact]
    public void WriteDiff_UnchangedBuffer_NoOutput()
    {
        var buf = new Buffer(5, 5);
        buf.Fill(new Rect(0, 0, 5, 5), new Cell('A', Color.White, Color.Black));
        var output = new ArrayBufferWriter<byte>();

        Screen.WriteDiff(buf, buf, output);

        Assert.Equal(0, output.WrittenCount);
    }

    [Fact]
    public void WriteDiff_SingleCellChange_EmitsPositionAndCell()
    {
        var prev = new Buffer(5, 5);
        prev.Fill(new Rect(0, 0, 5, 5), new Cell(' ', Color.White, Color.Black));
        var cur = new Buffer(5, 5);
        cur.Fill(new Rect(0, 0, 5, 5), new Cell(' ', Color.White, Color.Black));
        cur.Set(2, 1, new Cell('X', Color.White, Color.Black));
        var output = new ArrayBufferWriter<byte>();

        Screen.WriteDiff(cur, prev, output);

        var text = Encoding.UTF8.GetString(output.WrittenSpan);
        Assert.Contains("\x1b[2;3H", text);
        Assert.Contains("X", text);
    }

    [Fact]
    public void WriteDiff_AdjacentChanges_SkipsCursorMove()
    {
        var prev = new Buffer(5, 1);
        prev.Fill(new Rect(0, 0, 5, 1), new Cell(' ', Color.White, Color.Black));
        var cur = new Buffer(5, 1);
        cur.Fill(new Rect(0, 0, 5, 1), new Cell(' ', Color.White, Color.Black));
        cur.Set(0, 0, new Cell('A', Color.White, Color.Black));
        cur.Set(1, 0, new Cell('B', Color.White, Color.Black));
        var output = new ArrayBufferWriter<byte>();

        Screen.WriteDiff(cur, prev, output);

        var text = Encoding.UTF8.GetString(output.WrittenSpan);
        var firstPos = text.IndexOf("\x1b[1;1H");
        Assert.True(firstPos >= 0);
        var afterFirst = text[(firstPos + 6)..];
        Assert.DoesNotContain("\x1b[1;2H", afterFirst);
        Assert.Contains("A", text);
        Assert.Contains("B", text);
    }

    [Fact]
    public void WriteDiff_SameColorAsLast_SkipsSGR()
    {
        var prev = new Buffer(5, 1);
        var cur = new Buffer(5, 1);
        var color = new Color(100, 150, 200);
        cur.Set(0, 0, new Cell('A', color, Color.Black));
        cur.Set(1, 0, new Cell('B', color, Color.Black));
        var output = new ArrayBufferWriter<byte>();

        Screen.WriteDiff(cur, prev, output);

        var text = Encoding.UTF8.GetString(output.WrittenSpan);
        var fgSeq = $"\x1b[38;2;{color.R};{color.G};{color.B}m";
        var first = text.IndexOf(fgSeq);
        var second = text.IndexOf(fgSeq, first + 1);
        Assert.True(first >= 0);
        Assert.Equal(-1, second);
    }

    [Fact]
    public void WriteDiff_DifferentColors_EmitsNewSGR()
    {
        var prev = new Buffer(5, 1);
        var cur = new Buffer(5, 1);
        var color1 = new Color(100, 0, 0);
        var color2 = new Color(0, 100, 0);
        cur.Set(0, 0, new Cell('A', color1, Color.Black));
        cur.Set(1, 0, new Cell('B', color2, Color.Black));
        var output = new ArrayBufferWriter<byte>();

        Screen.WriteDiff(cur, prev, output);

        var text = Encoding.UTF8.GetString(output.WrittenSpan);
        Assert.Contains($"\x1b[38;2;{color1.R};{color1.G};{color1.B}m", text);
        Assert.Contains($"\x1b[38;2;{color2.R};{color2.G};{color2.B}m", text);
    }

    [Fact]
    public void WriteDiff_TrueColorFormat()
    {
        var prev = new Buffer(1, 1);
        var cur = new Buffer(1, 1);
        var fg = new Color(10, 20, 30);
        var bg = new Color(40, 50, 60);
        cur.Set(0, 0, new Cell('Z', fg, bg));
        var output = new ArrayBufferWriter<byte>();

        Screen.WriteDiff(cur, prev, output);

        var text = Encoding.UTF8.GetString(output.WrittenSpan);
        Assert.Contains("\x1b[38;2;10;20;30m", text);
        Assert.Contains("\x1b[48;2;40;50;60m", text);
    }

    [Fact]
    public void WriteFullFrame_EmitsAllCells()
    {
        var buf = new Buffer(3, 1);
        buf.Set(0, 0, new Cell('A', Color.White, Color.Black));
        buf.Set(1, 0, new Cell('B', Color.White, Color.Black));
        buf.Set(2, 0, new Cell('C', Color.White, Color.Black));
        var output = new ArrayBufferWriter<byte>();

        Screen.WriteFullFrame(buf, output);

        var text = Encoding.UTF8.GetString(output.WrittenSpan);
        Assert.Contains("A", text);
        Assert.Contains("B", text);
        Assert.Contains("C", text);
    }
}
