using Ox.App.Input;
using Ox.App.Views;
using Ox.Terminal.Rendering;

namespace Ox.Tests.App.Views;

/// <summary>
/// Regression tests for the Ox composer styling.
///
/// Terminal.Gui derives editable backgrounds from the normal foreground when a
/// scheme leaves the Editable role implicit. With Ox's white-on-black normal
/// colors that produces the gray editor surface reported in the bug. This test
/// locks in the Ox palette contract so the composer stays black-backed.
/// </summary>
public sealed class InputAreaViewTests
{
    [Fact]
    public void OxPalette_UsesBlackEditableBackground()
    {
        var palette = OxThemePalette.Ox;

        Assert.Equal(OxThemeColor.White, palette.EditableForeground);
        Assert.Equal(OxThemeColor.Black, palette.EditableBackground);
    }

    [Fact]
    public void ComposeStatusText_WhenPercentAndModelPresent_FormatsCombinedSummary()
    {
        var text = InputStatusFormatter.Compose(47, "google/gemini-3-flash-preview");

        Assert.Equal("47%  google/gemini-3-flash-preview", text);
    }

    [Fact]
    public void ComposeStatusText_WhenOnlyModelPresent_ReturnsModel()
    {
        var text = InputStatusFormatter.Compose(null, "openai/gpt-5-nano");

        Assert.Equal("openai/gpt-5-nano", text);
    }

    [Fact]
    public void ComposeStatusText_WhenNothingPresent_ReturnsNull()
    {
        var text = InputStatusFormatter.Compose(null, null);

        Assert.Null(text);
    }

    [Fact]
    public void Render_UsesSquareGrayFrameOnBlackWithoutShadow()
    {
        var palette = OxThemePalette.Ox;
        var buffer = new ConsoleBuffer(40, 10);
        var view = new InputAreaView();
        var editor = new TextEditor();
        editor.SetText("hello");

        view.Render(buffer, x: 1, y: 2, width: 20, editor, ghostText: null, statusRight: "model", throbber: null, isFocused: false);

        AssertCell(buffer, 1, 2, '┌', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 20, 2, '┐', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 1, 4, '├', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 20, 4, '┤', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 1, 6, '└', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 20, 6, '┘', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 3, 3, 'h', palette.Text, palette.Background);
        AssertCell(buffer, 2, 3, ' ', palette.Text, palette.Background);
        AssertCell(buffer, 19, 3, ' ', palette.Text, palette.Background);
        AssertCell(buffer, 14, 5, 'm', palette.StatusText, palette.Background);
        AssertCell(buffer, 18, 5, 'l', palette.StatusText, palette.Background);
        AssertCell(buffer, 19, 5, ' ', palette.Text, palette.Background);

        Assert.Equal(Cell.Empty, buffer.GetCell(21, 3));
        Assert.Equal(Cell.Empty, buffer.GetCell(2, 7));
    }

    [Fact]
    public void Render_PadsThrobberOneCellInFromLeftBorder()
    {
        var palette = OxThemePalette.Ox;
        var buffer = new ConsoleBuffer(40, 10);
        var view = new InputAreaView();
        var editor = new TextEditor();
        var throbber = new Throbber();
        throbber.Start();

        view.Render(buffer, x: 1, y: 2, width: 24, editor, ghostText: null, statusRight: null, throbber: throbber, isFocused: false);

        AssertCell(buffer, 2, 5, ' ', palette.Text, palette.Background);
        AssertCell(buffer, 3, 5, '●', palette.ThrobberInactive, palette.Background);
    }

    private static void AssertCell(ConsoleBuffer buffer, int x, int y, char rune, Color foreground, Color background)
    {
        var cell = buffer.GetCell(x, y);
        Assert.Equal(rune, cell.Rune);
        Assert.Equal(foreground, cell.Foreground);
        Assert.Equal(background, cell.Background);
    }
}
