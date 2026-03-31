using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Components;

/// <summary>
/// Base type for all renderable, interactive UI elements. Replaces <see cref="IComponent"/>.
/// Chrome (border, background, padding) is configuration on any widget — subclasses
/// implement <see cref="RenderContent"/> and get chrome for free.
/// </summary>
public abstract class Widget
{
    public bool Border { get; set; }
    public Color? BorderForeground { get; set; }
    public Color? BorderBackground { get; set; }
    public Color? Background { get; set; }
    public Thickness Padding { get; set; }

    /// <summary>
    /// Renders chrome then delegates to <see cref="RenderContent"/>.
    /// Not virtual — the chrome pipeline is fixed.
    /// </summary>
    public void Render(Buffer buffer, Rect area)
    {
        var inner = ContentRect(area);
        if (inner.Width <= 0 || inner.Height <= 0)
            return;

        if (Background is { } bg)
            buffer.Fill(area, new Cell(' ', Color.Default, bg));

        if (Border)
        {
            var borderFg = BorderForeground ?? Color.White;
            var borderBg = BorderBackground ?? Background ?? Color.Black;
            buffer.DrawBox(area, borderFg, borderBg);
        }

        RenderContent(buffer, inner);
    }

    /// <summary>Subclasses draw content here. The rect has chrome already subtracted.</summary>
    protected abstract void RenderContent(Buffer buffer, Rect area);

    /// <summary>Process a key event. Return true if consumed.</summary>
    public abstract bool HandleKey(KeyEvent key);

    /// <summary>
    /// Override to report preferred content height for a given width.
    /// Returns null by default (no preferred height).
    /// </summary>
    protected virtual int? MeasureContentHeight(int availableWidth) => null;

    /// <summary>
    /// Total height including chrome. Calls <see cref="MeasureContentHeight"/>
    /// and adds border + padding overhead. Not virtual.
    /// </summary>
    public int? MeasureHeight(int availableWidth)
    {
        var hChrome = HorizontalChrome;
        var contentHeight = MeasureContentHeight(availableWidth - hChrome);
        if (contentHeight is null)
            return null;
        return contentHeight.Value + VerticalChrome;
    }

    /// <summary>Computes the inner content rect given an outer rect.</summary>
    public Rect ContentRect(Rect outer)
    {
        var b = Border ? 1 : 0;
        var x = outer.X + b + Padding.Left;
        var y = outer.Y + b + Padding.Top;
        var w = outer.Width - HorizontalChrome;
        var h = outer.Height - VerticalChrome;
        return new Rect(x, y, Math.Max(0, w), Math.Max(0, h));
    }

    private int HorizontalChrome => (Border ? 2 : 0) + Padding.Horizontal;
    private int VerticalChrome => (Border ? 2 : 0) + Padding.Vertical;
}
