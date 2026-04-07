using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Permissions;
using Ur.Tools;

namespace Ur.Tests;

/// <summary>
/// Unit tests for <see cref="SubagentRunner"/>, using a fake <see cref="IChatClient"/>
/// that returns controlled responses without a real API key. These tests verify:
///   - Text response is accumulated and returned correctly.
///   - No-text-response fallback string is used when the model emits only tool calls.
///   - The <c>run_subagent</c> tool is excluded from the child registry (no self-recursion).
///   - The task string is prepended as the first user message in the fresh history.
/// </summary>
public sealed class SubagentRunnerTests
{
    // ─── Fake clients ──────────────────────────────────────────────────

    /// <summary>
    /// Returns a single streaming chunk with the given text, then terminates.
    /// Simulates the minimal happy-path sub-agent response: one message, no tools.
    /// </summary>
    private sealed class TextOnlyClient(string response) : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new NotSupportedException("SubagentRunner uses streaming");

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            yield return new ChatResponseUpdate(ChatRole.Assistant, response);
            await Task.CompletedTask;
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }

    /// <summary>
    /// Throws during streaming, causing AgentLoop to produce a fatal Error event.
    /// Used to verify that SubagentRunner relays the error event before re-raising.
    /// </summary>
    private sealed class ThrowingStreamClient : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new NotSupportedException("SubagentRunner uses streaming");

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            await Task.CompletedTask;
            throw new HttpRequestException("simulated API failure");
            // ReSharper disable once HeuristicUnreachableCode
#pragma warning disable CS0162 // Unreachable — yield needed to make this an async iterator
            yield break;
#pragma warning restore CS0162
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }

    /// <summary>
    /// Returns no streaming content, causing AgentLoop to produce an empty assistant
    /// message and terminate (TurnCompleted with zero tool calls means no tool dispatch).
    /// </summary>
    private sealed class EmptyResponseClient : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new NotSupportedException("SubagentRunner uses streaming");

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            // No content updates — the loop will see an empty assistant message
            // with no tool calls and yield TurnCompleted immediately.
            await Task.CompletedTask;
            yield break;
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }

    /// <summary>
    /// Records both the tools list and the messages passed on the first invocation.
    /// Allows tests to assert what input the sub-agent received, not only what it
    /// produced — this verifies the task-as-user-message contract.
    /// </summary>
    private sealed class CapturingClient : IChatClient
    {
        public IList<AITool>? CapturedTools { get; private set; }
        public List<ChatMessage>? CapturedMessages { get; private set; }

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new NotSupportedException("SubagentRunner uses streaming");

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            // Capture on first call. Subsequent calls (after tool dispatch) can be ignored.
            if (CapturedTools is null)
            {
                CapturedTools = options?.Tools;
                CapturedMessages = messages.ToList();
            }

            yield return new ChatResponseUpdate(ChatRole.Assistant, "done");
            await Task.CompletedTask;
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }

    // ─── Workspace helper ──────────────────────────────────────────────

    private static Workspace MakeTempWorkspace()
    {
        var path = Path.Combine(Path.GetTempPath(), "ur-subagent-runner-tests", Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(path);
        return new Workspace(path);
    }

    // ─── Tests ─────────────────────────────────────────────────────────

    [Fact]
    public async Task RunAsync_ReturnsFinalTextFromSubagent()
    {
        var workspace = MakeTempWorkspace();
        var runner = new SubagentRunner(
            new TextOnlyClient("sub-agent answer"),
            new ToolRegistry(),
            workspace,
            callbacks: null,
            systemPrompt: null);

        var result = await runner.RunAsync("do something", CancellationToken.None);

        Assert.Equal("sub-agent answer", result);
    }

    [Fact]
    public async Task RunAsync_WhenSubagentProducesNoText_ReturnsFallbackString()
    {
        // A client that emits no content produces an empty assistant message.
        // SubagentRunner must return a defined fallback rather than an empty string.
        var workspace = MakeTempWorkspace();
        var runner = new SubagentRunner(
            new EmptyResponseClient(),
            new ToolRegistry(),
            workspace,
            callbacks: null,
            systemPrompt: null);

        var result = await runner.RunAsync("do something", CancellationToken.None);

        Assert.False(string.IsNullOrWhiteSpace(result),
            "SubagentRunner must not return empty string when the sub-agent yields no text.");
        Assert.Contains("no text response", result, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task RunAsync_SubagentRegistryOmitsRunSubagentTool()
    {
        // The parent registry has run_subagent registered; the sub-agent must NOT
        // receive it — this is the primary guard against unbounded recursion.
        var workspace = MakeTempWorkspace();
        var parentRegistry = new ToolRegistry();

        // Use a stub AIFunction to register run_subagent in the parent.
        var stubTool = new SubagentTool(new StubConsistentRunner());
        parentRegistry.Register(stubTool, OperationType.Execute);

        var capturingClient = new CapturingClient();
        var runner = new SubagentRunner(
            capturingClient,
            parentRegistry,
            workspace,
            callbacks: null,
            systemPrompt: null);

        await runner.RunAsync("any task", CancellationToken.None);

        // The child registry must not expose run_subagent.
        Assert.NotNull(capturingClient.CapturedTools);
        Assert.DoesNotContain(
            capturingClient.CapturedTools,
            t => t.Name == SubagentTool.ToolName);
    }

    [Fact]
    public async Task RunAsync_TaskIsPrependedAsFirstUserMessage()
    {
        // SubagentRunner must place the task as ChatRole.User in a fresh message
        // history. If the role or the message content were wrong, the LLM would
        // receive incorrect input and the tests that check the output would not catch it.
        var workspace = MakeTempWorkspace();
        var capturingClient = new CapturingClient();
        var runner = new SubagentRunner(
            capturingClient,
            new ToolRegistry(),
            workspace,
            callbacks: null,
            systemPrompt: null);

        await runner.RunAsync("summarize the findings", CancellationToken.None);

        Assert.NotNull(capturingClient.CapturedMessages);
        var firstMessage = Assert.Single(capturingClient.CapturedMessages);
        Assert.Equal(ChatRole.User, firstMessage.Role);
        Assert.Contains("summarize the findings",
            firstMessage.Contents.OfType<TextContent>().FirstOrDefault()?.Text ?? "");
    }

    [Fact]
    public async Task RunAsync_SubagentRegistryIncludesOtherParentTools()
    {
        // Only run_subagent should be stripped — all other parent tools must be inherited.
        var workspace = MakeTempWorkspace();
        var parentRegistry = new ToolRegistry();

        var subagentTool = new SubagentTool(new StubConsistentRunner());
        parentRegistry.Register(subagentTool, OperationType.Execute);

        var bashTool = new BashTool(workspace);
        parentRegistry.Register(bashTool, OperationType.Execute);

        var capturingClient = new CapturingClient();
        var runner = new SubagentRunner(
            capturingClient,
            parentRegistry,
            workspace,
            callbacks: null,
            systemPrompt: null);

        await runner.RunAsync("any task", CancellationToken.None);

        Assert.NotNull(capturingClient.CapturedTools);
        Assert.Contains(capturingClient.CapturedTools, t => t.Name == "bash");
        Assert.DoesNotContain(capturingClient.CapturedTools, t => t.Name == SubagentTool.ToolName);
    }

    // ─── Stub runner (needed to construct SubagentTool in registry tests) ─

    /// <summary>
    /// A placeholder runner only used to satisfy SubagentTool's constructor
    /// when registering it into the parent registry under test.
    /// </summary>
    private sealed class StubConsistentRunner : ISubagentRunner
    {
        public Task<string> RunAsync(string task, CancellationToken ct)
            => Task.FromResult("stub");
    }

    // ─── SubagentEventEmitted relay tests ──────────────────────────────

    [Fact]
    public async Task RunAsync_RelaysEventsToSubagentEventEmittedCallback()
    {
        // SubagentRunner must invoke SubagentEventEmitted for each event the inner
        // loop produces, so the parent UI sees sub-agent activity in real time.
        var workspace = MakeTempWorkspace();
        var relayedEvents = new List<AgentLoopEvent>();

        var callbacks = new TurnCallbacks
        {
            SubagentEventEmitted = evt =>
            {
                relayedEvents.Add(evt);
                return ValueTask.CompletedTask;
            }
        };

        var runner = new SubagentRunner(
            new TextOnlyClient("hello"),
            new ToolRegistry(),
            workspace,
            callbacks: callbacks,
            systemPrompt: null);

        await runner.RunAsync("do something", CancellationToken.None);

        // At minimum a ResponseChunk and a TurnCompleted should have been relayed.
        Assert.NotEmpty(relayedEvents);
        Assert.Contains(relayedEvents, e => e is SubagentEvent { Inner: ResponseChunk });
        Assert.Contains(relayedEvents, e => e is SubagentEvent { Inner: TurnCompleted });
    }

    [Fact]
    public async Task RunAsync_AllRelayedEventsHaveConsistentSubagentId()
    {
        // All events relayed from a single RunAsync call must share the same SubagentId,
        // so the parent UI can group them correctly when multiple sub-agents run.
        var workspace = MakeTempWorkspace();
        var relayedEvents = new List<AgentLoopEvent>();

        var callbacks = new TurnCallbacks
        {
            SubagentEventEmitted = evt =>
            {
                relayedEvents.Add(evt);
                return ValueTask.CompletedTask;
            }
        };

        var runner = new SubagentRunner(
            new TextOnlyClient("response"),
            new ToolRegistry(),
            workspace,
            callbacks: callbacks,
            systemPrompt: null);

        await runner.RunAsync("any task", CancellationToken.None);

        var subagentEvents = relayedEvents.OfType<SubagentEvent>().ToList();
        Assert.NotEmpty(subagentEvents);

        // All events from this run must have the same SubagentId.
        var distinctIds = subagentEvents.Select(e => e.SubagentId).Distinct().ToList();
        Assert.Single(distinctIds);

        // The ID must be non-empty and stable — the exact format is an implementation
        // detail, but it must be a non-blank string that groups events from this run.
        Assert.False(string.IsNullOrWhiteSpace(distinctIds[0]));
    }

    [Fact]
    public async Task RunAsync_FatalErrorEventIsRelayedBeforeExceptionPropagates()
    {
        // The relay must fire BEFORE SubagentRunner's switch re-raises the fatal error.
        // If the relay call and the throw were transposed, the callback would never fire
        // for fatal errors and the parent UI would silently miss the error event.
        var workspace = MakeTempWorkspace();
        var relayedEvents = new List<AgentLoopEvent>();

        var callbacks = new TurnCallbacks
        {
            SubagentEventEmitted = evt =>
            {
                relayedEvents.Add(evt);
                return ValueTask.CompletedTask;
            }
        };

        var runner = new SubagentRunner(
            new ThrowingStreamClient(),
            new ToolRegistry(),
            workspace,
            callbacks: callbacks,
            systemPrompt: null);

        // The runner must throw because the inner loop hit a fatal error.
        await Assert.ThrowsAsync<InvalidOperationException>(
            () => runner.RunAsync("trigger error", CancellationToken.None));

        // Even though an exception was thrown, the fatal Error event must have been relayed.
        Assert.Contains(relayedEvents, e => e is SubagentEvent { Inner: TurnError { IsFatal: true } });
    }
}
