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
}
