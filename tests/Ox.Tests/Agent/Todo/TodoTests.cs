using System.Text.Json;
using Microsoft.Extensions.AI;
using Ox.App.Views;
using Ox.Agent.AgentLoop;
using Ox.Agent.Todo;
using Ox.Agent.Tools;

namespace Ox.Tests.Agent.Todo;

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

    [Fact]
    public void ToolCallStarted_FormatCall_RendersPlanItemsInsteadOfRawJson()
    {
        var evt = new ToolCallStarted
        {
            CallId = "call-1",
            ToolName = "todo_write",
            Arguments = new Dictionary<string, object?>
            {
                ["todos"] = MakeTodosJson(
                    ("Initial setup", "completed"),
                    ("Feature development", "in_progress"),
                    ("Integration testing", "pending"))
            }
        };

        Assert.Equal(
            "Plan\n✓ Initial setup\n● Feature development\n○ Integration testing",
            evt.FormatCall());
    }

    [Fact]
    public void ToolCallStarted_FormatCall_EmptyTodos_RendersClearedState()
    {
        var evt = new ToolCallStarted
        {
            CallId = "call-2",
            ToolName = "todo_write",
            Arguments = new Dictionary<string, object?>
            {
                ["todos"] = MakeTodosJson()
            }
        };

        Assert.Equal("Plan\n(cleared)", evt.FormatCall());
    }
}

// --- Word wrap tests (ported from old TUI rendering tests) ---
// Tests the ConversationView.WrapText helper which is the shared word-wrap
// algorithm used by conversation entries and plan rendering.

public sealed class WordWrapTests
{
    [Fact]
    public void WrapText_ShortText_SingleLine()
    {
        var result = TextLayout.WrapText("Hello", 20);
        Assert.Single(result);
        Assert.Equal("Hello", result[0]);
    }

    [Fact]
    public void WrapText_ExactFit_SingleLine()
    {
        var result = TextLayout.WrapText("12345", 5);
        Assert.Single(result);
        Assert.Equal("12345", result[0]);
    }

    [Fact]
    public void WrapText_BreaksAtSpace()
    {
        var result = TextLayout.WrapText("Hello world", 7);
        Assert.Equal(2, result.Count);
        Assert.Equal("Hello", result[0]);
        Assert.Equal("world", result[1]);
    }

    [Fact]
    public void WrapText_HardBreak_WhenNoSpace()
    {
        var result = TextLayout.WrapText("abcdefghij", 5);
        Assert.Equal(2, result.Count);
        Assert.Equal("abcde", result[0]);
        Assert.Equal("fghij", result[1]);
    }

    [Fact]
    public void WrapText_NewlinesRespected()
    {
        var result = TextLayout.WrapText("Line1\nLine2", 20);
        Assert.Equal(2, result.Count);
        Assert.Equal("Line1", result[0]);
        Assert.Equal("Line2", result[1]);
    }

    [Fact]
    public void WrapText_TrailingNewlineTrimmed()
    {
        var result = TextLayout.WrapText("Hello\n", 20);
        Assert.Single(result);
        Assert.Equal("Hello", result[0]);
    }

    [Fact]
    public void WrapText_EmptyString_SingleEmptyLine()
    {
        var result = TextLayout.WrapText("", 20);
        Assert.Single(result);
        Assert.Equal("", result[0]);
    }

    [Fact]
    public void WrapText_BlankLine_PreservedAsEmptyRow()
    {
        var result = TextLayout.WrapText("A\n\nB", 20);
        Assert.Equal(3, result.Count);
        Assert.Equal("A", result[0]);
        Assert.Equal("", result[1]);
        Assert.Equal("B", result[2]);
    }

    [Fact]
    public void WrapText_SpaceAtBoundary_SplitsCleanly()
    {
        // "hello world" at width 5: char at position 5 is ' ', should split there.
        var result = TextLayout.WrapText("hello world", 5);
        Assert.Equal(2, result.Count);
        Assert.Equal("hello", result[0]);
        Assert.Equal("world", result[1]);
    }
}
