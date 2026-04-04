using Ur.Console;
using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A single-line text input widget that accepts typed characters when focused.
/// Rendered as a solid filled block with half-block edges on top and bottom,
/// using distinct colors for focused vs. unfocused states so the user can
/// always identify input fields.
///
/// Visual structure (3 rows):
///   ▄▄▄▄▄▄▄▄▄▄   ← lower-half blocks: fg = input color, bg = highlight
///   █ value  █   ← full blocks + value text on input-colored background
///   ▀▀▀▀▀▀▀▀▀▀   ← upper-half blocks: fg = input color, bg = highlight
///
/// The half blocks leave a half-cell gap on the outer edges that exposes the
/// lighter highlight color, creating a soft frame effect without borders.
///
/// Labels (prompts like "Name:") are separate Label widgets — TextInput only
/// handles the editable value.
/// </summary>
public class TextInput : Widget
{
    private string _value = "";
    private int _cursorPos;

    // Block characters used to build the solid input shape.
    private const char LowerHalfBlock = '\u2584'; // ▄
    private const char UpperHalfBlock = '\u2580'; // ▀
    private const char FullBlock      = '\u2588'; // █

    /// <summary>
    /// Minimum number of columns for the editable area.
    /// Keeps the widget from collapsing to zero width when Value is empty.
    /// </summary>
    private const int MinEditWidth = 20;

    // Input fill color and a slightly lighter variant for the half-block edges.
    private static readonly Color InputColor     = Color.FromRgb(60, 60, 80);
    private static readonly Color HighlightColor = Color.FromRgb(90, 90, 115);

    // Focused inputs use a brighter shade so the user can see which field is
    // currently selected in the focus ring.
    private static readonly Color FocusedInputColor     = Color.FromRgb(40, 50, 120);
    private static readonly Color FocusedHighlightColor = Color.FromRgb(70, 85, 160);

    public TextInput()
    {
        Focusable = true;
        PreferredWidth = MinEditWidth;
        PreferredHeight = 3;
        // TextInput is always exactly 3 rows tall. MinHeight ensures ShrinkOnAxis
        // cannot collapse it when a sibling (e.g. ScrollView) overflows.
        MinHeight = 3;
    }

    /// <summary>
    /// The user-typed text. Updated character-by-character via HandleInput.
    /// Setting this also resets the cursor to the end of the new value.
    /// </summary>
    public string Value
    {
        get => _value;
        set
        {
            _value = value ?? "";
            _cursorPos = _value.Length;
            RecalculatePreferredWidth();
        }
    }

    /// <summary>
    /// TextInput is always 3 rows tall; width fills the available space so the
    /// input area extends edge-to-edge across the parent container.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        Width  = availableWidth > 0 ? availableWidth : PreferredWidth;
        Height = 3;
    }

    /// <summary>
    /// Handles keyboard input when this widget has focus.
    /// Characters are inserted at the cursor position; Backspace deletes behind
    /// the cursor; Left/Right arrow keys move the cursor within the value.
    /// All other keys are ignored — the runner handles Tab/Ctrl-C at a higher level.
    /// </summary>
    public override void HandleInput(InputEvent input)
    {
        if (input is KeyEvent { Key: Key.Character, Char: char c })
        {
            // Insert at cursor position rather than always appending,
            // so arrow-key repositioning actually works.
            _value = _value.Insert(_cursorPos, c.ToString());
            _cursorPos++;
            RecalculatePreferredWidth();
        }
        else if (input is KeyEvent { Key: Key.Backspace } && _cursorPos > 0)
        {
            _value = _value.Remove(_cursorPos - 1, 1);
            _cursorPos--;
            RecalculatePreferredWidth();
        }
        else if (input is KeyEvent { Key: Key.Left } && _cursorPos > 0)
        {
            _cursorPos--;
        }
        else if (input is KeyEvent { Key: Key.Right } && _cursorPos < _value.Length)
        {
            _cursorPos++;
        }
    }

    /// <summary>
    /// Draws the input as a solid block with half-block edges on top and bottom.
    ///
    /// Row 0: lower-half blocks — fg is the input color (fills bottom half),
    ///        bg is the lighter highlight (shows in the top half).
    /// Row 1: value text on a solid input-colored background, padded with full
    ///        blocks on either side. Cursor inverts fg/bg at its position.
    /// Row 2: upper-half blocks — fg is the input color (fills top half),
    ///        bg is the lighter highlight (shows in the bottom half).
    /// </summary>
    public override void Draw(ICanvas canvas)
    {
        var fillColor = IsFocused ? FocusedInputColor : InputColor;
        var hlColor   = IsFocused ? FocusedHighlightColor : HighlightColor;

        // Row 0: lower-half blocks create the top edge.
        // The fg fills the bottom half; the bg shows in the top half.
        var edgeStyle = new Style(fillColor, hlColor);
        canvas.DrawHLine(0, 0, Width, LowerHalfBlock, edgeStyle);

        // Row 1: solid background for the content row. Fill with full blocks,
        // then overlay the value text with readable foreground.
        var blockStyle = new Style(fillColor, Color.Black);
        canvas.DrawHLine(0, 1, Width, FullBlock, blockStyle);
        var textStyle = new Style(Color.BrightWhite, fillColor);
        canvas.DrawText(0, 1, _value, textStyle);

        // Only show the cursor when focused — unfocused inputs just display the value.
        if (IsFocused && _cursorPos < Width)
        {
            var charUnderCursor = _cursorPos < _value.Length ? _value[_cursorPos] : ' ';
            var cursorStyle = new Style(fillColor, Color.BrightWhite);
            canvas.SetCell(_cursorPos, 1, charUnderCursor, cursorStyle);
        }

        // Row 2: upper-half blocks create the bottom edge (mirrors the top).
        canvas.DrawHLine(0, 2, Width, UpperHalfBlock, edgeStyle);
    }

    private void RecalculatePreferredWidth()
    {
        // +1 for the cursor character
        var needed = _value.Length + 1;
        PreferredWidth = Math.Max(needed, MinEditWidth);
    }
}
