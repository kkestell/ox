using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Tests;

public class ScrollableListTests
{
    private static KeyEvent Named(Key key) => new(key, Modifiers.None, null);

    private static ScrollableList<string> CreateList(IReadOnlyList<string> items) =>
        new()
        {
            ItemRenderer = (buffer, rect, item, isSelected) =>
                buffer.WriteString(rect.X, rect.Y, item, Color.White, Color.Black),
            Items = items,
        };

    private static void RenderList(ScrollableList<string> list, int width, int height)
    {
        var buffer = new Buffer(width, height);
        list.Render(buffer, new Rect(0, 0, width, height));
    }

    // --- Selection ---

    [Fact]
    public void InitialSelection_IsZero()
    {
        var list = CreateList(["a", "b", "c"]);

        Assert.Equal(0, list.SelectedIndex);
        Assert.Equal("a", list.SelectedItem);
    }

    [Fact]
    public void Down_MovesSelectionForward()
    {
        var list = CreateList(["a", "b", "c"]);

        list.HandleKey(Named(Key.Down));

        Assert.Equal(1, list.SelectedIndex);
        Assert.Equal("b", list.SelectedItem);
    }

    [Fact]
    public void Up_MovesSelectionBackward()
    {
        var list = CreateList(["a", "b", "c"]);
        list.HandleKey(Named(Key.Down));
        list.HandleKey(Named(Key.Down));

        list.HandleKey(Named(Key.Up));

        Assert.Equal(1, list.SelectedIndex);
    }

    [Fact]
    public void Down_AtEnd_StaysAtLast()
    {
        var list = CreateList(["a", "b"]);
        list.HandleKey(Named(Key.Down));
        list.HandleKey(Named(Key.Down));
        list.HandleKey(Named(Key.Down));

        Assert.Equal(1, list.SelectedIndex);
    }

    [Fact]
    public void Up_AtStart_StaysAtZero()
    {
        var list = CreateList(["a", "b"]);

        list.HandleKey(Named(Key.Up));

        Assert.Equal(0, list.SelectedIndex);
    }

    [Fact]
    public void Home_JumpsToFirst()
    {
        var list = CreateList(["a", "b", "c", "d"]);
        list.HandleKey(Named(Key.Down));
        list.HandleKey(Named(Key.Down));

        list.HandleKey(Named(Key.Home));

        Assert.Equal(0, list.SelectedIndex);
    }

    [Fact]
    public void End_JumpsToLast()
    {
        var list = CreateList(["a", "b", "c", "d"]);

        list.HandleKey(Named(Key.End));

        Assert.Equal(3, list.SelectedIndex);
    }

    [Fact]
    public void Enter_ReturnsNotConsumed()
    {
        var list = CreateList(["a", "b"]);

        var consumed = list.HandleKey(Named(Key.Enter));

        Assert.False(consumed);
    }

    [Fact]
    public void NavigationKeys_ReturnConsumed()
    {
        var list = CreateList(["a", "b", "c"]);

        Assert.True(list.HandleKey(Named(Key.Down)));
        Assert.True(list.HandleKey(Named(Key.Up)));
        Assert.True(list.HandleKey(Named(Key.Home)));
        Assert.True(list.HandleKey(Named(Key.End)));
        Assert.True(list.HandleKey(Named(Key.PageDown)));
        Assert.True(list.HandleKey(Named(Key.PageUp)));
    }

    [Fact]
    public void UnknownKey_ReturnsNotConsumed()
    {
        var list = CreateList(["a"]);

        Assert.False(list.HandleKey(Named(Key.Escape)));
    }

    // --- Items property ---

    [Fact]
    public void EmptyItems_SelectedIndexIsNegativeOne()
    {
        var list = CreateList([]);

        Assert.Equal(-1, list.SelectedIndex);
        Assert.Null(list.SelectedItem);
    }

    [Fact]
    public void SetItems_ClampsSelection()
    {
        var list = CreateList(["a", "b", "c", "d"]);
        list.HandleKey(Named(Key.End)); // select index 3

        list.Items = ["x", "y"]; // only 2 items now

        Assert.Equal(1, list.SelectedIndex);
    }

    [Fact]
    public void SetItems_ToEmpty_ResetsSelection()
    {
        var list = CreateList(["a", "b"]);
        list.HandleKey(Named(Key.Down));

        list.Items = [];

        Assert.Equal(-1, list.SelectedIndex);
    }

    // --- SetSelectedIndex ---

    [Fact]
    public void SetSelectedIndex_ClampsToRange()
    {
        var list = CreateList(["a", "b", "c"]);

        list.SetSelectedIndex(100);

        Assert.Equal(2, list.SelectedIndex);
    }

    [Fact]
    public void SetSelectedIndex_NegativeClamps()
    {
        var list = CreateList(["a", "b", "c"]);

        list.SetSelectedIndex(-5);

        Assert.Equal(0, list.SelectedIndex);
    }

    // --- Scrolling ---

    [Fact]
    public void PageDown_MovesSelectionByVisibleCount()
    {
        var list = CreateList(["a", "b", "c", "d", "e", "f", "g", "h", "i", "j"]);
        RenderList(list, 20, 5); // visible count = 5

        list.HandleKey(Named(Key.PageDown));

        Assert.Equal(5, list.SelectedIndex);
    }

    [Fact]
    public void PageUp_MovesSelectionBackByVisibleCount()
    {
        var list = CreateList(["a", "b", "c", "d", "e", "f", "g", "h", "i", "j"]);
        RenderList(list, 20, 5);
        list.SetSelectedIndex(7);

        list.HandleKey(Named(Key.PageUp));

        Assert.Equal(2, list.SelectedIndex);
    }

    // --- Render ---

    [Fact]
    public void Render_CallsItemRenderer_ForVisibleItems()
    {
        var rendered = new List<(string Item, bool IsSelected)>();
        var list = new ScrollableList<string>
        {
            ItemRenderer = (_, _, item, isSelected) => rendered.Add((item, isSelected)),
            Items = ["a", "b", "c"],
        };
        var buffer = new Buffer(20, 3);

        list.Render(buffer, new Rect(0, 0, 20, 3));

        Assert.Equal(3, rendered.Count);
        Assert.Equal(("a", true), rendered[0]);
        Assert.Equal(("b", false), rendered[1]);
        Assert.Equal(("c", false), rendered[2]);
    }

    [Fact]
    public void Render_OnlyVisibleItems_WhenListExceedsHeight()
    {
        var rendered = new List<string>();
        var list = new ScrollableList<string>
        {
            ItemRenderer = (_, _, item, _) => rendered.Add(item),
            Items = ["a", "b", "c", "d", "e"],
        };
        var buffer = new Buffer(20, 3);

        list.Render(buffer, new Rect(0, 0, 20, 3));

        Assert.Equal(3, rendered.Count);
        Assert.Equal(["a", "b", "c"], rendered);
    }

    [Fact]
    public void Render_DrawsScrollIndicator_WhenItemsOverflow()
    {
        var list = CreateList(["a", "b", "c", "d", "e"]);
        var buffer = new Buffer(20, 3);

        list.Render(buffer, new Rect(0, 0, 20, 3));

        // Down indicator at bottom-right.
        Assert.Equal('▼', buffer.Get(19, 2).Char);
    }

    [Fact]
    public void Render_DrawsUpIndicator_WhenScrolledDown()
    {
        var list = CreateList(["a", "b", "c", "d", "e"]);
        // First render establishes visible count.
        RenderList(list, 20, 3);
        // Now navigate to the end so the list scrolls.
        list.HandleKey(Named(Key.End));

        var buffer = new Buffer(20, 3);
        list.Render(buffer, new Rect(0, 0, 20, 3));

        Assert.Equal('▲', buffer.Get(19, 0).Char);
    }
}
