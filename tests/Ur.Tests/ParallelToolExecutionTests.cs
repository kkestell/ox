using System.Collections.Concurrent;
using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging.Abstractions;
using Ur.AgentLoop;
using Ur.Permissions;
using Ur.Tools;
using AgentLoopClass = Ur.AgentLoop.AgentLoop;

namespace Ur.Tests;

/// <summary>
/// Tests that verify parallel tool execution when the LLM returns multiple tool
/// calls in a single response. Covers concurrent dispatch, event ordering, permission
/// categorization (auto-allowed, granted, denied), and result assembly.
/// </summary>
public sealed class ParallelToolExecutionTests : IDisposable
{
    private readonly string _workspaceRoot = Path.Combine(
        Path.GetTempPath(), "ur-parallel-tests", Guid.NewGuid().ToString("N"));

    public ParallelToolExecutionTests()
    {
        Directory.CreateDirectory(_workspaceRoot);
    }

    public void Dispose()
    {
        if (Directory.Exists(_workspaceRoot))
            Directory.Delete(_workspaceRoot, recursive: true);
    }

    private AgentLoopClass MakeLoop(IChatClient client, ToolRegistry tools) =>
        new(client, tools, new Workspace(_workspaceRoot), NullLogger<AgentLoopClass>.Instance, NullLoggerFactory.Instance);

    /// <summary>
    /// Builds a file_path argument pointing inside the workspace so that
    /// Read + in-workspace auto-allow logic triggers correctly.
    /// </summary>
    private Dictionary<string, object?> InWorkspaceArgs() =>
        new() { ["file_path"] = Path.Combine(_workspaceRoot, "test.txt") };

    // -------------------------------------------------------------------------
    // Multiple auto-allowed tools execute concurrently
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_MultipleAutoAllowedTools_AllExecuteAndProduceEvents()
    {
        var tools = new ToolRegistry();
        tools.Register(MakeSlowTool("tool_a", "first", delayMs: 100), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));
        tools.Register(MakeSlowTool("tool_b", "second", delayMs: 100), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));

        // Both tools are Read + in-workspace → auto-allowed, no callback needed.
        var client = new MultiToolCallingClient(
            ("tool_a", InWorkspaceArgs()),
            ("tool_b", InWorkspaceArgs()));
        var loop = MakeLoop(client, tools);

        var events = await CollectEventsAsync(loop.RunTurnAsync([], callbacks: null));

        // Both tools should have Started and Completed events.
        var started = events.OfType<ToolCallStarted>().ToList();
        var completed = events.OfType<ToolCallCompleted>().ToList();
        Assert.Equal(2, started.Count);
        Assert.Equal(2, completed.Count);
        Assert.Contains(completed, c => c.ToolName == "tool_a" && c.Result == "first");
        Assert.Contains(completed, c => c.ToolName == "tool_b" && c.Result == "second");
    }

    // -------------------------------------------------------------------------
    // Concurrent tools actually run in parallel (structural proof, not wall-clock)
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_ConcurrentTools_RunInParallel()
    {
        // Use timestamps to prove both tools were executing concurrently:
        // both must start before either finishes. This is a structural proof
        // that is deterministic regardless of machine load.
        var startTimes = new ConcurrentDictionary<string, long>();
        var endTimes = new ConcurrentDictionary<string, long>();

        var tools = new ToolRegistry();
        tools.Register(MakeTimedTool("tool_a", "a", delayMs: 100, startTimes, endTimes), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));
        tools.Register(MakeTimedTool("tool_b", "b", delayMs: 100, startTimes, endTimes), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));

        var client = new MultiToolCallingClient(
            ("tool_a", InWorkspaceArgs()),
            ("tool_b", InWorkspaceArgs()));
        var loop = MakeLoop(client, tools);

        var events = await CollectEventsAsync(loop.RunTurnAsync([], callbacks: null));

        var completed = events.OfType<ToolCallCompleted>().ToList();
        Assert.Equal(2, completed.Count);

        // Structural proof: both tools must have started before either completed.
        // If they ran sequentially, tool_b's start time would be after tool_a's end time.
        var earliestEnd = Math.Min(endTimes["tool_a"], endTimes["tool_b"]);
        Assert.True(startTimes["tool_a"] < earliestEnd,
            "tool_a should have started before the earliest tool finished");
        Assert.True(startTimes["tool_b"] < earliestEnd,
            "tool_b should have started before the earliest tool finished");
    }

    // -------------------------------------------------------------------------
    // Mix of auto-allowed and denied (no callback) tools
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_MixedAutoAllowedAndDenied_AutoAllowedExecutesDeniedReportsError()
    {
        var tools = new ToolRegistry();
        // tool_read is Read + in-workspace → auto-allowed.
        tools.Register(MakeTool("tool_read", "read result"), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));
        // tool_write is Write (default) → needs approval.
        tools.Register(MakeTool("tool_write", "write result"));

        var client = new MultiToolCallingClient(
            ("tool_read", InWorkspaceArgs()),
            ("tool_write", null));
        var loop = MakeLoop(client, tools);

        // No callbacks → write is auto-denied, read is auto-allowed.
        var events = await CollectEventsAsync(loop.RunTurnAsync([], callbacks: null));

        var completed = events.OfType<ToolCallCompleted>().ToList();
        Assert.Equal(2, completed.Count);

        var readCompleted = completed.Single(c => c.ToolName == "tool_read");
        Assert.Equal("read result", readCompleted.Result);
        Assert.False(readCompleted.IsError);

        var writeCompleted = completed.Single(c => c.ToolName == "tool_write");
        Assert.Equal("Permission denied.", writeCompleted.Result);
        Assert.True(writeCompleted.IsError);
    }

    // -------------------------------------------------------------------------
    // Approval-required tool that is granted executes concurrently
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_ApprovalGranted_ToolExecutes()
    {
        var tools = new ToolRegistry();
        tools.Register(MakeTool("tool_write", "wrote it"));

        var client = new MultiToolCallingClient(("tool_write", null));
        var loop = MakeLoop(client, tools);

        var grantCallbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Once))
        };

        var events = await CollectEventsAsync(loop.RunTurnAsync([], grantCallbacks));

        var completed = events.OfType<ToolCallCompleted>().Single();
        Assert.Equal("wrote it", completed.Result);
        Assert.False(completed.IsError);
    }

    // -------------------------------------------------------------------------
    // ToolAwaitingApproval event is emitted with correct CallId
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_ApprovalRequired_EmitsToolAwaitingApprovalWithCorrectCallId()
    {
        var tools = new ToolRegistry();
        tools.Register(MakeTool("tool_write", "wrote it"));

        var client = new MultiToolCallingClient(("tool_write", null));
        var loop = MakeLoop(client, tools);

        var grantCallbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Once))
        };

        var events = await CollectEventsAsync(loop.RunTurnAsync([], grantCallbacks));

        // ToolAwaitingApproval should appear between ToolCallStarted and ToolCallCompleted,
        // and its CallId must match the corresponding ToolCallStarted.
        var started = events.OfType<ToolCallStarted>().Single(s => s.ToolName == "tool_write");
        var awaiting = events.OfType<ToolAwaitingApproval>().Single();
        var completed = events.OfType<ToolCallCompleted>().Single(c => c.ToolName == "tool_write");

        // Verify CallId identity — EventRouter uses this to look up the correct renderable.
        Assert.Equal(started.CallId, awaiting.CallId);

        // Verify ordering: Started → AwaitingApproval → Completed.
        var startedIdx = events.IndexOf(started);
        var awaitingIdx = events.IndexOf(awaiting);
        var completedIdx = events.IndexOf(completed);
        Assert.True(startedIdx < awaitingIdx, "ToolAwaitingApproval must come after ToolCallStarted");
        Assert.True(awaitingIdx < completedIdx, "ToolAwaitingApproval must come before ToolCallCompleted");
    }

    // -------------------------------------------------------------------------
    // Denied approval-required tool among auto-allowed tools
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_DeniedAmongAutoAllowed_OtherToolsStillRun()
    {
        var tools = new ToolRegistry();
        // Auto-allowed read.
        tools.Register(MakeTool("tool_read", "read result"), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));
        // Needs approval — will be denied.
        tools.Register(MakeTool("tool_write", "write result"));

        var client = new MultiToolCallingClient(
            ("tool_read", InWorkspaceArgs()),
            ("tool_write", null));
        var loop = MakeLoop(client, tools);

        var denyCallbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(false, null))
        };

        var events = await CollectEventsAsync(loop.RunTurnAsync([], denyCallbacks));

        var completed = events.OfType<ToolCallCompleted>().ToList();
        Assert.Equal(2, completed.Count);

        // Read tool executed normally despite the write being denied.
        var readCompleted = completed.Single(c => c.ToolName == "tool_read");
        Assert.Equal("read result", readCompleted.Result);
        Assert.False(readCompleted.IsError);

        var writeCompleted = completed.Single(c => c.ToolName == "tool_write");
        Assert.Equal("Permission denied.", writeCompleted.Result);
        Assert.True(writeCompleted.IsError);
    }

    // -------------------------------------------------------------------------
    // Result message contains all results in original call order
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_ResultsInCorrectCallOrder()
    {
        var tools = new ToolRegistry();
        // Register three auto-allowed tools with different delays to ensure
        // they complete out of order, but results in resultMessage must be
        // ordered by the original call position.
        tools.Register(MakeSlowTool("tool_c", "third", delayMs: 50), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));
        tools.Register(MakeSlowTool("tool_a", "first", delayMs: 150), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));
        tools.Register(MakeSlowTool("tool_b", "second", delayMs: 100), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));

        // Call order: tool_c (call-0), tool_a (call-1), tool_b (call-2)
        var client = new MultiToolCallingClient(
            ("tool_c", InWorkspaceArgs()),
            ("tool_a", InWorkspaceArgs()),
            ("tool_b", InWorkspaceArgs()));
        var loop = MakeLoop(client, tools);

        // Pass a messages list so we can inspect the toolResultMessage that
        // AgentLoop adds after InvokeAllAsync completes.
        var messages = new List<ChatMessage>();
        await CollectEventsAsync(loop.RunTurnAsync(messages, callbacks: null));

        // Find the Tool-role message — it's the one containing FunctionResultContent.
        var toolMsg = messages.Single(m => m.Role == ChatRole.Tool);
        var results = toolMsg.Contents.OfType<FunctionResultContent>().ToList();
        Assert.Equal(3, results.Count);

        // Verify results are in the original call order (call-0, call-1, call-2),
        // not in completion order (tool_c finishes first, then tool_b, then tool_a).
        Assert.Equal("call-0", results[0].CallId); // tool_c
        Assert.Equal("call-1", results[1].CallId); // tool_a
        Assert.Equal("call-2", results[2].CallId); // tool_b
    }

    // -------------------------------------------------------------------------
    // Multiple approval-required tools are prompted serially (not concurrently)
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_MultipleApprovalRequired_PromptsSerially()
    {
        var tools = new ToolRegistry();
        tools.Register(MakeSlowTool("write_a", "a", delayMs: 50));
        tools.Register(MakeSlowTool("write_b", "b", delayMs: 50));

        var client = new MultiToolCallingClient(("write_a", null), ("write_b", null));
        var loop = MakeLoop(client, tools);

        // Track concurrent callback invocations to prove seriality.
        // If prompts overlap, the in-flight count will exceed 1.
        var inFlight = 0;
        var maxConcurrent = 0;
        var promptOrder = new List<string>();
        var grantCallbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (req, _) =>
            {
                var current = Interlocked.Increment(ref inFlight);
                // Record the peak — if > 1, prompts ran concurrently.
                int observed;
                do { observed = maxConcurrent; }
                while (current > observed && Interlocked.CompareExchange(ref maxConcurrent, current, observed) != observed);

                lock (promptOrder) { promptOrder.Add(req.RequestingExtension); }

                Interlocked.Decrement(ref inFlight);
                return ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Once));
            }
        };

        var events = await CollectEventsAsync(loop.RunTurnAsync([], grantCallbacks));

        // Both tools should have been prompted.
        Assert.Equal(2, promptOrder.Count);
        // Prompts must have been serial — never more than 1 in flight at a time.
        Assert.Equal(1, maxConcurrent);

        // Both tools should have executed.
        var completed = events.OfType<ToolCallCompleted>().ToList();
        Assert.Equal(2, completed.Count);
        Assert.All(completed, c => Assert.False(c.IsError));
    }

    // -------------------------------------------------------------------------
    // Every ToolCallStarted has a matching ToolCallCompleted
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_AllStartedEventsHaveMatchingCompleted()
    {
        var tools = new ToolRegistry();
        tools.Register(MakeTool("tool_a", "a"), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));
        tools.Register(MakeTool("tool_b", "b"));

        var client = new MultiToolCallingClient(
            ("tool_a", InWorkspaceArgs()),
            ("tool_b", null));
        var loop = MakeLoop(client, tools);

        var denyCallbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(false, null))
        };

        var events = await CollectEventsAsync(loop.RunTurnAsync([], denyCallbacks));

        var startedIds = events.OfType<ToolCallStarted>().Select(s => s.CallId).ToHashSet();
        var completedIds = events.OfType<ToolCallCompleted>().Select(c => c.CallId).ToHashSet();

        // Every started tool must have a completed event.
        Assert.True(startedIds.SetEquals(completedIds),
            $"Mismatched: started={string.Join(",", startedIds)} completed={string.Join(",", completedIds)}");
    }

    // -------------------------------------------------------------------------
    // Unknown tool name produces an error result
    // -------------------------------------------------------------------------

    [Fact]
    public async Task InvokeAllAsync_UnknownToolName_ReturnsError()
    {
        var tools = new ToolRegistry();
        // Register one real tool but call two — the second is not registered.
        tools.Register(MakeTool("real_tool", "ok"), OperationType.Read,
            targetExtractor: TargetExtractors.FromKey("file_path"));

        var client = new MultiToolCallingClient(
            ("real_tool", InWorkspaceArgs()),
            ("hallucinated_tool", null));
        var loop = MakeLoop(client, tools);

        // Provide a granting callback so the unknown tool passes the permission
        // gate and reaches ExecuteToolAsync, where the "Unknown tool" error is produced.
        // Without a callback, the unknown tool would be auto-denied at the permission
        // layer (default Write operation + no callback = denied) and we'd never exercise
        // the handler-lookup path.
        var grantCallbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Once))
        };

        var events = await CollectEventsAsync(loop.RunTurnAsync([], grantCallbacks));

        var completed = events.OfType<ToolCallCompleted>().ToList();
        Assert.Equal(2, completed.Count);

        var realCompleted = completed.Single(c => c.ToolName == "real_tool");
        Assert.Equal("ok", realCompleted.Result);
        Assert.False(realCompleted.IsError);

        // The unknown tool should produce an error result, not crash.
        var unknownCompleted = completed.Single(c => c.ToolName == "hallucinated_tool");
        Assert.StartsWith("Unknown tool:", unknownCompleted.Result);
        Assert.True(unknownCompleted.IsError);
    }

    // -------------------------------------------------------------------------
    // Helpers
    // -------------------------------------------------------------------------

    private static AIFunction MakeTool(string name, string returnValue) =>
        AIFunctionFactory.Create(() => returnValue, name, $"Test tool {name}");

    private static AIFunction MakeSlowTool(string name, string returnValue, int delayMs) =>
        AIFunctionFactory.Create(async () =>
        {
            await Task.Delay(delayMs);
            return returnValue;
        }, name, $"Test tool {name}");

    /// <summary>
    /// Creates a tool that records its start and end timestamps in the provided
    /// dictionaries, enabling structural proof of concurrent execution without
    /// relying on wall-clock thresholds.
    /// </summary>
    private static AIFunction MakeTimedTool(
        string name, string returnValue, int delayMs,
        ConcurrentDictionary<string, long> startTimes,
        ConcurrentDictionary<string, long> endTimes) =>
        AIFunctionFactory.Create(async () =>
        {
            startTimes[name] = Environment.TickCount64;
            await Task.Delay(delayMs);
            endTimes[name] = Environment.TickCount64;
            return returnValue;
        }, name, $"Test tool {name}");

    private static async Task<List<AgentLoopEvent>> CollectEventsAsync(
        IAsyncEnumerable<AgentLoopEvent> events)
    {
        var collected = new List<AgentLoopEvent>();
        await foreach (var evt in events)
            collected.Add(evt);
        return collected;
    }

    /// <summary>
    /// Fake IChatClient that emits multiple tool calls in a single response, then
    /// returns a text response once all tool results arrive. This triggers the
    /// parallel dispatch path in ToolInvoker.
    /// </summary>
    private sealed class MultiToolCallingClient(
        params (string ToolName, Dictionary<string, object?>? Arguments)[] toolCalls) : IChatClient
    {
        private bool _toolCallsEmitted;

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken ct = default)
        {
            await Task.CompletedTask;

            if (!_toolCallsEmitted)
            {
                _toolCallsEmitted = true;
                // Emit all tool calls in a single update — this is what triggers
                // the parallel dispatch pipeline in InvokeAllAsync.
                var contents = toolCalls.Select((tc, i) =>
                    (AIContent)new FunctionCallContent(
                        $"call-{i}",
                        tc.ToolName,
                        tc.Arguments ?? new Dictionary<string, object?>()))
                    .ToList();

                yield return new ChatResponseUpdate { Contents = contents };
            }
            else
            {
                yield return new ChatResponseUpdate(ChatRole.Assistant, "done");
            }
        }

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken ct = default) =>
            Task.FromResult(new ChatResponse(new ChatMessage(ChatRole.Assistant, "done")));

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }
}
