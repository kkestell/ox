using System.Text;
using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

public sealed class ChatInput : IComponent
{
    private const string Prompt = "> ";
    private static readonly Color PromptFg = new(0, 200, 200);
    private static readonly Color TextFg = Color.White;
    private static readonly Color Bg = Color.Black;
    private static readonly Color CursorFg = Color.Black;
    private static readonly Color CursorBg = Color.White;

    private readonly StringBuilder _text = new();
    private int _cursorPos;

    public string Text => _text.ToString();

    public void Clear()
    {
        _text.Clear();
        _cursorPos = 0;
    }

    public void Render(Buffer buffer, Rect area)
    {
        if (area.Width < 1 || area.Height < 1)
            return;

        // Fill background
        buffer.Fill(area, new Cell(' ', TextFg, Bg));

        // Draw prompt
        buffer.WriteString(area.X, area.Y, Prompt, PromptFg, Bg);

        // Draw text
        var textX = area.X + Prompt.Length;
        var availableWidth = area.Width - Prompt.Length;
        if (availableWidth <= 0)
            return;

        var text = _text.ToString();
        var visibleText = text.Length <= availableWidth ? text : text[..availableWidth];
        buffer.WriteString(textX, area.Y, visibleText, TextFg, Bg);

        // Draw cursor (inverted colors)
        var cursorX = textX + _cursorPos;
        if (cursorX < area.Right)
        {
            var cursorChar = _cursorPos < _text.Length ? _text[_cursorPos] : ' ';
            buffer.Set(cursorX, area.Y, new Cell(cursorChar, CursorFg, CursorBg));
        }
    }

    public bool HandleKey(KeyEvent key)
    {
        switch (key.Key)
        {
            case Key.Enter:
                return false; // Signal to app: input submitted

            case Key.Backspace:
                if (_cursorPos > 0)
                {
                    _text.Remove(_cursorPos - 1, 1);
                    _cursorPos--;
                }
                return true;

            case Key.Delete:
                if (_cursorPos < _text.Length)
                    _text.Remove(_cursorPos, 1);
                return true;

            case Key.Left:
                if (_cursorPos > 0)
                    _cursorPos--;
                return true;

            case Key.Right:
                if (_cursorPos < _text.Length)
                    _cursorPos++;
                return true;

            case Key.Home:
                _cursorPos = 0;
                return true;

            case Key.End:
                _cursorPos = _text.Length;
                return true;

            default:
                if (key.Char.HasValue)
                {
                    _text.Insert(_cursorPos, key.Char.Value);
                    _cursorPos++;
                    return true;
                }
                return true;
        }
    }
}
