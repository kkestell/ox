using Ur.Tui.Util;

namespace Ur.Tui.Tests;

public class WordWrapTests
{
    [Fact]
    public void ShortLine_NoWrap()
    {
        var result = WordWrap.Wrap("hello", 20);

        Assert.Single(result);
        Assert.Equal("hello", result[0]);
    }

    [Fact]
    public void LongLine_WrapsAtSpace()
    {
        var result = WordWrap.Wrap("hello world today", 11);

        Assert.Equal(2, result.Count);
        Assert.Equal("hello world", result[0]);
        Assert.Equal("today", result[1]);
    }

    [Fact]
    public void LongWord_HardBreaks()
    {
        var result = WordWrap.Wrap("abcdefghij", 5);

        Assert.Equal(2, result.Count);
        Assert.Equal("abcde", result[0]);
        Assert.Equal("fghij", result[1]);
    }

    [Fact]
    public void EmptyString_ReturnsSingleEmptyLine()
    {
        var result = WordWrap.Wrap("", 20);

        Assert.Single(result);
        Assert.Equal("", result[0]);
    }

    [Fact]
    public void ExactWidth_NoWrap()
    {
        var result = WordWrap.Wrap("12345", 5);

        Assert.Single(result);
        Assert.Equal("12345", result[0]);
    }

    [Fact]
    public void MultipleSpaces_WrapsCorrectly()
    {
        var result = WordWrap.Wrap("aaa bbb ccc ddd", 7);

        Assert.Equal(2, result.Count);
        Assert.Equal("aaa bbb", result[0]);
        Assert.Equal("ccc ddd", result[1]);
    }
}
