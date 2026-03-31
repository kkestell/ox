using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Components;

/// <summary>
/// Reusable scrollable, selectable list. Manages selection state, scroll offset,
/// visible window, and scroll indicators. The caller provides items and a render
/// callback for each item.
/// </summary>
public sealed class ScrollableList<T> : Widget
{
    private static readonly Color IndicatorFg = new(128, 128, 128);

    private IReadOnlyList<T> _items = [];
    private int _selectedIndex;
    private int _scrollOffset;
    private int _lastVisibleCount;

    /// <summary>Callback to render one item. Parameters: buffer, rect, item, isSelected.</summary>
    public required Action<Buffer, Rect, T, bool> ItemRenderer { get; init; }

    public IReadOnlyList<T> Items
    {
        get => _items;
        set
        {
            _items = value;
            _selectedIndex = _items.Count > 0 ? Math.Clamp(_selectedIndex, 0, _items.Count - 1) : -1;
            ClampScroll();
        }
    }

    public int SelectedIndex => _selectedIndex;
    public T? SelectedItem => _selectedIndex >= 0 && _selectedIndex < _items.Count ? _items[_selectedIndex] : default;

    protected override void RenderContent(Buffer buffer, Rect area)
    {
        _lastVisibleCount = area.Height;
        ClampScroll();

        var visibleCount = Math.Min(area.Height, Math.Max(0, _items.Count - _scrollOffset));
        for (var i = 0; i < visibleCount; i++)
        {
            var itemIndex = _scrollOffset + i;
            var itemRect = new Rect(area.X, area.Y + i, area.Width, 1);
            ItemRenderer(buffer, itemRect, _items[itemIndex], itemIndex == _selectedIndex);
        }

        // Scroll indicators.
        if (area.Width > 0 && area.Height > 0)
        {
            var indicatorBg = Background ?? Color.Black;
            if (_scrollOffset > 0)
                buffer.WriteString(area.Right - 1, area.Y, "▲", IndicatorFg, indicatorBg);
            if (_scrollOffset + area.Height < _items.Count)
                buffer.WriteString(area.Right - 1, area.Bottom - 1, "▼", IndicatorFg, indicatorBg);
        }
    }

    public override bool HandleKey(KeyEvent key)
    {
        switch (key.Key)
        {
            case Key.Up:
                MoveSelection(-1);
                return true;
            case Key.Down:
                MoveSelection(1);
                return true;
            case Key.Home:
                _selectedIndex = 0;
                EnsureVisible();
                return true;
            case Key.End:
                _selectedIndex = Math.Max(0, _items.Count - 1);
                EnsureVisible();
                return true;
            case Key.PageUp:
                MoveSelection(-Math.Max(1, _lastVisibleCount));
                return true;
            case Key.PageDown:
                MoveSelection(Math.Max(1, _lastVisibleCount));
                return true;
            case Key.Enter:
                return false;
            default:
                return false;
        }
    }

    /// <summary>Set the selected index, clamping to valid range.</summary>
    public void SetSelectedIndex(int index)
    {
        _selectedIndex = _items.Count > 0 ? Math.Clamp(index, 0, _items.Count - 1) : -1;
        EnsureVisible();
    }

    private void MoveSelection(int delta)
    {
        if (_items.Count == 0) return;
        _selectedIndex = Math.Clamp(_selectedIndex + delta, 0, _items.Count - 1);
        EnsureVisible();
    }

    private void EnsureVisible()
    {
        if (_lastVisibleCount <= 0) return;
        if (_selectedIndex < _scrollOffset)
            _scrollOffset = _selectedIndex;
        else if (_selectedIndex >= _scrollOffset + _lastVisibleCount)
            _scrollOffset = _selectedIndex - _lastVisibleCount + 1;
    }

    private void ClampScroll()
    {
        var maxScroll = Math.Max(0, _items.Count - Math.Max(1, _lastVisibleCount));
        _scrollOffset = Math.Clamp(_scrollOffset, 0, maxScroll);
    }
}
