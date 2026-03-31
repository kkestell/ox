using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

public sealed class ModelStatusBar(IChatBackend backend) : Widget
{
    private static readonly Color Fg = new(100, 100, 100);
    private static readonly Color Bg = Color.Black;

    protected override int? MeasureContentHeight(int availableWidth) => 1;

    protected override void RenderContent(Buffer buffer, Rect area)
    {
        var text = backend.SelectedModelId ?? "";
        if (text.Length == 0 || area.Width <= 0)
            return;

        var x = area.X + Math.Max(0, area.Width - text.Length);
        buffer.WriteString(x, area.Y, text, Fg, Bg);
    }

    public override bool HandleKey(KeyEvent key) => false;
}
