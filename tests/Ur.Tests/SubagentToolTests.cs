using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Permissions;
using Ur.Tools;

namespace Ur.Tests;

/// <summary>
/// Unit tests for <see cref="SubagentTool"/>, using a mock <see cref="ISubagentRunner"/>
/// to verify the tool's contract without spinning up a real agent loop.
/// </summary>
public sealed class SubagentToolTests
{
    // ─── Helpers ───────────────────────────────────────────────────────

    /// <summary>
    /// Invokes an AIFunction with the given named arguments.
    /// Mirrors the helper in BuiltinToolTests for consistency.
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
    /// A minimal ISubagentRunner that returns a fixed string for every call,
    /// recording the last task it was given for assertion.
    /// </summary>
    private sealed class StubRunner(string response) : ISubagentRunner
    {
        public string? LastTask { get; private set; }

        public Task<string> RunAsync(string task, CancellationToken ct)
        {
            LastTask = task;
            return Task.FromResult(response);
        }
    }

    /// <summary>
    /// A runner that always throws, to verify exception propagation.
    /// </summary>
    private sealed class ThrowingRunner : ISubagentRunner
    {
        public Task<string> RunAsync(string task, CancellationToken ct)
            => throw new InvalidOperationException("runner failed");
    }

    // ─── Name and metadata ─────────────────────────────────────────────

    [Fact]
    public void Name_IsRunSubagent()
    {
        var tool = new SubagentTool(new StubRunner("ok"));
        Assert.Equal("run_subagent", tool.Name);
    }

    [Fact]
    public void ToolName_ConstantMatchesSubagentRunnerExclusionKey()
    {
        // SubagentTool.ToolName is the value that SubagentRunner passes to FilteredCopy
        // to exclude run_subagent from the child registry. Both sides of that contract
        // must agree on the string, or the recursion guard silently breaks. The test
        // verifies the live tool name matches the constant used in production code.
        Assert.Equal("run_subagent", SubagentTool.ToolName);
        Assert.Equal(SubagentTool.ToolName, new SubagentTool(new StubRunner("ok")).Name);
    }

    [Fact]
    public void OperationType_IsRead()
    {
        // Read means "auto-allow when in-workspace" — appropriate here because the
        // subagent does not bypass the permission system; its own tool calls are
        // individually gated, so prompting on the spawn itself adds no security value.
        IToolMeta tool = new SubagentTool(new StubRunner("ok"));
        Assert.Equal(OperationType.Read, tool.OperationType);
    }

    [Fact]
    public void TargetExtractor_ExtractsTaskArgument()
    {
        IToolMeta tool = new SubagentTool(new StubRunner("ok"));
        var result = tool.TargetExtractor.Extract(
            new Dictionary<string, object?> { ["task"] = "summarize X" });
        Assert.Equal("summarize X", result);
    }

    [Fact]
    public void TargetExtractor_FallsBackWhenTaskMissing()
    {
        IToolMeta tool = new SubagentTool(new StubRunner("ok"));
        var result = tool.TargetExtractor.Extract(new Dictionary<string, object?>());
        // Falls back to the TargetExtractors.FromKey default fallback string.
        Assert.Equal("(unknown)", result);
    }

    // ─── Invocation ────────────────────────────────────────────────────

    [Fact]
    public async Task InvokeAsync_PassesTaskStringToRunner()
    {
        var runner = new StubRunner("result text");
        var tool = new SubagentTool(runner);

        await InvokeAsync(tool, ("task", "research how X works"));

        Assert.Equal("research how X works", runner.LastTask);
    }

    [Fact]
    public async Task InvokeAsync_ReturnsRunnerResult()
    {
        var tool = new SubagentTool(new StubRunner("the sub-agent answer"));

        var result = await InvokeAsync(tool, ("task", "any task"));

        Assert.Equal("the sub-agent answer", result);
    }

    [Fact]
    public async Task InvokeAsync_WhenRunnerThrows_ExceptionPropagates()
    {
        // SubagentTool does not catch exceptions from the runner — it lets
        // ToolInvoker's error-result wrapper handle them at the call site.
        var tool = new SubagentTool(new ThrowingRunner());

        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool, ("task", "any task")));
        Assert.Equal("runner failed", ex.Message);
    }

    [Fact]
    public async Task InvokeAsync_WhenTaskArgumentMissing_Throws()
    {
        // The "task" argument is required. Missing it must throw rather than silently
        // pass null to the runner. This guards against schema/implementation drift
        // where the argument key name changes on one side but not the other.
        var tool = new SubagentTool(new StubRunner("ok"));

        await Assert.ThrowsAsync<ArgumentException>(
            () => InvokeAsync(tool  /* no task argument */));
    }
}
