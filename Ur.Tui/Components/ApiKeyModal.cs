using System.Text;
using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

public sealed class ApiKeyModal : IComponent
{
    public const int ModalWidth = 50;
    public const int ModalHeight = 7;

    private static readonly Color BorderFg = new(220, 220, 220);
    private static readonly Color ModalBg = new(30, 30, 60);
    private static readonly Color TitleFg = new(255, 255, 100);
    private static readonly Color HintFg = new(128, 128, 128);
    private static readonly Color InputFg = Color.White;

    private readonly StringBuilder _text = new();

    public bool Submitted { get; private set; }
    public bool Dismissed { get; private set; }
    public string? Value { get; private set; }

    public void Render(Buffer buffer, Rect area)
    {
        // The area passed is the full screen; we center ourselves within it
        var mx = (area.Width - ModalWidth) / 2 + area.X;
        var my = (area.Height - ModalHeight) / 2 + area.Y;
        var modalRect = new Rect(mx, my, ModalWidth, ModalHeight);

        // Fill and draw border
        buffer.Fill(modalRect, new Cell(' ', BorderFg, ModalBg));
        buffer.DrawBox(modalRect, BorderFg, ModalBg);

        // Title
        buffer.WriteString(mx + 2, my + 1, "API Key", TitleFg, ModalBg);

        // Hint
        buffer.WriteString(mx + 2, my + 3, "Enter your OpenRouter API key:", HintFg, ModalBg);

        // Masked input field
        var inputX = mx + 2;
        var inputY = my + 4;
        var inputWidth = ModalWidth - 4;
        var masked = new string('*', Math.Min(_text.Length, inputWidth));
        buffer.WriteString(inputX, inputY, masked, InputFg, ModalBg);

        // Esc hint
        buffer.WriteString(mx + 2, my + 5, "Esc to cancel", HintFg, ModalBg);
    }

    public bool HandleKey(KeyEvent key)
    {
        switch (key.Key)
        {
            case Key.Enter:
                Submitted = true;
                Value = _text.ToString();
                return false;

            case Key.Escape:
                Dismissed = true;
                return false;

            case Key.Backspace:
                if (_text.Length > 0)
                    _text.Remove(_text.Length - 1, 1);
                return true;

            default:
                if (key.Char.HasValue)
                    _text.Append(key.Char.Value);
                return true;
        }
    }
}
