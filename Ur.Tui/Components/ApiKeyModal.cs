using System.Text;
using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

public sealed class ApiKeyModal : Widget
{
    public const int ModalWidth = 50;
    public const int ModalHeight = 7;

    private static readonly Color ModalBg = new(30, 30, 60);
    private static readonly Color TitleFg = new(255, 255, 100);
    private static readonly Color HintFg = new(128, 128, 128);
    private static readonly Color InputFg = Color.White;

    private readonly StringBuilder _text = new();

    public ApiKeyModal()
    {
        Border = true;
        BorderForeground = new Color(220, 220, 220);
        BorderBackground = ModalBg;
        Background = ModalBg;
    }

    public bool Submitted { get; private set; }
    public bool Dismissed { get; private set; }
    public string? Value { get; private set; }

    protected override void RenderContent(Buffer buffer, Rect area)
    {
        var bg = ModalBg;

        // Title
        buffer.WriteString(area.X, area.Y, "API Key", TitleFg, bg);

        // Hint
        buffer.WriteString(area.X, area.Y + 2, "Enter your OpenRouter API key:", HintFg, bg);

        // Masked input field
        var masked = new string('*', Math.Min(_text.Length, area.Width));
        buffer.WriteString(area.X, area.Y + 3, masked, InputFg, bg);

        // Esc hint
        buffer.WriteString(area.X, area.Y + 4, "Esc to cancel", HintFg, bg);
    }

    public override bool HandleKey(KeyEvent key)
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
