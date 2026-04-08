using System.Text.Json;
using Microsoft.Extensions.AI;
using Ur.Todo;
using Ur.Tools;
using Ox.Rendering;

namespace Ur.Tests;

// ---------------------------------------------------------------------------
// TodoStore
// ---------------------------------------------------------------------------

public sealed class TodoStoreTests
{
    [Fact]
    public void Items_InitiallyEmpty()
    {
        var store = new TodoStore();
        Assert.Empty(store.Items);
    }

    [Fact]
    public void Update_ReplacesEntireList()
    {
        var store = new TodoStore();
        var items = new List<TodoItem>
        {
            new("Task A", TodoStatus.Pending),
            new("Task B", TodoStatus.InProgress)
        };

        store.Update(items);

        Assert.Equal(2, store.Items.Count);
        Assert.Equal("Task A", store.Items[0].Content);
        Assert.Equal(TodoStatus.InProgress, store.Items[1].Status);
    }

    [Fact]
    public void Update_ReplacesPreExistingList()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Old", TodoStatus.Pending)]);
        store.Update([new TodoItem("New", TodoStatus.Completed)]);

        Assert.Single(store.Items);
        Assert.Equal("New", store.Items[0].Content);
        Assert.Equal(TodoStatus.Completed, store.Items[0].Status);
    }

    [Fact]
    public void Update_EmptyList_ClearsItems()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Task", TodoStatus.Pending)]);
        store.Update([]);

        Assert.Empty(store.Items);
    }

    [Fact]
    public void Changed_FiresAfterStateIsUpdated()
    {
        var store = new TodoStore();
        var countAtFireTime = -1;
        // Capture the item count inside the handler to verify the event
        // fires after the store's internal state is updated (not before).
        store.Changed += () => countAtFireTime = store.Items.Count;

        store.Update([new TodoItem("Task", TodoStatus.Pending)]);

        Assert.Equal(1, countAtFireTime);
    }

    [Fact]
    public void Changed_FiresOnEmptyUpdate()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Task", TodoStatus.Pending)]);

        var fired = false;
        store.Changed += () => fired = true;
        store.Update([]);

        Assert.True(fired);
    }
}

// ---------------------------------------------------------------------------
// TodoWriteTool
// ---------------------------------------------------------------------------

public sealed class TodoWriteToolTests
{
    /// <summary>
    /// Invokes an AIFunction with the given named arguments.
    /// </summary>
    private static async Task<object?> InvokeAsync(
        AIFunction tool,
        params (string Key, object? Value)[] args)
    {
        var dict = new Dictionary<string, object?>();
        foreach (var (key, value) in args)
            dict[key] = value;
        return await tool.InvokeAsync(new AIFunctionArguments(dict));
    }

    /// <summary>
    /// Builds a JSON array of todo items for tool invocation.
    /// </summary>
    private static JsonElement MakeTodosJson(params (string Content, string Status)[] items)
    {
        var array = items.Select(i => new { content = i.Content, status = i.Status });
        return JsonSerializer.SerializeToElement(array);
    }

    [Fact]
    public async Task ValidInput_UpdatesStore()
    {
        var store = new TodoStore();
        var tool = new TodoWriteTool(store);
        var todos = MakeTodosJson(
            ("Read config", "completed"),
            ("Implement feature", "in_progress"),
            ("Write tests", "pending"));

        var result = (string?)await InvokeAsync(tool, ("todos", todos));

        Assert.Contains("3 items", result);
        Assert.Equal(3, store.Items.Count);
        Assert.Equal(TodoStatus.Completed, store.Items[0].Status);
        Assert.Equal(TodoStatus.InProgress, store.Items[1].Status);
        Assert.Equal(TodoStatus.Pending, store.Items[2].Status);
    }

    [Fact]
    public async Task EmptyList_ClearsStore()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Existing", TodoStatus.Pending)]);
        var tool = new TodoWriteTool(store);
        var todos = MakeTodosJson();

        var result = (string?)await InvokeAsync(tool, ("todos", todos));

        Assert.Contains("0 items", result);
        Assert.Empty(store.Items);
    }

    [Fact]
    public async Task NullStore_ReturnsErrorMessage()
    {
        var tool = new TodoWriteTool(null);
        var todos = MakeTodosJson(("Task", "pending"));

        var result = (string?)await InvokeAsync(tool, ("todos", todos));

        Assert.Contains("not available", result);
    }

    [Fact]
    public async Task MissingTodos_ReturnsErrorMessage()
    {
        var store = new TodoStore();
        var tool = new TodoWriteTool(store);

        var result = (string?)await InvokeAsync(tool);

        Assert.Contains("Missing required parameter", result);
    }

    [Fact]
    public async Task InvalidStatus_Throws()
    {
        var store = new TodoStore();
        var tool = new TodoWriteTool(store);
        var todos = MakeTodosJson(("Task", "invalid_status"));

        await Assert.ThrowsAsync<ArgumentException>(
            () => InvokeAsync(tool, ("todos", todos)));
    }

    [Fact]
    public async Task NonJsonElement_Throws()
    {
        // Documents the contract: ParseTodos only accepts JsonElement arrays.
        // If a framework change or test passes a plain List, it should fail
        // with ArgumentException rather than silently misbehaving.
        var store = new TodoStore();
        var tool = new TodoWriteTool(store);
        var plainList = new List<object> { "not a JsonElement" };

        await Assert.ThrowsAsync<ArgumentException>(
            () => InvokeAsync(tool, ("todos", plainList)));
    }

    [Fact]
    public void ToolName_IsTodoWrite()
    {
        var tool = new TodoWriteTool(null);
        Assert.Equal("todo_write", tool.Name);
    }
}

// ---------------------------------------------------------------------------
// TodoSection
// ---------------------------------------------------------------------------

public sealed class TodoSectionTests
{
    [Fact]
    public void HasContent_FalseWhenEmpty()
    {
        var store = new TodoStore();
        var section = new TodoSection(store);

        Assert.False(section.HasContent);
    }

    [Fact]
    public void HasContent_TrueWhenItemsExist()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Task", TodoStatus.Pending)]);
        var section = new TodoSection(store);

        Assert.True(section.HasContent);
    }

    [Fact]
    public void Render_Empty_ReturnsNoRows()
    {
        var store = new TodoStore();
        var section = new TodoSection(store);

        var rows = section.Render(30);

        Assert.Empty(rows);
    }

    [Fact]
    public void Render_SingleItem_RendersHeaderAndItem()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Do something", TodoStatus.Pending)]);
        var section = new TodoSection(store);

        var rows = section.Render(30);

        // Header row: "Plan"
        Assert.True(rows.Count >= 3, $"Expected at least 3 rows, got {rows.Count}");
        Assert.Contains("Plan", RowToString(rows[0]));
    }

    [Fact]
    public void Render_MixedStatuses_ShowsCorrectIndicators()
    {
        var store = new TodoStore();
        store.Update([
            new TodoItem("Done task", TodoStatus.Completed),
            new TodoItem("Active task", TodoStatus.InProgress),
            new TodoItem("Waiting task", TodoStatus.Pending)
        ]);
        var section = new TodoSection(store);

        var rows = section.Render(40);
        var text = string.Join("\n", rows.Select(RowToString));

        // Check that all items appear in the output.
        Assert.Contains("Done task", text);
        Assert.Contains("Active task", text);
        Assert.Contains("Waiting task", text);

        // Check the summary line.
        Assert.Contains("1/3 completed", text);
    }

    [Fact]
    public void Render_CompletedItem_UsesGreenColor()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Finished", TodoStatus.Completed)]);
        var section = new TodoSection(store);

        var rows = section.Render(30);
        // Find the row with "Finished" — should have green foreground.
        var itemRow = rows.First(r => RowToString(r).Contains("Finished"));
        var firstContentCell = itemRow.Cells.First(c => c.Rune == 'F');
        Assert.Equal(Color.Green, firstContentCell.Foreground);
    }

    [Fact]
    public void Render_InProgressItem_UsesYellowColor()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Working", TodoStatus.InProgress)]);
        var section = new TodoSection(store);

        var rows = section.Render(30);
        var itemRow = rows.First(r => RowToString(r).Contains("Working"));
        var firstContentCell = itemRow.Cells.First(c => c.Rune == 'W');
        Assert.Equal(Color.Yellow, firstContentCell.Foreground);
    }

    [Fact]
    public void Render_ProgressSummary_AtBottom()
    {
        var store = new TodoStore();
        store.Update([
            new TodoItem("A", TodoStatus.Completed),
            new TodoItem("B", TodoStatus.Completed),
            new TodoItem("C", TodoStatus.Pending)
        ]);
        var section = new TodoSection(store);

        var rows = section.Render(30);
        var lastRow = RowToString(rows[^1]);

        Assert.Contains("2/3 completed", lastRow);
    }

    [Fact]
    public void Changed_FiresWhenStoreUpdates()
    {
        var store = new TodoStore();
        var section = new TodoSection(store);
        var fired = false;
        section.Changed += () => fired = true;

        store.Update([new TodoItem("New task", TodoStatus.Pending)]);

        Assert.True(fired);
    }

    [Fact]
    public void Render_LongItem_WordWrapsAtSpace()
    {
        var store = new TodoStore();
        // "  ○ " prefix is 4 chars. With width=20, content gets 16 chars.
        // "Short words wrap nicely here" is 28 chars, should wrap.
        store.Update([new TodoItem("Short words wrap nicely here", TodoStatus.Pending)]);
        var section = new TodoSection(store);

        var rows = section.Render(20);
        var text = string.Join("\n", rows.Select(RowToString));

        // The content should span multiple lines without truncation.
        Assert.Contains("Short words wrap", text);
        Assert.Contains("nicely here", text);
    }

    [Fact]
    public void Render_LongWordWithoutSpaces_HardBreaks()
    {
        var store = new TodoStore();
        // A single 30-char word with no spaces — must hard-break.
        var longWord = new string('x', 30);
        store.Update([new TodoItem(longWord, TodoStatus.Pending)]);
        var section = new TodoSection(store);

        // Width 20, prefix 4 → content width 16. Should produce 2 lines.
        var rows = section.Render(20);
        var text = string.Join("", rows.Select(RowToString));

        // All 30 x's should appear in the output (no truncation).
        var xCount = text.Count(c => c == 'x');
        Assert.Equal(30, xCount);
    }

    [Fact]
    public void Render_ItemExactlyFitsWidth_NoWrap()
    {
        var store = new TodoStore();
        // Prefix "  ○ " is 4 chars. With width=20, content gets 16 chars.
        // An item exactly 16 chars should fit in one line.
        store.Update([new TodoItem("Exactly16chars!!", TodoStatus.Pending)]);
        var section = new TodoSection(store);

        var rows = section.Render(20);
        // Header "Plan" + blank + item + blank + summary = 5 rows minimum.
        // Only one row should contain the item text (no continuation).
        var itemRows = rows.Where(r => RowToString(r).Contains("Exactly16")).ToList();
        Assert.Single(itemRows);
    }

    /// <summary>Converts a CellRow to a plain string for assertions.</summary>
    private static string RowToString(CellRow row) =>
        new(row.Cells.Select(c => c.Rune).ToArray());
}

// ---------------------------------------------------------------------------
// Sidebar
// ---------------------------------------------------------------------------

public sealed class SidebarTests
{
    /// <summary>Minimal ISidebarSection for testing.</summary>
    private sealed class FakeSection(bool hasContent, IReadOnlyList<CellRow>? rows = null) : ISidebarSection
    {
        public bool HasContent => hasContent;
        public event Action? Changed;
        public IReadOnlyList<CellRow> Render(int availableWidth) =>
            rows ?? [];

        public void TriggerChange() => Changed?.Invoke();
    }

    [Fact]
    public void IsVisible_FalseWhenNoSections()
    {
        var sidebar = new Sidebar();
        Assert.False(sidebar.IsVisible);
    }

    [Fact]
    public void IsVisible_FalseWhenAllSectionsEmpty()
    {
        var sidebar = new Sidebar();
        sidebar.AddSection(new FakeSection(false));
        sidebar.AddSection(new FakeSection(false));

        Assert.False(sidebar.IsVisible);
    }

    [Fact]
    public void IsVisible_TrueWhenAnySectionHasContent()
    {
        var sidebar = new Sidebar();
        sidebar.AddSection(new FakeSection(false));
        sidebar.AddSection(new FakeSection(true));

        Assert.True(sidebar.IsVisible);
    }

    [Fact]
    public void Render_SkipsEmptySections()
    {
        var sidebar = new Sidebar();
        sidebar.AddSection(new FakeSection(false, [CellRow.FromText("hidden", Color.Default, Color.Default)]));
        sidebar.AddSection(new FakeSection(true, [CellRow.FromText("visible", Color.Default, Color.Default)]));

        var rows = sidebar.Render(20);

        Assert.Single(rows);
        Assert.Equal('v', rows[0].Cells[0].Rune);
    }

    [Fact]
    public void Changed_FiresWhenSectionChanges()
    {
        var sidebar = new Sidebar();
        var section = new FakeSection(true);
        sidebar.AddSection(section);

        var fired = false;
        sidebar.Changed += () => fired = true;

        section.TriggerChange();

        Assert.True(fired);
    }

    [Fact]
    public void Render_CombinesMultipleSections()
    {
        var sidebar = new Sidebar();
        sidebar.AddSection(new FakeSection(true, [CellRow.FromText("A", Color.Default, Color.Default)]));
        sidebar.AddSection(new FakeSection(true, [CellRow.FromText("B", Color.Default, Color.Default)]));

        var rows = sidebar.Render(20);

        Assert.Equal(2, rows.Count);
        Assert.Equal('A', rows[0].Cells[0].Rune);
        Assert.Equal('B', rows[1].Cells[0].Rune);
    }
}

// ---------------------------------------------------------------------------
// Viewport sidebar layout
// ---------------------------------------------------------------------------

public sealed class ViewportSidebarTests
{
    private static string RowText(ScreenBuffer buffer, int row, int width)
    {
        var chars = new char[width];
        for (var col = 0; col < width; col++)
            chars[col] = buffer[row, col].Rune;
        return new string(chars);
    }

    /// <summary>
    /// Verifies that no │ separator character appears anywhere in the buffer.
    /// </summary>
    private static void AssertNoSeparator(ScreenBuffer buffer, int width, int height)
    {
        for (var row = 0; row < height; row++)
        for (var col = 0; col < width; col++)
            Assert.NotEqual('│', buffer[row, col].Rune);
    }

    [Fact]
    public void BuildFrame_NoSidebar_UsesFullWidth()
    {
        var eventList = new EventList();
        var viewport = new Viewport(eventList);
        viewport.SetSessionId("test-session");

        var buffer = viewport.BuildFrame(80, 24);
        var headerText = RowText(buffer, 0, 80).TrimEnd('\0').TrimEnd();

        Assert.StartsWith("test-session", headerText);
        // No separator should appear anywhere when there's no sidebar.
        AssertNoSeparator(buffer, 80, 24);
    }

    [Fact]
    public void BuildFrame_HiddenSidebar_UsesFullWidth()
    {
        // Sidebar with no content should not affect layout.
        var store = new TodoStore();
        var section = new TodoSection(store);
        var sidebar = new Sidebar();
        sidebar.AddSection(section);

        var eventList = new EventList();
        var viewport = new Viewport(eventList, sidebar);
        viewport.SetSessionId("test-session");

        var buffer = viewport.BuildFrame(80, 24);
        var headerText = RowText(buffer, 0, 80).TrimEnd('\0').TrimEnd();

        Assert.StartsWith("test-session", headerText);
        // No separator should appear when the sidebar is hidden.
        AssertNoSeparator(buffer, 80, 24);
    }

    [Fact]
    public void BuildFrame_VisibleSidebar_DrawsSeparator()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Task", TodoStatus.Pending)]);
        var section = new TodoSection(store);
        var sidebar = new Sidebar();
        sidebar.AddSection(section);

        var eventList = new EventList();
        var viewport = new Viewport(eventList, sidebar);

        // Use a 90-wide terminal. Sidebar gets min(36, 90/3)=30.
        // Separator at column 90-30-1=59.
        var buffer = viewport.BuildFrame(90, 24);

        // The separator column should be │ (U+2502) on every row.
        var separatorCol = 90 - 30 - 1;
        Assert.Equal('│', buffer[0, separatorCol].Rune);
        Assert.Equal(Color.BrightBlack, buffer[0, separatorCol].Foreground);
    }

    [Fact]
    public void BuildFrame_VisibleSidebar_RendersTodoContent()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("My task", TodoStatus.Pending)]);
        var section = new TodoSection(store);
        var sidebar = new Sidebar();
        sidebar.AddSection(section);

        var eventList = new EventList();
        var viewport = new Viewport(eventList, sidebar);

        var buffer = viewport.BuildFrame(90, 24);

        // Find "Plan" in the sidebar area. The sidebar content starts at
        // leftWidth + 1 (after the separator).
        var sidebarStart = 90 - 30;
        var row0Text = RowText(buffer, 0, 90);
        var sidebarText = row0Text[sidebarStart..].TrimEnd('\0').TrimEnd();

        Assert.Equal("Plan", sidebarText);
    }

    [Fact]
    public void BuildFrame_NarrowTerminal_SidebarCappedToOneThird()
    {
        var store = new TodoStore();
        store.Update([new TodoItem("Task", TodoStatus.Pending)]);
        var section = new TodoSection(store);
        var sidebar = new Sidebar();
        sidebar.AddSection(section);

        var eventList = new EventList();
        var viewport = new Viewport(eventList, sidebar);

        // 60-wide terminal. Sidebar = min(36, 60/3) = 20.
        // Left column = 60 - 20 - 1 = 39.
        var buffer = viewport.BuildFrame(60, 24);
        var separatorCol = 39;
        Assert.Equal('│', buffer[0, separatorCol].Rune);
    }
}
