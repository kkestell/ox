using Ox.Terminal.Rendering;

namespace Ox.Tests.Terminal;

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

    /// <summary>
    /// Bug: A freshly constructed buffer had identical front and back buffers
    /// (both Cell.Empty), so the first Render() saw zero dirty cells and wrote
    /// nothing — the screen was never painted. The fix leaves the front buffer
    /// as the C# default struct (char '\0'), guaranteeing every cell is dirty
    /// on the first frame.
    /// </summary>
    [Fact]
    public void NewBuffer_AllCellsDirtyOnFirstFrame()
    {
        var buffer = new ConsoleBuffer(3, 2);

        // All 6 cells should be dirty because the front buffer (default struct)
        // doesn't match the back buffer (Cell.Empty).
        Assert.Equal(6, buffer.GetDirtyCellCount());
    }

    /// <summary>
    /// Bug: After Resize(), both buffers were reinitialized to Cell.Empty, so
    /// the diff-based renderer saw no changes and wrote nothing — leaving the
    /// old (now-corrupted) screen content in place. The fix ensures the front
    /// buffer is invalidated on resize, forcing a full redraw.
    /// </summary>
    [Fact]
    public void Resize_InvalidatesFrontBuffer_AllCellsDirty()
    {
        var buffer = new ConsoleBuffer(3, 2);
        buffer.Render(new StringWriter()); // Sync front and back.

        buffer.Resize(4, 3);

        // All 12 cells in the new 4×3 buffer should be dirty.
        Assert.Equal(12, buffer.GetDirtyCellCount());
    }

    /// <summary>
    /// Bug: Cells with Color.Default background emit SGR 49 ("use terminal's
    /// configured default"), which is whatever the user set in their terminal
    /// preferences — not necessarily black. DefaultBackgroundOverride replaces
    /// Color.Default backgrounds with an explicit color at render time so the
    /// application controls the background regardless of terminal settings.
    /// </summary>
    [Fact]
    public void DefaultBackgroundOverride_ReplacesDefaultInSgrOutput()
    {
        var buffer = new ConsoleBuffer(1, 1);
        buffer.DefaultBackgroundOverride = Color.Black;
        buffer.SetCell(0, 0, 'A', Color.White, Color.Default);

        var writer = new StringWriter();
        buffer.Render(writer);
        var output = writer.ToString();

        // SGR 40 = explicit black background; SGR 49 = terminal default.
        // With the override, we should see 40 and never 49.
        Assert.Contains(";40", output);
        Assert.DoesNotContain(";49", output);
    }

}
