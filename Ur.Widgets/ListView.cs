using System.Collections.ObjectModel;
using System.Collections.Specialized;
using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A data-driven container widget that keeps a widget child for every item in a
/// collection. Think of it as a Stack whose children are created and destroyed
/// automatically as items are added to or removed from <see cref="Items"/>.
///
/// Design:
/// - ListView is a thin wrapper over the normal container mechanism. It does not
///   own any rendering logic; Draw() is empty, just like Flex.
/// - ListView.Layout() positions children in parent-relative coordinates and calls
///   each child's Layout(). The Renderer draws them as with any other container.
/// - A parallel <see cref="_itemWidgets"/> list maps each item index to its widget
///   so that CollectionChanged Remove events can find the right widget to evict.
/// - Typical usage: wrap in a ScrollView to get scrolling + scrollbar behavior.
/// </summary>
/// <typeparam name="T">The item type held in the data source.</typeparam>
public class ListView<T> : Widget
{
    private readonly Func<T, Widget> _itemFactory;

    // Parallel to Items — entry i is the widget created for Items[i].
    private readonly List<Widget> _itemWidgets = [];

    /// <summary>
    /// The observable data source. Mutate this collection from the UI thread
    /// (or via Application.Invoke) to add, remove, or replace items; the ListView
    /// will automatically create or destroy the corresponding child widgets.
    /// </summary>
    public ObservableCollection<T> Items { get; } = [];

    /// <param name="itemFactory">
    /// Called once per item to produce the widget that represents it.
    /// The returned widget is added as a child and removed when the item is removed.
    /// </param>
    public ListView(Func<T, Widget> itemFactory)
    {
        ArgumentNullException.ThrowIfNull(itemFactory);
        _itemFactory = itemFactory;

        // Grow horizontally to fill the scroll viewport width; let height fit children
        // so the parent (ScrollView) can measure the natural content height.
        HorizontalSizing = SizingMode.Grow;
        VerticalSizing = SizingMode.Fit;
        Direction = LayoutDirection.Vertical;

        // Subscribe to collection changes so the widget tree stays in sync with Items.
        Items.CollectionChanged += OnItemsChanged;
    }

    private void OnItemsChanged(object? sender, NotifyCollectionChangedEventArgs e)
    {
        switch (e.Action)
        {
            case NotifyCollectionChangedAction.Add:
                // New items appended (or inserted) — create and add a widget for each.
                // NewStartingIndex lets us handle insertions correctly, but for chat
                // the typical case is append-to-end.
                if (e.NewItems is null) break;
                for (var i = 0; i < e.NewItems.Count; i++)
                {
                    var item = (T)e.NewItems[i]!;
                    var widget = _itemFactory(item);
                    var insertAt = e.NewStartingIndex + i;

                    // Maintain the parallel list in sync with Children.
                    _itemWidgets.Insert(insertAt, widget);
                    Children.Insert(insertAt, widget);
                    widget.Parent = this;
                }
                break;

            case NotifyCollectionChangedAction.Remove:
                // Items removed — find the corresponding widgets and evict them.
                if (e.OldItems is null) break;
                for (var i = e.OldItems.Count - 1; i >= 0; i--)
                {
                    // Remove in reverse so indices stay valid as we remove.
                    var removeAt = e.OldStartingIndex + i;
                    var widget = _itemWidgets[removeAt];
                    _itemWidgets.RemoveAt(removeAt);
                    RemoveChild(widget);
                }
                break;

            case NotifyCollectionChangedAction.Reset:
                // Collection was cleared — remove all item widgets and rebuild if
                // Items still has content (ObservableCollection.Clear() sends Reset
                // with an empty NewItems list, so rebuilding covers the generic case).
                foreach (var w in _itemWidgets)
                    RemoveChild(w);
                _itemWidgets.Clear();

                foreach (var item in Items)
                {
                    var widget = _itemFactory(item);
                    _itemWidgets.Add(widget);
                    AddChild(widget);
                }
                break;
        }
    }

    /// <summary>
    /// Lays out children as a vertical stack, sizing each one to the available width
    /// and letting it determine its own height. Total height is the sum of children.
    ///
    /// availableHeight=0 means unconstrained (the ScrollView convention): we report
    /// our natural height so ScrollView can compute the scrollbar ratio.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        Width  = availableWidth > 0 ? availableWidth : PreferredWidth;

        var y = 0;
        foreach (var child in Children)
        {
            child.X = 0;
            child.Y = y;
            child.Layout(Width, 0); // unconstrained height — let child report natural size
            y += child.Height + ChildGap;
        }

        // Natural height is the sum of children — no vertical constraint imposed.
        Height = y > 0 ? y - ChildGap : 0;
    }

    /// <summary>
    /// ListView is a pure container — children do all the visual work.
    /// </summary>
    public override void Draw(ICanvas canvas) { }
}
