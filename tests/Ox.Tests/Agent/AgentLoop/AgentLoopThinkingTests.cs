using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging.Abstractions;
using Ox.Agent;
using Ox.Agent.AgentLoop;
using Ox.Agent.Permissions;
using Ox.Agent.Providers.Fake;
using Ox.Agent.Tools;
using Ox.Tests.TestSupport;

// The class Ox.Agent.AgentLoop.AgentLoop lives inside the Ox.Agent.AgentLoop namespace,
// causing a name clash when `using Ox.Agent.AgentLoop;` is in scope. Alias it so
// the test body can say `Loop` without ambiguity.
using Loop = Ox.Agent.AgentLoop.AgentLoop;

// Suppress the "no parameters" warning for EnumeratorCancellation on zero-arg async iterators
// used in SpyChatClient.
#pragma warning disable CS8424

namespace Ox.Tests.Agent.AgentLoop;

/// <summary>
/// Unit tests for the AgentLoop's thinking (TextReasoningContent) handling.
///
/// Two behaviours are critical:
///   1. When a provider emits <see cref="TextReasoningContent"/> items,
///      AgentLoop should yield <see cref="ThinkingChunk"/> events.
///   2. <c>BuildLlmMessages</c> must strip <see cref="TextReasoningContent"/>
///      from the conversation history before sending it back to the LLM, so
///      prior reasoning traces are never fed back as input.
///
/// Tests use a lightweight <see cref="SpyChatClient"/> that records the messages
/// each call receives, enabling assertions about what AgentLoop feeds into the
/// LLM without needing a real provider.
/// </summary>
public sealed class AgentLoopThinkingTests
{
    // ─── ThinkingChunk event emission ────────────────────────────────────────

    [Fact]
    public async Task RunTurnAsync_TextReasoningContent_EmitsThinkingChunks()
    {
        // A fake that emits two reasoning chunks followed by a text response.
        var scenario = new FakeScenario
        {
            Name = "thinking-test",
            Turns =
            [
                new FakeScenarioTurn
                {
                    ReasoningChunks = ["First thought. ", "Second thought."],
                    TextChunks = ["The answer is 59."],
                }
            ]
        };

        var events = await RunOneTurnAsync(scenario);

        // Both reasoning chunks must appear as ThinkingChunk events.
        var thinkingChunks = events.OfType<ThinkingChunk>().ToList();
        Assert.Equal(2, thinkingChunks.Count);
        Assert.Equal("First thought. ", thinkingChunks[0].Text);
        Assert.Equal("Second thought.", thinkingChunks[1].Text);

        // The response text must also appear.
        var responseChunks = events.OfType<ResponseChunk>().ToList();
        Assert.Single(responseChunks);
        Assert.Equal("The answer is 59.", responseChunks[0].Text);
    }

    [Fact]
    public async Task RunTurnAsync_ReasoningBeforeText_OrderPreserved()
    {
        // Reasoning events must arrive before response events — they represent
        // model thinking that precedes the actual answer.
        var scenario = new FakeScenario
        {
            Name = "order-test",
            Turns =
            [
                new FakeScenarioTurn
                {
                    ReasoningChunks = ["Reasoning."],
                    TextChunks = ["Response."],
                }
            ]
        };

        var events = await RunOneTurnAsync(scenario);

        var ordered = events.Where(e => e is ThinkingChunk or ResponseChunk).ToList();
        Assert.IsType<ThinkingChunk>(ordered[0]);
        Assert.IsType<ResponseChunk>(ordered[1]);
    }

    [Fact]
    public async Task RunTurnAsync_NoReasoning_NoThinkingChunks()
    {
        // Normal scenario with no reasoning: no ThinkingChunk should appear.
        var scenario = new FakeScenario
        {
            Name = "no-thinking",
            Turns = [new FakeScenarioTurn { TextChunks = ["Hello."] }]
        };

        var events = await RunOneTurnAsync(scenario);

        Assert.Empty(events.OfType<ThinkingChunk>());
        Assert.Single(events.OfType<ResponseChunk>());
    }

    // ─── BuildLlmMessages: TextReasoningContent filtering ────────────────────

    [Fact]
    public async Task RunTurnAsync_SecondTurn_DoesNotReceiveReasoningFromFirstTurn()
    {
        // After a turn that produced reasoning content, the next LLM call must
        // not see that reasoning in the message history — providers do not want
        // prior reasoning traces fed back as input.
        //
        // Test design: two-call scenario. Call 1 emits reasoning + a tool call
        // (forcing a second LLM call). Call 2 is the final text response. A spy
        // client records what messages each call received. The registered fake tool
        // always succeeds so the loop completes normally.
        using var workspace = new TempWorkspace();

        // Register a trivial fake tool so the loop can execute the tool call.
        var registry = new ToolRegistry();
        var fakeTool = AIFunctionFactory.Create(
            () => "result",
            "fake_tool",
            "A test tool that always returns 'result'.");
        registry.Register(fakeTool);

        var spy = new SpyChatClient(
        [
            // Call 1: reasoning chunk + a call to fake_tool.
            new SpyChatTurn(
                new TextReasoningContent("My thinking"),
                new FunctionCallContent("call-1", "fake_tool",
                    new Dictionary<string, object?>())),
            // Call 2: final response after tool execution.
            new SpyChatTurn(new TextContent("Done.")),
        ]);

        var loop = new Loop(
            spy,
            registry,
            new Workspace(workspace.WorkspacePath),
            NullLogger<Loop>.Instance,
            NullLoggerFactory.Instance);

        var messages = new List<ChatMessage>
        {
            new(ChatRole.User, "Use fake_tool and report back.")
        };

        var callbacks = new TurnCallbacks
        {
            // Auto-approve so the tool call executes without blocking.
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(Granted: true, Scope: PermissionScope.Once))
        };

        await foreach (var _ in loop.RunTurnAsync(messages, callbacks))
        {
            // Drain to completion.
        }

        // Verify: the second LLM call must not contain TextReasoningContent.
        Assert.True(spy.CallCount >= 2, "Expected at least 2 LLM calls (tool call + final response).");

        var secondCallMessages = spy.ReceivedMessages[1];
        var hasReasoning = secondCallMessages
            .SelectMany(m => m.Contents)
            .OfType<TextReasoningContent>()
            .Any();
        Assert.False(hasReasoning,
            "BuildLlmMessages must strip TextReasoningContent before replaying to the LLM.");
    }

    // ─── Helpers ─────────────────────────────────────────────────────────────

    /// <summary>
    /// Runs a single user turn through an AgentLoop backed by the given scenario
    /// and collects all emitted events.
    /// </summary>
    private static async Task<List<AgentLoopEvent>> RunOneTurnAsync(FakeScenario scenario)
    {
        using var workspace = new TempWorkspace();
        var client = new FakeChatClient(scenario);
        var loop = new Loop(
            client,
            new ToolRegistry(),
            new Workspace(workspace.WorkspacePath),
            NullLogger<Loop>.Instance,
            NullLoggerFactory.Instance);

        var messages = new List<ChatMessage>
        {
            new(ChatRole.User, "Think about this.")
        };

        var events = new List<AgentLoopEvent>();
        await foreach (var evt in loop.RunTurnAsync(messages))
            events.Add(evt);

        return events;
    }

    // ─── SpyChatClient ────────────────────────────────────────────────────────

    /// <summary>
    /// A minimal <see cref="IChatClient"/> that replays pre-built turns and
    /// records the <see cref="ChatMessage"/> list each call received.
    ///
    /// Used to verify that <c>BuildLlmMessages</c> strips <see cref="TextReasoningContent"/>
    /// from the conversation history before handing it to the LLM.
    /// </summary>
    private sealed class SpyChatClient(IReadOnlyList<SpyChatTurn> turns) : IChatClient
    {
        /// <summary>All message lists that each call received, in call order.</summary>
        public List<List<ChatMessage>> ReceivedMessages { get; } = [];
        public int CallCount => ReceivedMessages.Count;

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new NotSupportedException("Only streaming path is used.");

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            var callIndex = ReceivedMessages.Count;
            ReceivedMessages.Add(messages.ToList());

            if (callIndex >= turns.Count)
                throw new InvalidOperationException(
                    $"SpyChatClient: unexpected call #{callIndex + 1}, only {turns.Count} turns configured.");

            foreach (var content in turns[callIndex].Contents)
            {
                cancellationToken.ThrowIfCancellationRequested();
                yield return new ChatResponseUpdate
                {
                    Role = ChatRole.Assistant,
                    Contents = [content]
                };
            }

            await Task.CompletedTask;
        }

        public object? GetService(Type serviceType, object? key = null) => null;
        public void Dispose() { }
    }

    /// <summary>One turn for <see cref="SpyChatClient"/>: a list of content items to emit.</summary>
    private sealed class SpyChatTurn(params AIContent[] contents)
    {
        public IReadOnlyList<AIContent> Contents { get; } = contents;
    }
}
