using System.Text.Json;
using Ox.Agent.Todo;

namespace Ox.Tests.Agent.Todo;

/// <summary>
/// Unit tests for <see cref="TodoJson.Parse"/>. Covers the happy path plus the
/// three validation failures the parser can surface back to the LLM: non-array
/// inputs, missing required fields, and unknown status strings.
/// </summary>
public sealed class TodoJsonTests
{
    [Fact]
    public void Parse_ValidArray_ReturnsItems()
    {
        var json = JsonDocument.Parse("""
            [
              { "content": "first",  "status": "pending" },
              { "content": "second", "status": "in_progress" },
              { "content": "third",  "status": "completed" }
            ]
            """).RootElement;

        var items = TodoJson.Parse(json);

        Assert.Collection(items,
            a => Assert.Equal(("first", TodoStatus.Pending), (a.Content, a.Status)),
            b => Assert.Equal(("second", TodoStatus.InProgress), (b.Content, b.Status)),
            c => Assert.Equal(("third", TodoStatus.Completed), (c.Content, c.Status)));
    }

    [Fact]
    public void Parse_EmptyArray_ReturnsEmpty()
    {
        var json = JsonDocument.Parse("[]").RootElement;
        Assert.Empty(TodoJson.Parse(json));
    }

    [Fact]
    public void Parse_NonArrayInput_Throws()
    {
        // Anything that isn't a JSON array (string, object, number) is invalid.
        var json = JsonDocument.Parse("\"oops\"").RootElement;
        Assert.Throws<ArgumentException>(() => TodoJson.Parse(json));
    }

    [Fact]
    public void Parse_UnknownStatus_Throws()
    {
        var json = JsonDocument.Parse("""[{ "content": "x", "status": "maybe" }]""").RootElement;
        var ex = Assert.Throws<ArgumentException>(() => TodoJson.Parse(json));
        Assert.Contains("maybe", ex.Message);
    }

    [Fact]
    public void Parse_MissingContent_Throws()
    {
        var json = JsonDocument.Parse("""[{ "status": "pending" }]""").RootElement;
        Assert.ThrowsAny<Exception>(() => TodoJson.Parse(json));
    }

    [Fact]
    public void Parse_MissingStatus_Throws()
    {
        var json = JsonDocument.Parse("""[{ "content": "x" }]""").RootElement;
        Assert.ThrowsAny<Exception>(() => TodoJson.Parse(json));
    }
}
