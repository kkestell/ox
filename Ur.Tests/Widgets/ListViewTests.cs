using Ur.Drawing;
using Ur.Widgets;
using Xunit;

namespace Ur.Tests.Widgets;

public class ListViewTests
{
    // Simple item type for tests — a record with an Id so we can distinguish items.
    private record Item(int Id, string Text);

    // Factory that creates a Label for each item, preserving a mapping for assertions.
    private static ListView<Item> MakeListView(out Func<Item, Widget> capturedFactory)
    {
        var created = new List<(Item item, Widget widget)>();
        capturedFactory = item =>
        {
            var w = new Label(item.Text);
            created.Add((item, w));
            return w;
        };
        return new ListView<Item>(capturedFactory);
    }

    [Fact]
    public void AddItem_CreatesChildWidget()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));

        listView.Items.Add(new Item(1, "Hello"));

        Assert.Single(listView.Children);
        var child = (Label)listView.Children[0];
        Assert.Equal("Hello", child.Text);
    }

    [Fact]
    public void AddMultipleItems_CreatesChildrenInOrder()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));

        listView.Items.Add(new Item(1, "A"));
        listView.Items.Add(new Item(2, "B"));
        listView.Items.Add(new Item(3, "C"));

        Assert.Equal(3, listView.Children.Count);
        Assert.Equal("A", ((Label)listView.Children[0]).Text);
        Assert.Equal("B", ((Label)listView.Children[1]).Text);
        Assert.Equal("C", ((Label)listView.Children[2]).Text);
    }

    [Fact]
    public void AddItem_SetsChildParent()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));
        listView.Items.Add(new Item(1, "Test"));

        Assert.Equal(listView, listView.Children[0].Parent);
    }

    [Fact]
    public void RemoveItem_RemovesCorrespondingWidget()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));
        var item1 = new Item(1, "A");
        var item2 = new Item(2, "B");
        var item3 = new Item(3, "C");

        listView.Items.Add(item1);
        listView.Items.Add(item2);
        listView.Items.Add(item3);

        listView.Items.Remove(item2);

        Assert.Equal(2, listView.Children.Count);
        Assert.Equal("A", ((Label)listView.Children[0]).Text);
        Assert.Equal("C", ((Label)listView.Children[1]).Text);
    }

    [Fact]
    public void RemoveItem_ClearsWidgetParent()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));
        var item = new Item(1, "Test");
        listView.Items.Add(item);
        var child = listView.Children[0];

        listView.Items.Remove(item);

        Assert.Null(child.Parent);
    }

    [Fact]
    public void ClearItems_RemovesAllWidgets()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));

        listView.Items.Add(new Item(1, "A"));
        listView.Items.Add(new Item(2, "B"));
        listView.Items.Add(new Item(3, "C"));

        listView.Items.Clear();

        Assert.Empty(listView.Children);
    }

    [Fact]
    public void InsertItem_AtBeginning_InsertsWidgetAtCorrectPosition()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));

        listView.Items.Add(new Item(1, "B"));
        listView.Items.Add(new Item(2, "C"));

        // Insert at front — verifies NewStartingIndex handling, not just append.
        listView.Items.Insert(0, new Item(0, "A"));

        Assert.Equal(3, listView.Children.Count);
        Assert.Equal("A", ((Label)listView.Children[0]).Text);
        Assert.Equal("B", ((Label)listView.Children[1]).Text);
        Assert.Equal("C", ((Label)listView.Children[2]).Text);
    }

    [Fact]
    public void InsertItem_InMiddle_InsertsWidgetAtCorrectPosition()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));

        listView.Items.Add(new Item(1, "A"));
        listView.Items.Add(new Item(3, "C"));

        listView.Items.Insert(1, new Item(2, "B"));

        Assert.Equal(3, listView.Children.Count);
        Assert.Equal("A", ((Label)listView.Children[0]).Text);
        Assert.Equal("B", ((Label)listView.Children[1]).Text);
        Assert.Equal("C", ((Label)listView.Children[2]).Text);
    }

    [Fact]
    public void RemoveMultipleItems_InSequence_KeepsRemainingWidgetsOrdered()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));
        var items = Enumerable.Range(0, 5).Select(i => new Item(i, $"Item {i}")).ToList();
        foreach (var item in items)
            listView.Items.Add(item);

        // Remove items at different positions to stress-test parallel list coherence.
        listView.Items.Remove(items[4]); // remove last
        listView.Items.Remove(items[0]); // remove first
        listView.Items.Remove(items[2]); // remove middle

        Assert.Equal(2, listView.Children.Count);
        Assert.Equal("Item 1", ((Label)listView.Children[0]).Text);
        Assert.Equal("Item 3", ((Label)listView.Children[1]).Text);
    }

    [Fact]
    public void Draw_DoesNotThrow()
    {
        var screen = new Screen(20, 5);
        var canvas = CanvasFactory.CreateCanvas(screen);
        var listView = new ListView<Item>(item => new Label(item.Text));
        listView.Items.Add(new Item(1, "Hello"));

        var ex = Record.Exception(() => listView.Draw(canvas));
        Assert.Null(ex);
    }

    [Fact]
    public void DefaultSizing_IsGrowHorizontalFitVertical()
    {
        var listView = new ListView<Item>(item => new Label(item.Text));

        Assert.Equal(SizingMode.Grow, listView.HorizontalSizing);
        Assert.Equal(SizingMode.Fit, listView.VerticalSizing);
    }

    [Fact]
    public void Constructor_ThrowsOnNullFactory()
    {
        Assert.Throws<ArgumentNullException>(() => new ListView<Item>(null!));
    }
}
