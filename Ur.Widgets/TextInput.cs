using Ur.Console;
using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A single-line text input widget that accepts typed characters when focused.
/// Renders the current value with a cursor, using distinct background colors for
/// focused vs. unfocused states so the user can always identify input fields.
/// Labels (prompts like "Name:") are separate Label widgets — TextInput only
/// handles the editable value.
/// </summary>
public class TextInput : Widget
{
    private string _value = "";
    private int _cursorPos;

    /// <summary>
    /// Minimum number of columns for the editable area.
    /// Keeps the widget from collapsing to zero width when Value is empty.
    /// </summary>
    private const int MinEditWidth = 20;

    private static readonly Style FocusedStyle = new(Color.BrightWhite, Color.Blue);
    private static readonly Style UnfocusedStyle = new(Color.White, Color.BrightBlack);

    public TextInput()
    {
        Focusable = true;
        PreferredWidth = MinEditWidth;
        PreferredHeight = 1;
        // TextInput is always exactly one row tall. MinHeight ensures ShrinkOnAxis
        // cannot collapse it to zero when a sibling (e.g. ScrollView) overflows.
        MinHeight = 1;
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
    /// TextInput is always one row tall; width fills the available space so the
    /// colored background extends edge-to-edge across the parent container.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        Width  = availableWidth > 0 ? availableWidth : PreferredWidth;
        Height = 1;
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
    /// Renders the current value and cursor.
    /// Focused inputs get a blue background; unfocused inputs get a grey background
    /// so they remain visually distinct from plain labels. The cursor inverts the
    /// foreground/background of the cell it sits on so the character underneath
    /// stays readable.
    /// </summary>
    public override void Draw(ICanvas canvas)
    {
        var style = IsFocused ? FocusedStyle : UnfocusedStyle;

        // Fill the entire line so the background color extends edge-to-edge,
        // making the input field visually distinct from surrounding labels.
        canvas.DrawHLine(0, 0, Width, ' ', style);
        canvas.DrawText(0, 0, _value, style);

        // Only show the cursor when focused — unfocused inputs just display the value.
        if (IsFocused && _cursorPos < Width)
        {
            var charUnderCursor = _cursorPos < _value.Length ? _value[_cursorPos] : ' ';
            var cursorStyle = new Style(style.Bg, style.Fg);
            canvas.SetCell(_cursorPos, 0, charUnderCursor, cursorStyle);
        }
    }

    private void RecalculatePreferredWidth()
    {
        // +1 for the cursor character
        var needed = _value.Length + 1;
        PreferredWidth = Math.Max(needed, MinEditWidth);
    }
}
