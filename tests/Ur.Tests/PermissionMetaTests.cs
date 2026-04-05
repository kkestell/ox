using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Permissions;

namespace Ur.Tests;

/// <summary>
/// Unit tests for <see cref="PermissionMeta.ResolveTarget"/>. This method
/// encapsulates the argument marshaling that <see cref="AgentLoop"/>
/// uses to build human-readable permission targets. The tests verify the three
/// branching paths: extractor present, extractor null, and non-dictionary arguments.
/// </summary>
public sealed class PermissionMetaTests
{
    [Fact]
    public void ResolveTarget_WithExtractor_ReturnsExtractedValue()
    {
        var meta = new PermissionMeta(
            OperationType.WriteInWorkspace,
            ExtensionId: null,
            TargetExtractor: args => (string)(args["file_path"] ?? ""));

        var call = new FunctionCallContent(
            "call-1", "write_file",
            new Dictionary<string, object?> { ["file_path"] = "/src/foo.cs" });

        Assert.Equal("/src/foo.cs", meta.ResolveTarget(call));
    }

    [Fact]
    public void ResolveTarget_NullExtractor_FallsBackToCallName()
    {
        var meta = new PermissionMeta(
            OperationType.WriteInWorkspace,
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
            OperationType.WriteInWorkspace,
            ExtensionId: null,
            TargetExtractor: args =>
            {
                extractorCalled = true;
                // With null arguments, ResolveTarget provides an empty dictionary
                // wrapped in AIFunctionArguments.
                return args.Count == 0 ? "empty" : "has-args";
            });

        // FunctionCallContent with null arguments — simulates a tool call
        // where the LLM didn't provide any arguments.
        var call = new FunctionCallContent("call-1", "some_tool", arguments: null);

        var result = meta.ResolveTarget(call);
        Assert.True(extractorCalled);
        Assert.Equal("empty", result);
    }
}
