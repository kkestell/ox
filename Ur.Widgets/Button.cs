using Ur.Console;
using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A clickable button widget. Renders its label wrapped in square brackets ([Label])
/// and fires a Clicked event when activated.
///
/// Buttons are focusable and activated by pressing Enter when focused.
/// The background highlights when focused so the user can see which button
/// is currently selected in the focus ring.
/// </summary>
public class Button : Widget
{
    private string _text;

    // Distinct background colors mirror TextInput's focus treatment: bright when
    // active, grey when reachable but inactive.
    private static readonly Style FocusedStyle   = new(Color.Black,      Color.BrightWhite);
    private static readonly Style UnfocusedStyle = new(Color.BrightWhite, Color.BrightBlack);

    /// <summary>
    /// Fired when the user activates the button by pressing Enter while focused.
    /// </summary>
    public event Action? Clicked;

    public Button(string text)
    {
        _text = text ?? "";
        Focusable = true;

        // Width = brackets + label; height is always one row.
        PreferredWidth  = _text.Length + 2; // "[" + text + "]"
        PreferredHeight = 1;
        MinHeight       = 1;
    }

    /// <summary>
    /// The button label (without brackets). Updating this recalculates preferred
    /// width so the layout engine can resize the button on the next pass.
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
    /// Buttons size to their label by default and never stretch vertically.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        Width  = availableWidth > 0 ? availableWidth : PreferredWidth;
        Height = 1;
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
    /// Renders "[Text]" with the full width filled so the background color
    /// extends edge-to-edge, making the focused state clearly visible.
    /// </summary>
    public override void Draw(ICanvas canvas)
    {
        var style = IsFocused ? FocusedStyle : UnfocusedStyle;

        // Fill background across the whole cell so focus highlight covers padding.
        canvas.DrawHLine(0, 0, Width, ' ', style);
        canvas.DrawText(0, 0, $"[{_text}]", style);
    }
}
