using System.Text;
using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Tui.Dummy;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

public sealed class ModelPickerModal : IComponent
{
    public const int ModalWidth = 60;
    public const int ModalHeight = 20;
    private const int ListStartRow = 4;
    private const int DetailHeight = 2; // Height reserved for selected model details
    private const int BottomBorderPadding = 1;
    private const int ListHeight = ModalHeight - ListStartRow - DetailHeight - BottomBorderPadding;

    private static readonly Color BorderFg = new(220, 220, 220);
    private static readonly Color ModalBg = new(30, 30, 60);
    private static readonly Color TitleFg = new(255, 255, 100);
    private static readonly Color FilterFg = Color.White;
    private static readonly Color HintFg = new(128, 128, 128);
    private static readonly Color ItemFg = new(180, 180, 180);
    private static readonly Color SelectedFg = Color.White;
    private static readonly Color SelectedBg = new(60, 60, 120);
    private static readonly Color DetailFg = new(160, 160, 160);

    private readonly IReadOnlyList<DummyModelInfo> _allModels;
    private readonly StringBuilder _filter = new();
    private List<DummyModelInfo> _filtered;
    private int _selectedIndex;
    private int _scrollOffset;

    public bool Submitted { get; private set; }
    public bool Dismissed { get; private set; }
    public DummyModelInfo? SelectedModel { get; private set; }

    public ModelPickerModal(IReadOnlyList<DummyModelInfo> models)
    {
        _allModels = models;
        _filtered = models.ToList();
    }

    public string Filter => _filter.ToString();
    public IReadOnlyList<DummyModelInfo> FilteredModels => _filtered;

    public void Render(Buffer buffer, Rect area)
    {
        var mx = (area.Width - ModalWidth) / 2 + area.X;
        var my = (area.Height - ModalHeight) / 2 + area.Y;
        var modalRect = new Rect(mx, my, ModalWidth, ModalHeight);

        buffer.Fill(modalRect, new Cell(' ', BorderFg, ModalBg));
        buffer.DrawBox(modalRect, BorderFg, ModalBg);

        // Title
        buffer.WriteString(mx + 2, my + 1, "Select Model", TitleFg, ModalBg);

        // Filter input
        var filterLabel = "Filter: ";
        buffer.WriteString(mx + 2, my + 2, filterLabel, HintFg, ModalBg);
        var filterText = _filter.ToString();
        var filterX = mx + 2 + filterLabel.Length;
        buffer.WriteString(filterX, my + 2, filterText, FilterFg, ModalBg);

        // Separator
        var sepWidth = ModalWidth - 2;
        for (var x = 0; x < sepWidth; x++)
            buffer.Set(mx + 1 + x, my + 3, new Cell('─', HintFg, ModalBg));

        // Model list
        var listStartY = my + ListStartRow;
        var visibleCount = Math.Min(ListHeight, _filtered.Count - _scrollOffset);
        for (var i = 0; i < visibleCount; i++)
        {
            var modelIndex = _scrollOffset + i;
            var model = _filtered[modelIndex];
            var isSelected = modelIndex == _selectedIndex;
            var fg = isSelected ? SelectedFg : ItemFg;
            var bg = isSelected ? SelectedBg : ModalBg;
            var itemWidth = ModalWidth - 4;

            // Fill the row background for selected item
            if (isSelected)
                buffer.Fill(new Rect(mx + 2, listStartY + i, itemWidth, 1), new Cell(' ', fg, bg));

            var displayText = model.Name.Length > itemWidth
                ? model.Name[..(itemWidth - 1)] + "…"
                : model.Name;
            buffer.WriteString(mx + 2, listStartY + i, displayText, fg, bg);
        }

        // Scroll indicators
        if (_scrollOffset > 0)
            buffer.WriteString(mx + ModalWidth - 4, listStartY, "▲", HintFg, ModalBg);
        if (_scrollOffset + ListHeight < _filtered.Count)
            buffer.WriteString(mx + ModalWidth - 4, listStartY + ListHeight - 1, "▼", HintFg, ModalBg);

        // Detail area for selected model
        var detailY = my + ModalHeight - DetailHeight - 1;
        if (_selectedIndex >= 0 && _selectedIndex < _filtered.Count)
        {
            var selected = _filtered[_selectedIndex];
            var contextStr = selected.ContextLength >= 1_000_000
                ? $"{selected.ContextLength / 1_000_000.0:F1}M"
                : $"{selected.ContextLength / 1_000}K";
            var detail = $"{selected.Id}  ctx:{contextStr}";
            buffer.WriteString(mx + 2, detailY, detail, DetailFg, ModalBg);
        }
    }

    public bool HandleKey(KeyEvent key)
    {
        switch (key.Key)
        {
            case Key.Enter:
                if (_selectedIndex >= 0 && _selectedIndex < _filtered.Count)
                {
                    Submitted = true;
                    SelectedModel = _filtered[_selectedIndex];
                }
                return false;

            case Key.Escape:
                Dismissed = true;
                return false;

            case Key.Up:
                if (_selectedIndex > 0)
                {
                    _selectedIndex--;
                    EnsureVisible();
                }
                return true;

            case Key.Down:
                if (_selectedIndex < _filtered.Count - 1)
                {
                    _selectedIndex++;
                    EnsureVisible();
                }
                return true;

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

        _selectedIndex = 0;
        _scrollOffset = 0;
    }

    private void EnsureVisible()
    {
        if (_selectedIndex < _scrollOffset)
            _scrollOffset = _selectedIndex;
        else if (_selectedIndex >= _scrollOffset + ListHeight)
            _scrollOffset = _selectedIndex - ListHeight + 1;
    }
}
