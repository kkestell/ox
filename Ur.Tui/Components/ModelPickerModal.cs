using System.Text;
using Ur.Providers;
using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

public sealed class ModelPickerModal : Widget
{
    public const int HorizontalPadding = 4;
    public const int VerticalPadding = 2;

    // Layout: row 0 = title, row 1 = filter, row 2 = separator, rows 3..N-1 = list.
    private const int HeaderRows = 3;

    private static readonly Color ModalBg = new(30, 30, 60);
    private static readonly Color TitleFg = new(255, 255, 100);
    private static readonly Color FilterFg = Color.White;
    private static readonly Color HintFg = new(128, 128, 128);
    private static readonly Color ItemFg = new(180, 180, 180);
    private static readonly Color SelectedFg = Color.White;
    private static readonly Color SelectedBg = new(60, 60, 120);
    private const int Gap = 2;
    private const int ContextColWidth = 5;  // "1.0M", "200k"
    private const int PriceColWidth = 6;    // "$75.00"

    private readonly IReadOnlyList<ModelInfo> _allModels;
    private readonly StringBuilder _filter = new();
    private List<ModelInfo> _filtered;

    private readonly ScrollableList<ModelInfo> _list;

    public bool Submitted { get; private set; }
    public bool Dismissed { get; private set; }
    public ModelInfo? SelectedModel { get; private set; }

    public ModelPickerModal(IReadOnlyList<ModelInfo> models)
    {
        Border = true;
        BorderForeground = new Color(220, 220, 220);
        BorderBackground = ModalBg;
        Background = ModalBg;

        _allModels = models;
        _filtered = models.ToList();

        _list = new ScrollableList<ModelInfo>
        {
            Background = ModalBg,
            ItemRenderer = RenderItem,
            Items = _filtered,
        };
    }

    public string Filter => _filter.ToString();
    public IReadOnlyList<ModelInfo> FilteredModels => _filtered;

    protected override void RenderContent(Buffer buffer, Rect area)
    {
        var bg = ModalBg;

        // Title
        buffer.WriteString(area.X, area.Y, "Select Model", TitleFg, bg);

        // Filter input
        var filterLabel = "Filter: ";
        buffer.WriteString(area.X, area.Y + 1, filterLabel, HintFg, bg);
        buffer.WriteString(area.X + filterLabel.Length, area.Y + 1, _filter.ToString(), FilterFg, bg);

        // Separator
        for (var x = 0; x < area.Width; x++)
            buffer.Set(area.X + x, area.Y + 2, new Cell('─', HintFg, bg));

        // List area
        var listHeight = area.Height - HeaderRows;
        if (listHeight > 0)
        {
            var listRect = new Rect(area.X, area.Y + HeaderRows, area.Width, listHeight);
            _list.Render(buffer, listRect);
        }
    }

    public override bool HandleKey(KeyEvent key)
    {
        switch (key.Key)
        {
            case Key.Enter:
                if (_list.SelectedItem is { } selected)
                {
                    Submitted = true;
                    SelectedModel = selected;
                }
                return false;

            case Key.Escape:
                Dismissed = true;
                return false;

            case Key.Up or Key.Down or Key.Home or Key.End or Key.PageUp or Key.PageDown:
                return _list.HandleKey(key);

            case Key.Backspace:
                if (_filter.Length > 0)
                {
                    _filter.Remove(_filter.Length - 1, 1);
                    ApplyFilter();
                }
                return true;

            default:
                if (key.Char.HasValue)
                {
                    _filter.Append(key.Char.Value);
                    ApplyFilter();
                }
                return true;
        }
    }

    private void ApplyFilter()
    {
        var filterText = _filter.ToString();
        if (string.IsNullOrEmpty(filterText))
        {
            _filtered = _allModels.ToList();
        }
        else
        {
            _filtered = _allModels
                .Where(m => m.Name.Contains(filterText, StringComparison.OrdinalIgnoreCase)
                         || m.Id.Contains(filterText, StringComparison.OrdinalIgnoreCase))
                .ToList();
        }

        _list.Items = _filtered;
        _list.SetSelectedIndex(0);
    }

    private void RenderItem(Buffer buffer, Rect rect, ModelInfo model, bool isSelected)
    {
        var fg = isSelected ? SelectedFg : ItemFg;
        var bg = isSelected ? SelectedBg : ModalBg;

        if (isSelected)
            buffer.Fill(rect, new Cell(' ', fg, bg));

        var context = FormatContext(model.ContextLength);
        var priceIn = FormatPrice(model.InputCostPerToken);
        var priceOut = FormatPrice(model.OutputCostPerToken);

        // Fixed columns from the right: gap + context + gap + priceIn + gap + priceOut + scroll indicator
        const int scrollReserve = 1;
        var fixedWidth = Gap + ContextColWidth + Gap + PriceColWidth + Gap + PriceColWidth + scrollReserve;
        var modelColWidth = rect.Width - fixedWidth;

        var x = rect.X;

        // Model ID (left-aligned, truncated if needed)
        if (modelColWidth > 0)
        {
            var id = model.Id.Length > modelColWidth
                ? model.Id[..(modelColWidth - 1)] + "…"
                : model.Id;
            buffer.WriteString(x, rect.Y, id, fg, bg);
        }
        x += Math.Max(0, modelColWidth) + Gap;

        // Context (right-aligned within column)
        buffer.WriteString(x + ContextColWidth - context.Length, rect.Y, context, fg, bg);
        x += ContextColWidth + Gap;

        // Price in (right-aligned within column)
        buffer.WriteString(x + PriceColWidth - priceIn.Length, rect.Y, priceIn, fg, bg);
        x += PriceColWidth + Gap;

        // Price out (right-aligned within column)
        buffer.WriteString(x + PriceColWidth - priceOut.Length, rect.Y, priceOut, fg, bg);
    }

    private static string FormatContext(int contextLength)
    {
        return contextLength >= 1_000_000
            ? $"{contextLength / 1_000_000.0:F1}M"
            : $"{contextLength / 1_000}k";
    }

    private static string FormatPrice(decimal costPerToken)
    {
        if (costPerToken == 0m)
            return "—";
        var perMtok = costPerToken * 1_000_000m;
        return $"${perMtok:F2}";
    }


}
