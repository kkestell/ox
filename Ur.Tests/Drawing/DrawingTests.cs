using Ur.Drawing;
using Xunit;

namespace Ur.Tests.Drawing;

public class RectTests
{
    [Fact]
    public void Create_SetsAllProperties()
    {
        var rect = new Rect(10, 20, 100, 50);
        Assert.Equal((ushort)10, rect.X);
        Assert.Equal((ushort)20, rect.Y);
        Assert.Equal((ushort)100, rect.Width);
        Assert.Equal((ushort)50, rect.Height);
    }

    [Fact]
    public void Right_ReturnsXPlusWidth()
    {
        var rect = new Rect(10, 20, 100, 50);
        Assert.Equal((ushort)110, rect.Right);
    }

    [Fact]
    public void Bottom_ReturnsYPlusHeight()
    {
        var rect = new Rect(10, 20, 100, 50);
        Assert.Equal((ushort)70, rect.Bottom);
    }

    [Fact]
    public void Contains_ReturnsTrueForPointInsideRect()
    {
        var rect = new Rect(10, 20, 100, 50);
        Assert.True(rect.Contains(10, 20));
        Assert.True(rect.Contains(50, 30));
        Assert.True(rect.Contains(109, 69));
    }

    [Fact]
    public void Contains_ReturnsFalseForPointOutsideRect()
    {
        var rect = new Rect(10, 20, 100, 50);
        Assert.False(rect.Contains(9, 20));
        Assert.False(rect.Contains(110, 20));
        Assert.False(rect.Contains(10, 19));
        Assert.False(rect.Contains(10, 70));
    }

    [Fact]
    public void Create_FactoryMethodWorks()
    {
        var rect = Rect.Create(1, 2, 3, 4);
        Assert.Equal((ushort)1, rect.X);
        Assert.Equal((ushort)2, rect.Y);
        Assert.Equal((ushort)3, rect.Width);
        Assert.Equal((ushort)4, rect.Height);
    }
}

public class ColorTests
{
    [Fact]
    public void PredefinedColors_HaveCorrectValues()
    {
        Assert.Equal(0x000000u, Color.Black.Value);
        Assert.Equal(0x800000u, Color.Red.Value);
        Assert.Equal(0x008000u, Color.Green.Value);
        Assert.Equal(0x808000u, Color.Yellow.Value);
        Assert.Equal(0x000080u, Color.Blue.Value);
        Assert.Equal(0xFFFFFFu, Color.BrightWhite.Value);
    }

    [Fact]
    public void FromRgb_CreatesCorrectColor()
    {
        var color = Color.FromRgb(255, 128, 64);
        Assert.Equal(0xFF8040u, color.Value);
    }

    [Fact]
    public void Components_ExtractsRgbValues()
    {
        var color = Color.FromRgb(255, 128, 64);
        var (r, g, b) = color.Components;
        Assert.Equal(255, r);
        Assert.Equal(128, g);
        Assert.Equal(64, b);
    }
}

public class StyleTests
{
    [Fact]
    public void Default_HasWhiteForegroundBlackBackground()
    {
        var style = Style.Default;
        Assert.Equal(Color.White, style.Fg);
        Assert.Equal(Color.Black, style.Bg);
        Assert.Equal(Modifier.None, style.Modifiers);
    }

    [Fact]
    public void Create_WithModifiers_SetsAllProperties()
    {
        var style = new Style(Color.Red, Color.Blue, Modifier.Bold | Modifier.Underline);
        Assert.Equal(Color.Red, style.Fg);
        Assert.Equal(Color.Blue, style.Bg);
        Assert.Equal(Modifier.Bold | Modifier.Underline, style.Modifiers);
    }
}

public class CellTests
{
    [Fact]
    public void Create_SetsRuneAndStyle()
    {
        var style = new Style(Color.Red, Color.Blue);
        var cell = Cell.Create('A', style);
        Assert.Equal('A', cell.Rune);
        Assert.Equal(style, cell.Style);
    }

    [Fact]
    public void Default_IsSpaceWithDefaultStyle()
    {
        var cell = Cell.Default;
        Assert.Equal(' ', cell.Rune);
        Assert.Equal(Color.White, cell.Style.Fg);
        Assert.Equal(Color.Black, cell.Style.Bg);
    }

    [Fact]
    public void RecordEquality_WorksCorrectly()
    {
        var style = new Style(Color.Red, Color.Blue);
        var cell1 = new Cell('A', style);
        var cell2 = new Cell('A', style);
        var cell3 = new Cell('B', style);

        Assert.Equal(cell1, cell2);
        Assert.NotEqual(cell1, cell3);
    }
}

public class ScreenTests
{
    [Fact]
    public void Create_InitializesAllCellsToDefault()
    {
        var screen = new Screen(10, 5);
        Assert.Equal(10, screen.Width);
        Assert.Equal(5, screen.Height);

        for (ushort y = 0; y < 5; y++)
        {
            for (ushort x = 0; x < 10; x++)
            {
                var cell = screen.Get(x, y);
                Assert.Equal(' ', cell.Rune);
            }
        }
    }

    [Fact]
    public void SetAndGet_RoundTrip()
    {
        var screen = new Screen(10, 5);
        var style = new Style(Color.Red, Color.Blue);
        screen.Set(5, 2, Cell.Create('X', style));

        var cell = screen.Get(5, 2);
        Assert.Equal('X', cell.Rune);
        Assert.Equal(Color.Red, cell.Style.Fg);
    }

    [Fact]
    public void Set_OutOfBounds_DoesNotThrow()
    {
        var screen = new Screen(10, 5);
        var style = new Style(Color.Red, Color.Blue);

        screen.Set(100, 2, Cell.Create('X', style));
        screen.Set(5, 100, Cell.Create('X', style));

        var cell = screen.Get(5, 2);
        Assert.Equal(' ', cell.Rune);
    }

    [Fact]
    public void Clear_ResetsAllCells()
    {
        var screen = new Screen(10, 5);
        var style = new Style(Color.Red, Color.Blue);

        for (ushort y = 0; y < 5; y++)
        {
            for (ushort x = 0; x < 10; x++)
            {
                screen.Set(x, y, Cell.Create('X', style));
            }
        }

        screen.Clear();

        for (ushort y = 0; y < 5; y++)
        {
            for (ushort x = 0; x < 10; x++)
            {
                var cell = screen.Get(x, y);
                Assert.Equal(' ', cell.Rune);
            }
        }
    }
}

public class CanvasTests
{
    [Fact]
    public void CreateCanvas_HasCorrectBounds()
    {
        var screen = new Screen(100, 50);
        var canvas = CanvasFactory.CreateCanvas(screen);

        Assert.Equal((ushort)0, canvas.Bounds.X);
        Assert.Equal((ushort)0, canvas.Bounds.Y);
        Assert.Equal((ushort)100, canvas.Bounds.Width);
        Assert.Equal((ushort)50, canvas.Bounds.Height);
    }

    [Fact]
    public void SubCanvas_HasOffsetBounds()
    {
        var screen = new Screen(100, 50);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var subRect = new Rect(10, 5, 20, 10);
        var subCanvas = canvas.SubCanvas(subRect);

        Assert.Equal((ushort)10, subCanvas.Bounds.X);
        Assert.Equal((ushort)5, subCanvas.Bounds.Y);
        Assert.Equal((ushort)20, subCanvas.Bounds.Width);
        Assert.Equal((ushort)10, subCanvas.Bounds.Height);
    }

    [Fact]
    public void SetCell_WritesToScreenAtCorrectPosition()
    {
        var screen = new Screen(100, 50);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var style = new Style(Color.Red, Color.Blue);

        canvas.SetCell(10, 5, 'X', style);

        var cell = screen.Get(10, 5);
        Assert.Equal('X', cell.Rune);
        Assert.Equal(Color.Red, cell.Style.Fg);
    }

    [Fact]
    public void SetCell_OutOfBounds_DoesNotThrow()
    {
        var screen = new Screen(100, 50);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var style = new Style(Color.Red, Color.Blue);

        canvas.SetCell(200, 5, 'X', style);
        canvas.SetCell(10, 100, 'X', style);

        var cell = screen.Get(200, 5);
        Assert.Equal(' ', cell.Rune);
    }

    [Fact]
    public void SubCanvas_SetCell_WritesAtOffsetPosition()
    {
        var screen = new Screen(100, 50);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var subCanvas = canvas.SubCanvas(new Rect(10, 5, 20, 10));
        var style = new Style(Color.Red, Color.Blue);

        subCanvas.SetCell(0, 0, 'X', style);

        var cell = screen.Get(10, 5);
        Assert.Equal('X', cell.Rune);
    }

    [Fact]
    public void DrawText_WritesTextAtPosition()
    {
        var screen = new Screen(100, 50);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var style = new Style(Color.Red, Color.Blue);

        canvas.DrawText(10, 5, "Hello", style);

        Assert.Equal('H', screen.Get(10, 5).Rune);
        Assert.Equal('e', screen.Get(11, 5).Rune);
        Assert.Equal('l', screen.Get(12, 5).Rune);
        Assert.Equal('l', screen.Get(13, 5).Rune);
        Assert.Equal('o', screen.Get(14, 5).Rune);
    }

    [Fact]
    public void DrawText_WithNewline_ContinuesOnNextLine()
    {
        var screen = new Screen(100, 50);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var style = new Style(Color.Red, Color.Blue);

        canvas.DrawText(10, 5, "A\nB", style);

        Assert.Equal('A', screen.Get(10, 5).Rune);
        Assert.Equal('B', screen.Get(10, 6).Rune);
    }

    [Fact]
    public void DrawBorder_DrawsBoxCharacters()
    {
        var screen = new Screen(100, 50);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var style = new Style(Color.Red, Color.Blue);
        var border = BorderSet.Single;

        canvas.DrawBorder(new Rect(10, 5, 4, 3), style, border);

        Assert.Equal('┌', screen.Get(10, 5).Rune);
        Assert.Equal('─', screen.Get(11, 5).Rune);
        Assert.Equal('┐', screen.Get(13, 5).Rune);
        Assert.Equal('│', screen.Get(10, 6).Rune);
        Assert.Equal('│', screen.Get(13, 6).Rune);
        Assert.Equal('└', screen.Get(10, 7).Rune);
        Assert.Equal('┘', screen.Get(13, 7).Rune);
    }

    [Fact]
    public void Clear_FillsWithSpaces()
    {
        var screen = new Screen(10, 5);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var style = new Style(Color.Red, Color.Blue);

        for (ushort y = 0; y < 5; y++)
        {
            for (ushort x = 0; x < 10; x++)
            {
                canvas.SetCell(x, y, 'X', style);
            }
        }

        canvas.Clear(Style.Default);

        for (ushort y = 0; y < 5; y++)
        {
            for (ushort x = 0; x < 10; x++)
            {
                Assert.Equal(' ', screen.Get(x, y).Rune);
            }
        }
    }

    [Fact]
    public void PushClip_PopClip_ClipsDrawing()
    {
        var screen = new Screen(100, 50);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var style = new Style(Color.Red, Color.Blue);

        var clipRect = new Rect(10, 10, 5, 5);
        canvas.PushClip(clipRect);

        canvas.SetCell(9, 10, 'X', style);
        canvas.SetCell(10, 10, 'Y', style);

        canvas.PopClip();

        Assert.Equal(' ', screen.Get(9, 10).Rune);
        Assert.Equal('Y', screen.Get(10, 10).Rune);
    }
}
