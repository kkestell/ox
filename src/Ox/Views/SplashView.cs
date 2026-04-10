using Terminal.Gui.Drawing;
using Terminal.Gui.ViewBase;
using Attribute = Terminal.Gui.Drawing.Attribute;
using Color = Terminal.Gui.Drawing.Color;

namespace Ox.Views;

/// <summary>
/// Displays the "OX" ASCII art splash centered in the viewport.
/// Shown when the conversation is empty; hidden once the first
/// message arrives.
/// </summary>
internal sealed class SplashView : View
{
    private static readonly Color Bg = new(ColorName16.Black);

    private static readonly string[] SplashLines =
    [
        "▒█▀▀▀█ ▀▄▒▄▀",
        "▒█░░▒█ ░▒█░░",
        "▒█▄▄▄█ ▄▀▒▀▄"
    ];

    public SplashView()
    {
        CanFocus = false;
    }

    /// <inheritdoc/>
    protected override bool OnDrawingContent(DrawContext? context)
    {
        var width = Viewport.Width;
        var height = Viewport.Height;
        if (width <= 0 || height <= 0)
            return true;

        var artWidth = SplashLines.Max(l => l.Length);
        var startRow = Math.Max(0, (height - SplashLines.Length) / 2);
        var startCol = Math.Max(0, (width - artWidth) / 2);

        for (var i = 0; i < SplashLines.Length; i++)
        {
            Move(startCol, startRow + i);
            SetAttribute(new Attribute(new Color(ColorName16.DarkGray), Bg));
            AddStr(SplashLines[i]);
        }

        return true;
    }
}
