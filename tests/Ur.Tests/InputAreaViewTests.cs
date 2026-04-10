using Ox.Views;

namespace Ur.Tests;

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
}
