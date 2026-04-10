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
}
