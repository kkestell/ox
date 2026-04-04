using Ur.Console;
using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A clickable button widget rendered as a solid filled block with a subtle
/// highlight on the top edge.
///
/// Visual structure (3 rows):
///   ▇▇▇▇▇▇▇   ← lower-7/8 blocks: fg = button color, bg = lighter highlight
///   █LABEL█   ← full blocks + label text on button-colored background
///   ███████   ← full blocks in button color
///
/// The lower-7/8 block (▇) on the top row leaves a 1/8-cell gap that exposes
/// the lighter background color, creating a soft top-edge highlight without
/// needing a separate border character. The result is a chunky, solid button
/// with a subtle 3D feel.
///
/// Buttons are focusable and activated by pressing Enter when focused.
/// </summary>
public class Button : Widget
{
    private string _text;

    // Block characters used to build the solid button shape.
    private const char LowerSevenEighths = '\u2587'; // ▇
    private const char FullBlock         = '\u2588'; // █

    // Button fill color and a slightly lighter variant for the top-edge highlight.
    private static readonly Color ButtonColor    = Color.FromRgb(50, 100, 200);
    private static readonly Color HighlightColor = Color.FromRgb(80, 135, 235);

    // Focused buttons use a brighter shade so the user can see which button is
    // currently selected in the focus ring.
    private static readonly Color FocusedButtonColor    = Color.FromRgb(70, 130, 240);
    private static readonly Color FocusedHighlightColor = Color.FromRgb(110, 165, 255);

    /// <summary>
    /// Fired when the user activates the button by pressing Enter while focused.
    /// </summary>
    public event Action? Clicked;

    public Button(string text)
    {
        _text = text ?? "";
        Focusable = true;

        // Width = padding + label + padding; height is always 3 rows.
        PreferredWidth  = _text.Length + 2;
        PreferredHeight = 3;
        MinHeight       = 3;
    }

    /// <summary>
    /// The button label. Updating this recalculates preferred width so the
    /// layout engine can resize the button on the next pass.
    /// </summary>
    public string Text
    {
        get => _text;
        set
        {
            _text = value ?? "";
            PreferredWidth = _text.Length + 2;
        }
    }

    /// <summary>
    /// Buttons size to their label by default. Height is always 3 rows.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        Width  = availableWidth > 0 ? availableWidth : PreferredWidth;
        Height = 3;
    }

    /// <summary>
    /// Enter activates the button. All other keys are ignored so they fall
    /// through to the application's Tab / Ctrl-C handling.
    /// </summary>
    public override void HandleInput(InputEvent input)
    {
        if (input is KeyEvent { Key: Key.Enter })
            Clicked?.Invoke();
    }

    /// <summary>
    /// Draws the button as a solid block with a highlight strip on the top row.
    ///
    /// Row 0: lower-7/8 blocks — fg is the button color (fills most of the cell),
    ///        bg is the lighter highlight (peeks through the 1/8 gap at the top).
    /// Row 1: label text on a solid button-colored background, padded with full
    ///        blocks on either side.
    /// Row 2: full blocks in the button color for a solid bottom edge.
    /// </summary>
    public override void Draw(ICanvas canvas)
    {
        var btnColor = IsFocused ? FocusedButtonColor : ButtonColor;
        var hlColor  = IsFocused ? FocusedHighlightColor : HighlightColor;

        // Row 0: lower-7/8 blocks create the top highlight edge.
        // The fg fills most of the cell; the bg shows through the thin gap at top.
        var topStyle = new Style(btnColor, hlColor);
        canvas.DrawHLine(0, 0, Width, LowerSevenEighths, topStyle);

        // Row 1: solid background for the label row. Full blocks on the sides,
        // label text centered with white foreground on the button color.
        var blockStyle = new Style(btnColor, Color.Black);
        canvas.DrawHLine(0, 1, Width, FullBlock, blockStyle);
        var textStyle = new Style(Color.BrightWhite, btnColor);
        var textX = Math.Max(0, (Width - _text.Length) / 2);
        canvas.DrawText(textX, 1, _text, textStyle);

        // Row 2: solid full blocks for the bottom edge.
        canvas.DrawHLine(0, 2, Width, FullBlock, blockStyle);
    }
}
