using Ox.Permission;
using Ox.Views;
using Te.Rendering;
using Ur.Permissions;

namespace Ox.Tests.Permission;

/// <summary>
/// View-level checks for the inline permission prompt chrome.
/// These assertions lock in the monochrome box treatment so approval requests
/// keep the same high-contrast framing as the composer.
/// </summary>
public sealed class PermissionPromptViewTests
{
    [Fact]
    public void Render_UsesSquareGrayFrameOnBlackWithoutShadow()
    {
        var palette = OxThemePalette.Ox;
        var buffer = new ConsoleBuffer(40, 8);
        var view = new PermissionPromptView
        {
            ActiveRequest = new PermissionRequest(
                OperationType.Write,
                "/home/kyle/src/ox/foo.txt",
                "write_file",
                [PermissionScope.Once, PermissionScope.Session]),
            WorkspacePath = "/home/kyle/src/ox",
        };
        view.Editor.SetText("s");

        view.Render(buffer, x: 2, y: 1, width: 30);

        AssertCell(buffer, 2, 1, '┌', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 31, 1, '┐', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 2, 3, '└', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 31, 3, '┘', palette.ChromeBorder, palette.Background);
        AssertCell(buffer, 3, 2, ' ', palette.Text, palette.Background);

        Assert.Equal(Cell.Empty, buffer.GetCell(32, 2));
        Assert.Equal(Cell.Empty, buffer.GetCell(3, 4));
    }

    private static void AssertCell(ConsoleBuffer buffer, int x, int y, char rune, Color foreground, Color background)
    {
        var cell = buffer.GetCell(x, y);
        Assert.Equal(rune, cell.Rune);
        Assert.Equal(foreground, cell.Foreground);
        Assert.Equal(background, cell.Background);
    }
}
