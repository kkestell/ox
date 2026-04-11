using System.IO;
using System.Reflection;
using Te.Demo;
using Te.Rendering;

namespace Te.Tests;

public sealed class ConsoleBufferTests
{
    [Fact]
    public void Render_WritesOnlyDirtyCells()
    {
        var buffer = new ConsoleBuffer(3, 2);
        buffer.SetCell(0, 0, 'A', Color.White, Color.Default);

        var initialWriter = new StringWriter();
        buffer.Render(initialWriter);

        var initialOutput = initialWriter.ToString();
        Assert.Contains("\u001b[1;1H", initialOutput);
        Assert.Contains("A", initialOutput);
        Assert.Equal(0, buffer.GetDirtyCellCount());

        var cleanWriter = new StringWriter();
        buffer.Render(cleanWriter);
        Assert.Equal(string.Empty, cleanWriter.ToString());

        buffer.SetCell(1, 0, 'B', Color.Green, Color.Default);

        var updateWriter = new StringWriter();
        buffer.Render(updateWriter);

        var updateOutput = updateWriter.ToString();
        Assert.Contains("\u001b[1;2H", updateOutput);
        Assert.DoesNotContain("\u001b[1;1H", updateOutput);
        Assert.Contains("B", updateOutput);
        Assert.Equal(new Cell('B', Color.Green, Color.Default), buffer.GetRenderedCell(1, 0));
    }

    [Fact]
    public void Clear_MarksPreviousContentDirty()
    {
        var buffer = new ConsoleBuffer(2, 1);
        buffer.SetCell(0, 0, 'X', Color.Red, Color.Default);
        buffer.Render(new StringWriter());

        buffer.Clear();
        Assert.Equal(1, buffer.GetDirtyCellCount());

        var writer = new StringWriter();
        buffer.Render(writer);

        Assert.Contains("\u001b[1;1H", writer.ToString());
        Assert.Equal(Cell.Empty, buffer.GetRenderedCell(0, 0));
    }

    [Fact]
    public void DemoFrame_DoesNotLetRecentEventsOverwriteStatusRows()
    {
        var buffer = new ConsoleBuffer(40, 16);
        var stateType = typeof(Te.Demo.Program).GetNestedType("DemoState", BindingFlags.NonPublic);
        Assert.NotNull(stateType);

        var state = Activator.CreateInstance(stateType!, nonPublic: true);
        Assert.NotNull(state);

        var recordEvent = stateType!.GetMethod("RecordEvent", BindingFlags.Instance | BindingFlags.Public);
        Assert.NotNull(recordEvent);

        foreach (var value in new[] { "A", "S", "D", "F", "A", "S" })
            recordEvent!.Invoke(state, [value]);

        var drawFrame = typeof(Te.Demo.Program).GetMethod("DrawFrame", BindingFlags.Static | BindingFlags.NonPublic);
        Assert.NotNull(drawFrame);
        drawFrame!.Invoke(null, [buffer, state!]);

        Assert.Equal('s', buffer.GetCell(4, 13).Rune);
        Assert.Equal('c', buffer.GetCell(4, 14).Rune);
    }
}
