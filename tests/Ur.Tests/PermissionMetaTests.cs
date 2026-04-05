using System.Text.Json;
using Microsoft.Extensions.AI;
using Ur.Permissions;
using Ur.Tools;

namespace Ur.Tests;

/// <summary>
/// Unit tests for <see cref="PermissionMeta.ResolveTarget"/>. This method
/// encapsulates the argument marshaling that <see cref="AgentLoop"/>
/// uses to build human-readable permission targets. The tests verify the three
/// branching paths: extractor present, extractor null, and non-dictionary arguments.
/// </summary>
public sealed class PermissionMetaTests
{
    /// <summary>
    /// Test helper that adapts a lambda into <see cref="ITargetExtractor"/> so tests
    /// can construct extractors inline without defining a class per test case.
    /// </summary>
    private sealed class LambdaExtractor(Func<IReadOnlyDictionary<string, object?>, string> fn) : ITargetExtractor
    {
        public string Extract(IReadOnlyDictionary<string, object?> arguments) => fn(arguments);
    }

    [Fact]
    public void ResolveTarget_WithExtractor_ReturnsExtractedValue()
    {
        var meta = new PermissionMeta(
            OperationType.Write,
            ExtensionId: null,
            TargetExtractor: TargetExtractors.FromKey("file_path"));

        var call = new FunctionCallContent(
            "call-1", "write_file",
            new Dictionary<string, object?> { ["file_path"] = "/src/foo.cs" });

        Assert.Equal("/src/foo.cs", meta.ResolveTarget(call));
    }

    [Fact]
    public void ResolveTarget_NullExtractor_FallsBackToCallName()
    {
        var meta = new PermissionMeta(
            OperationType.Write,
            ExtensionId: null,
            TargetExtractor: null);

        var call = new FunctionCallContent(
            "call-1", "write_file",
            new Dictionary<string, object?> { ["file_path"] = "/src/foo.cs" });

        Assert.Equal("write_file", meta.ResolveTarget(call));
    }

    [Fact]
    public void ResolveTarget_NullArguments_FallsBackGracefully()
    {
        var extractorCalled = false;
        var meta = new PermissionMeta(
            OperationType.Write,
            ExtensionId: null,
            TargetExtractor: new LambdaExtractor(args =>
            {
                extractorCalled = true;
                // With null arguments, ResolveTarget provides an empty dictionary.
                return args.Count == 0 ? "empty" : "has-args";
            }));

        // FunctionCallContent with null arguments — simulates a tool call
        // where the LLM didn't provide any arguments.
        var call = new FunctionCallContent("call-1", "some_tool", arguments: null);

        var result = meta.ResolveTarget(call);
        Assert.True(extractorCalled);
        Assert.Equal("empty", result);
    }

    [Fact]
    public void ResolveTarget_MissingKey_UsesDefaultFallback()
    {
        // TargetExtractors.FromKey falls back to "(unknown)" when the key is absent
        // from the arguments dictionary — verifies the default fallback path.
        var meta = new PermissionMeta(
            OperationType.Write,
            ExtensionId: null,
            TargetExtractor: TargetExtractors.FromKey("file_path"));

        var call = new FunctionCallContent(
            "call-1", "write_file",
            new Dictionary<string, object?> { ["other_key"] = "value" });

        Assert.Equal("(unknown)", meta.ResolveTarget(call));
    }

    [Fact]
    public void ResolveTarget_MissingKey_UsesCustomFallback()
    {
        var meta = new PermissionMeta(
            OperationType.Write,
            ExtensionId: null,
            TargetExtractor: TargetExtractors.FromKey("file_path", fallback: "unnamed file"));

        var call = new FunctionCallContent(
            "call-1", "write_file",
            new Dictionary<string, object?>());

        Assert.Equal("unnamed file", meta.ResolveTarget(call));
    }

    [Fact]
    public void ResolveTarget_JsonElementArgument_ExtractsCorrectly()
    {
        // Real LLM arguments arrive as JsonElement, not plain strings.
        // Verifies that KeyExtractor correctly handles JsonElement values
        // by delegating to ToolArgHelpers coercion logic.
        var meta = new PermissionMeta(
            OperationType.Write,
            ExtensionId: null,
            TargetExtractor: TargetExtractors.FromKey("file_path"));

        using var doc = JsonDocument.Parse("""{"file_path": "/data/file.txt"}""");
        var dict = new Dictionary<string, object?>
        {
            ["file_path"] = doc.RootElement.GetProperty("file_path")
        };

        var call = new FunctionCallContent("call-1", "write_file", dict);
        Assert.Equal("/data/file.txt", meta.ResolveTarget(call));
    }
}
