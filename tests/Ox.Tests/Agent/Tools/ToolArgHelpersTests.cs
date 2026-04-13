using System.Text.Json;
using Ox.Agent.Tools;

namespace Ox.Tests.Agent.Tools;

/// <summary>
/// Tests for <see cref="ToolArgHelpers"/>. These cover the argument coercion
/// and output truncation logic shared by all built-in tools. The coercion
/// layer is critical for security — if it silently drops or misinterprets
/// arguments, tools may operate on wrong files or with wrong parameters.
/// The truncation logic protects against OOM from unbounded tool output.
/// </summary>
public sealed class ToolArgHelpersTests
{
    // ─── TruncateOutput ───────────────────────────────────────────────

    [Fact]
    public void TruncateOutput_ShortOutput_ReturnedUnchanged()
    {
        const string output = "line1\nline2\nline3";

        var result = ToolArgHelpers.TruncateOutput(output);

        Assert.Equal("line1\nline2\nline3", result);
    }

    [Fact]
    public void TruncateOutput_ExceedsLineLimit_TruncatedWithMarker()
    {
        var lines = Enumerable.Range(1, 2500).Select(i => $"line {i}");
        var output = string.Join('\n', lines);

        var result = ToolArgHelpers.TruncateOutput(output);

        Assert.Contains("[truncated]", result);
        Assert.Contains("line 1", result);
        Assert.Contains("line 2000", result);
        Assert.DoesNotContain("line 2001", result);
    }

    [Fact]
    public void TruncateOutput_ExceedsByteLimit_TruncatedWithMarker()
    {
        // Create output that fits within the line limit but exceeds the byte limit.
        // Default byte limit is 100KB, so create 50 lines of ~3KB each = ~150KB.
        var longLine = new string('x', 3000);
        var lines = Enumerable.Range(1, 50).Select(_ => longLine);
        var output = string.Join('\n', lines);

        Assert.True(output.Length > 100 * 1024, "Test output should exceed byte limit");

        var result = ToolArgHelpers.TruncateOutput(output);

        Assert.Contains("[truncated]", result);
        Assert.True(result.Length < output.Length, "Truncated output should be shorter");
    }

    [Fact]
    public void TruncateOutput_ExactlyAtLimit_NotTruncated()
    {
        var lines = Enumerable.Range(1, 2000).Select(i => $"L{i}");
        var output = string.Join('\n', lines);

        // Guard: this test only applies when the output fits within the byte limit.
        if (output.Length > 100 * 1024)
            return;

        var result = ToolArgHelpers.TruncateOutput(output);
        Assert.DoesNotContain("[truncated]", result);
    }

    [Fact]
    public void TruncateOutput_CustomLimits_Respected()
    {
        const string output = "alpha\nbeta\ncharlie\ndelta\necho";

        var result = ToolArgHelpers.TruncateOutput(output, maxLines: 3);

        Assert.Contains("[truncated]", result);
        Assert.Contains("alpha", result);
        Assert.Contains("charlie", result);
        // Lines beyond the limit should not appear in the content portion.
        Assert.DoesNotContain("delta", result);
    }

    // ─── ResolvePath ──────────────────────────────────────────────────

    [Fact]
    public void ResolvePath_NullSubPath_ReturnsWorkspaceRoot()
    {
        var result = ToolArgHelpers.ResolvePath("/workspace", null);

        Assert.Equal("/workspace", result);
    }

    [Fact]
    public void ResolvePath_EmptySubPath_ReturnsWorkspaceRoot()
    {
        var result = ToolArgHelpers.ResolvePath("/workspace", "");

        Assert.Equal("/workspace", result);
    }

    [Fact]
    public void ResolvePath_RelativeSubPath_ResolvesAgainstRoot()
    {
        var result = ToolArgHelpers.ResolvePath("/workspace", "src/main.cs");

        Assert.Equal(Path.GetFullPath("/workspace/src/main.cs"), result);
    }

    [Fact]
    public void ResolvePath_AbsoluteSubPath_ReturnsAbsolute()
    {
        var result = ToolArgHelpers.ResolvePath("/workspace", "/tmp/file.txt");

        Assert.Equal(Path.GetFullPath("/tmp/file.txt"), result);
    }

    // ─── GetOptionalInt — type coercion ───────────────────────────────

    [Fact]
    public void GetOptionalInt_IntValue_ReturnsInt()
    {
        var args = MakeArgs(("count", 42));

        Assert.Equal(42, ToolArgHelpers.GetOptionalInt(args, "count"));
    }

    [Fact]
    public void GetOptionalInt_LongValue_ReturnsInt()
    {
        var args = MakeArgs(("count", 42L));

        Assert.Equal(42, ToolArgHelpers.GetOptionalInt(args, "count"));
    }

    [Fact]
    public void GetOptionalInt_DoubleValue_ReturnsInt()
    {
        var args = MakeArgs(("count", 42.0));

        Assert.Equal(42, ToolArgHelpers.GetOptionalInt(args, "count"));
    }

    [Fact]
    public void GetOptionalInt_JsonElement_ReturnsInt()
    {
        // Real LLM args arrive as JsonElement — this tests the actual runtime path.
        using var doc = JsonDocument.Parse("42");
        var args = MakeArgs(("count", doc.RootElement.Clone()));

        Assert.Equal(42, ToolArgHelpers.GetOptionalInt(args, "count"));
    }

    [Fact]
    public void GetOptionalInt_MissingKey_ReturnsNull()
    {
        var args = MakeArgs();

        Assert.Null(ToolArgHelpers.GetOptionalInt(args, "missing"));
    }

    [Fact]
    public void GetOptionalInt_NullValue_ReturnsNull()
    {
        var args = MakeArgs(("count", null));

        Assert.Null(ToolArgHelpers.GetOptionalInt(args, "count"));
    }

    // ─── GetRequiredString — validation ───────────────────────────────

    [Fact]
    public void GetRequiredString_Present_ReturnsValue()
    {
        var args = MakeArgs(("name", "hello"));

        Assert.Equal("hello", ToolArgHelpers.GetRequiredString(args, "name"));
    }

    [Fact]
    public void GetRequiredString_JsonElement_CoercesToString()
    {
        using var doc = JsonDocument.Parse("\"hello\"");
        var args = MakeArgs(("name", doc.RootElement.Clone()));

        Assert.Equal("hello", ToolArgHelpers.GetRequiredString(args, "name"));
    }

    [Fact]
    public void GetRequiredString_Missing_ThrowsArgumentException()
    {
        var args = MakeArgs();

        var ex = Assert.Throws<ArgumentException>(
            () => ToolArgHelpers.GetRequiredString(args, "missing"));

        Assert.Contains("missing", ex.Message);
    }

    [Fact]
    public void GetRequiredString_NullValue_ThrowsArgumentException()
    {
        var args = MakeArgs(("name", null));

        Assert.Throws<ArgumentException>(
            () => ToolArgHelpers.GetRequiredString(args, "name"));
    }

    // ─── GetOptionalString ────────────────────────────────────────────

    [Fact]
    public void GetOptionalString_Present_ReturnsValue()
    {
        var args = MakeArgs(("key", "value"));

        Assert.Equal("value", ToolArgHelpers.GetOptionalString(args, "key"));
    }

    [Fact]
    public void GetOptionalString_Missing_ReturnsNull()
    {
        var args = MakeArgs();

        Assert.Null(ToolArgHelpers.GetOptionalString(args, "missing"));
    }

    // ─── Helpers ──────────────────────────────────────────────────────

    private static Microsoft.Extensions.AI.AIFunctionArguments MakeArgs(
        params (string Key, object? Value)[] entries)
    {
        var dict = new Dictionary<string, object?>();
        foreach (var (key, value) in entries)
            dict[key] = value;
        return new Microsoft.Extensions.AI.AIFunctionArguments(dict);
    }
}
