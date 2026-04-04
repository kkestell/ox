using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A simple text display widget that sizes itself based on its content.
/// </summary>
public class Label : Widget
{
    private string _text;
    private string[] _lines;

    public Label(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        _text = text;
        _lines = text.Split('\n');
        var maxWidth = _lines.Max(l => l.Length);
        PreferredWidth = maxWidth;
        PreferredHeight = _lines.Length;
        Width = maxWidth;
        Height = _lines.Length;
    }

    /// <summary>
    /// Gets or sets the displayed text. Setting this recalculates the label's
    /// preferred dimensions so the layout engine can resize it on the next pass.
    /// </summary>
    public string Text
    {
        get => _text;
        set
        {
            ArgumentNullException.ThrowIfNull(value);
            _text = value;
            _lines = value.Split('\n');
            var maxWidth = _lines.Max(l => l.Length);
            PreferredWidth = maxWidth;
            PreferredHeight = _lines.Length;
        }
    }

    public string[] Lines => _lines;

    /// <summary>
    /// Sets Width to the available width (constrained by parent) or the natural text
    /// width when unconstrained. Height is always the natural line count — Labels never
    /// shrink vertically regardless of what the parent offers.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        Width  = availableWidth > 0 ? availableWidth : PreferredWidth;
        Height = PreferredHeight;
    }

    public override void Draw(ICanvas canvas)
    {
        var y = 0;
        foreach (var line in _lines)
        {
            canvas.DrawText(0, y, line, Style);
            y++;
        }
    }
}
