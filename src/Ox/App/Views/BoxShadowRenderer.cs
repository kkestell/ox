using Ox.Terminal.Rendering;

namespace Ox.App.Views;

/// <summary>
/// Draws the offset shadow treatment used by Ox floating chrome.
///
/// The shadow is intentionally lightweight: a one-column vertical cast made of
/// full blocks and a one-row bottom cast made of top-half blocks. That gives
/// boxes visual lift in plain text dumps and in terminals whose font rendering
/// makes shaded block characters read more crisply than dim box-drawing lines.
/// </summary>
internal static class BoxShadowRenderer
{
    private const char VerticalShadowRune = '█';
    private const char BottomShadowRune = '▀';

    /// <summary>
    /// Draw a shadow one column to the right and one row below the box.
    /// <paramref name="verticalInset"/> lets callers start the right-hand cast
    /// below the box's top edge when the chrome looks better without a cap.
    /// The helper clips naturally when the box is flush against the terminal edge.
    /// </summary>
    public static void Render(
        ConsoleBuffer buffer,
        int x,
        int y,
        int width,
        int height,
        Color shadowColor,
        int verticalInset = 0)
    {
        if (width <= 0 || height <= 0)
            return;

        var shadowColumn = x + width;
        if (shadowColumn < buffer.Width)
        {
            for (var row = y + verticalInset; row < y + height && row < buffer.Height; row++)
            {
                if (row < 0)
                    continue;

                buffer.SetCell(shadowColumn, row, VerticalShadowRune, shadowColor, Color.Default);
            }
        }

        var shadowRow = y + height;
        if (shadowRow < buffer.Height)
        {
            for (var column = x + 1; column <= x + width && column < buffer.Width; column++)
            {
                if (column < 0)
                    continue;

                buffer.SetCell(column, shadowRow, BottomShadowRune, shadowColor, Color.Default);
            }
        }
    }
}
