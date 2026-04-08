using System.Text.Json;
using Microsoft.Extensions.AI;
using Te.Rendering;
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

        // Header row + item = at least 2 rows.
        Assert.True(rows.Count >= 2, $"Expected at least 2 rows, got {rows.Count}");
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
        // "○ " prefix is 2 chars. With width=20, content gets 18 chars.
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

        // Width 20, prefix 2 → content width 18. Should produce 2 lines.
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
        // Prefix "○ " is 2 chars. With width=20, content gets 18 chars.
        // An item exactly 18 chars should fit in one line.
        store.Update([new TodoItem("Exactly18charsss!!", TodoStatus.Pending)]);
        var section = new TodoSection(store);

        var rows = section.Render(20);
        // Header "Plan" + item = 2 rows.
        // Only one row should contain the item text (no continuation).
        var itemRows = rows.Where(r => RowToString(r).Contains("Exactly18")).ToList();
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
    public void IsVisible_FalseWhenEmpty()
    {
        var sidebar = new Sidebar();
        Assert.False(sidebar.IsVisible);
    }

    [Fact]
    public void IsVisible_TrueWhenSectionHasContent()
    {
        var sidebar = new Sidebar();
        sidebar.AddSection(new FakeSection(true, [CellRow.FromText("data", Color.Default, Color.Default)]));
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
    // ConsoleBuffer uses (x=col, y=row) — helper extracts a full row as a string.
    private static string RowText(Viewport viewport, int row, int width)
    {
        var chars = new char[width];
        for (var col = 0; col < width; col++)
            chars[col] = viewport._buffer.GetCell(col, row).Rune;
        return new string(chars);
    }

    /// <summary>
    /// Verifies that no │ separator character appears anywhere in the buffer.
    /// </summary>
    private static void AssertNoSeparator(Viewport viewport, int width, int height)
    {
        for (var row = 0; row < height; row++)
        for (var col = 0; col < width; col++)
            Assert.NotEqual('│', viewport._buffer.GetCell(col, row).Rune);
    }

    [Fact]
    public void BuildFrame_NoSidebar_UsesFullWidth()
    {
        var eventList = new EventList();
        var viewport = new Viewport(eventList);

        viewport.BuildFrame(80, 24);

        // No separator should appear anywhere when there's no sidebar.
        AssertNoSeparator(viewport, 80, 24);
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
        viewport.BuildFrame(90, 24);

        // The separator column should be │ (U+2502) on every row.
        // GetCell uses (x=col, y=row).
        var separatorCol = 90 - 30 - 1;
        Assert.Equal('│', viewport._buffer.GetCell(separatorCol, 0).Rune);
        Assert.Equal(Color.BrightBlack, viewport._buffer.GetCell(separatorCol, 0).Foreground);
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

        viewport.BuildFrame(90, 24);

        // Find "Plan" in the sidebar area. The sidebar content starts at
        // leftWidth + 2 (separator + 1-col pad).
        var sidebarContentStart = 90 - 30 + 1;
        var row0Text = RowText(viewport, 0, 90);
        var sidebarText = row0Text[sidebarContentStart..].TrimEnd('\0').TrimEnd();

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
        viewport.BuildFrame(60, 24);
        var separatorCol = 39;
        Assert.Equal('│', viewport._buffer.GetCell(separatorCol, 0).Rune);
    }
}

// ---------------------------------------------------------------------------
// Viewport splash screen
// ---------------------------------------------------------------------------

public sealed class ViewportSplashTests
{
    // ConsoleBuffer uses (x=col, y=row) — helper extracts a full row as a string.
    private static string RowText(Viewport viewport, int row, int width)
    {
        var chars = new char[width];
        for (var col = 0; col < width; col++)
            chars[col] = viewport._buffer.GetCell(col, row).Rune;
        return new string(chars);
    }

    [Fact]
    public void BuildFrame_EmptyConversation_ShowsSplashArt()
    {
        var eventList = new EventList();
        var viewport = new Viewport(eventList);

        viewport.BuildFrame(80, 24);

        // Collect all non-empty text from the conversation area (rows 0 .. height-6).
        var viewportHeight = 24 - 5; // 19 rows
        var allText = string.Concat(
            Enumerable.Range(0, viewportHeight)
                .Select(r => RowText(viewport, r, 80).TrimEnd('\0')));

        Assert.Contains("▒█▀▀▀█", allText);
        Assert.Contains("▒█▄▄▄█", allText);
    }

    [Fact]
    public void BuildFrame_EmptyConversation_SplashIsCenteredVertically()
    {
        var eventList = new EventList();
        var viewport = new Viewport(eventList);

        viewport.BuildFrame(80, 24);

        // The splash is 3 lines tall. Viewport height = 24 - 5 = 19.
        // Centered: startRow = (19 - 3) / 2 = 8.
        var expectedStartRow = (24 - 5 - 3) / 2;

        // The row before the splash should be empty.
        var rowBefore = RowText(viewport, expectedStartRow - 1, 80).TrimEnd('\0').Trim();
        Assert.Empty(rowBefore);

        // The first splash row should contain the first line of the art.
        var splashRow = RowText(viewport, expectedStartRow, 80).TrimEnd('\0').Trim();
        Assert.Contains("▒█▀▀▀█", splashRow);
    }

    [Fact]
    public void BuildFrame_EmptyConversation_SplashIsCenteredHorizontally()
    {
        var eventList = new EventList();
        var viewport = new Viewport(eventList);

        viewport.BuildFrame(80, 24);

        // The splash art is 12 chars wide. Centered in 80 cols: startCol = (80 - 12) / 2 = 34.
        var expectedStartRow = (24 - 5 - 3) / 2;
        var expectedStartCol = (80 - 12) / 2;

        // The cell just before the art should be empty/space.
        // GetCell uses (x=col, y=row) convention.
        Assert.True(
            viewport._buffer.GetCell(expectedStartCol - 1, expectedStartRow).Rune is '\0' or ' ',
            "Cell before splash art should be blank");

        // The first character of the art should be '▒'.
        Assert.Equal('▒', viewport._buffer.GetCell(expectedStartCol, expectedStartRow).Rune);
    }

    [Fact]
    public void BuildFrame_EmptyConversation_SplashIsBrightBlack()
    {
        var eventList = new EventList();
        var viewport = new Viewport(eventList);

        viewport.BuildFrame(80, 24);

        // Find the first '▒' in the buffer and verify its color.
        var expectedStartRow = (24 - 5 - 3) / 2;
        var expectedStartCol = (80 - 12) / 2;

        Assert.Equal(Color.BrightBlack, viewport._buffer.GetCell(expectedStartCol, expectedStartRow).Foreground);
    }

    [Fact]
    public void BuildFrame_NonEmptyConversation_NoSplash()
    {
        var eventList = new EventList();
        var renderable = new TextRenderable();
        renderable.SetText("Hello");
        eventList.Add(renderable);

        var viewport = new Viewport(eventList);
        viewport.BuildFrame(80, 24);

        // The splash characters should not appear anywhere.
        var viewportHeight = 24 - 5;
        var allText = string.Concat(
            Enumerable.Range(0, viewportHeight)
                .Select(r => RowText(viewport, r, 80).TrimEnd('\0')));

        Assert.DoesNotContain("▒█▀▀▀█", allText);
    }
}
