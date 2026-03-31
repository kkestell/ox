using System.Text;
using Ur.Providers;
using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

public sealed class ModelPickerModal : Widget
{
    public const int ModalWidth = 60;
    public const int ModalHeight = 20;

    // Layout: row 0 = title, row 1 = filter, row 2 = separator, rows 3..N-3 = list, rows N-2..N-1 = detail.
    private const int HeaderRows = 3;
    private const int DetailRows = 2;

    private static readonly Color ModalBg = new(30, 30, 60);
    private static readonly Color TitleFg = new(255, 255, 100);
    private static readonly Color FilterFg = Color.White;
    private static readonly Color HintFg = new(128, 128, 128);
    private static readonly Color ItemFg = new(180, 180, 180);
    private static readonly Color SelectedFg = Color.White;
    private static readonly Color SelectedBg = new(60, 60, 120);
    private static readonly Color DetailFg = new(160, 160, 160);

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
        var listHeight = area.Height - HeaderRows - DetailRows;
        if (listHeight > 0)
        {
            var listRect = new Rect(area.X, area.Y + HeaderRows, area.Width, listHeight);
            _list.Render(buffer, listRect);
        }

        // Detail area for selected model
        var detailY = area.Y + area.Height - DetailRows;
        if (_list.SelectedItem is { } selected)
        {
            var contextStr = selected.ContextLength >= 1_000_000
                ? $"{selected.ContextLength / 1_000_000.0:F1}M"
                : $"{selected.ContextLength / 1_000}K";
            var detail = $"{selected.Id}  ctx:{contextStr}";
            buffer.WriteString(area.X, detailY, detail, DetailFg, bg);
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

    private static void RenderItem(Buffer buffer, Rect rect, ModelInfo model, bool isSelected)
    {
        var fg = isSelected ? SelectedFg : ItemFg;
        var bg = isSelected ? SelectedBg : ModalBg;

        if (isSelected)
            buffer.Fill(rect, new Cell(' ', fg, bg));

        var displayText = model.Name.Length > rect.Width
            ? model.Name[..(rect.Width - 1)] + "…"
            : model.Name;
        buffer.WriteString(rect.X, rect.Y, displayText, fg, bg);
    }
}
