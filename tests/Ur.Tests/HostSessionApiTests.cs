using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Hosting;
using Ur.Permissions;
using Ur.Settings;
using Ur.Tests.TestSupport;
using Ur.Tools;

namespace Ur.Tests;

public class HostSessionApiTests
{
    [Fact]
    public async Task StartAsync_WithoutApiKeyOrModel_ReportsReadinessBlockers()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        var readiness = host.Configuration.Readiness;

        // Without a model selected, only MissingModelSelection is reported — the provider
        // check only runs once we know which provider the user intends to use.
        Assert.False(readiness.CanRunTurns);
        Assert.Contains(ChatBlockingIssue.MissingModelSelection, readiness.BlockingIssues);
        Assert.Empty(host.ListSessions());
    }

    [Fact]
    public async Task Configuration_ModelSelection_WritesUserAndWorkspaceScopes()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        host.Configuration.SetSelectedModel("user-model");
        Assert.Equal("user-model", host.Configuration.SelectedModelId);
        // Settings are now written in nested JSON format: {"ur": {"model": "..."}}
        Assert.Contains("\"model\": \"user-model\"", await File.ReadAllTextAsync(workspace.UserSettingsPath));

        host.Configuration.SetSelectedModel("workspace-model", ConfigurationScope.Workspace);
        Assert.Equal("workspace-model", host.Configuration.SelectedModelId);
        Assert.Contains(
            "\"model\": \"workspace-model\"",
            await File.ReadAllTextAsync(Path.Combine(workspace.WorkspacePath, ".ur", "settings.json")));

        host.Configuration.ClearSelectedModel(ConfigurationScope.Workspace);
        Assert.Equal("user-model", host.Configuration.SelectedModelId);

        host.Configuration.ClearSelectedModel();
        Assert.Null(host.Configuration.SelectedModelId);
    }

    [Fact]
    public async Task RunTurnAsync_WhenNotReady_ThrowsBeforePersisting()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);
        var session = host.CreateSession();

        var ex = await Assert.ThrowsAsync<ChatNotReadyException>(async () =>
        {
            await foreach (var _ in session.RunTurnAsync("hello"))
            {
            }
        });

        Assert.False(ex.Readiness.CanRunTurns);
        Assert.False(session.IsPersisted);
        Assert.Empty(session.Messages);
        Assert.Empty(host.ListSessions());
    }

    [Fact]
    public async Task RunTurnAsync_PersistsFirstMessageAndAssistantReply()
    {
        using var workspace = new TempWorkspace();
        var keyring = new TestKeyring();
        var host = await CreateHostAsync(workspace, keyring, _ => new FakeChatClient("hello from assistant"));

        host.Configuration.SetApiKey("test-key");
        host.Configuration.SetSelectedModel("openrouter/test-model");

        var session = host.CreateSession();
        Assert.False(session.IsPersisted);
        Assert.Empty(host.ListSessions());

        var events = await CollectEventsAsync(session.RunTurnAsync("hello"));

        Assert.True(session.IsPersisted);
        Assert.Equal("openrouter/test-model", session.ActiveModelId);
        Assert.Equal(2, session.Messages.Count);
        Assert.Equal(ChatRole.User, session.Messages[0].Role);
        Assert.Equal(ChatRole.Assistant, session.Messages[1].Role);
        Assert.Collection(
            events,
            evt => Assert.IsType<ResponseChunk>(evt),
            evt => Assert.IsType<TurnCompleted>(evt));

        var listedSession = Assert.Single(host.ListSessions());
        Assert.Equal(session.Id, listedSession.Id);

        var sessionPath = Path.Combine(workspace.WorkspacePath, ".ur", "sessions", $"{session.Id}.jsonl");
        Assert.True(File.Exists(sessionPath));
        Assert.Equal(2, (await File.ReadAllLinesAsync(sessionPath)).Length);

        var reopened = await host.OpenSessionAsync(session.Id);
        Assert.NotNull(reopened);
        Assert.True(reopened.IsPersisted);
        Assert.Equal(2, reopened.Messages.Count);
        Assert.Equal("openrouter/test-model", reopened.ActiveModelId);
    }

    [Fact]
    public async Task ActiveModelId_AfterTurnFallsBackToUpdatedConfigurationSelection()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace, keyring: new TestKeyring(), chatClientFactory: _ => new FakeChatClient("hello"));

        host.Configuration.SetApiKey("test-key");
        host.Configuration.SetSelectedModel("openrouter/test-model");

        var session = host.CreateSession();
        await CollectEventsAsync(session.RunTurnAsync("hello"));

        host.Configuration.SetSelectedModel("ollama/qwen3:4b");

        Assert.Equal("ollama/qwen3:4b", session.ActiveModelId);
    }

    // ── Session metrics ─────────────────────────────────────────────

    [Fact]
    public async Task SessionDispose_WritesMetricsJson_WithCorrectTokenCounts()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace, keyring: new TestKeyring(),
            chatClientFactory: _ => new UsageReportingChatClient("hello", inputTokens: 1500, outputTokens: 200));

        host.Configuration.SetApiKey("test-key");
        host.Configuration.SetSelectedModel("openrouter/test-model");

        var session = host.CreateSession();
        await CollectEventsAsync(session.RunTurnAsync("hello"));

        // Dispose writes the metrics file.
        await session.DisposeAsync();

        var sessionsDir = Path.Combine(workspace.WorkspacePath, ".ur", "sessions");
        var metricsFiles = Directory.GetFiles(sessionsDir, "*.metrics.json");
        var metricsFile = Assert.Single(metricsFiles);

        var json = await File.ReadAllTextAsync(metricsFile);
        Assert.Contains("\"turns\": 1", json);
        Assert.Contains("\"input_tokens\": 1500", json);
        Assert.Contains("\"output_tokens\": 200", json);
        Assert.Contains("\"tool_calls_total\": 0", json);
        Assert.Contains("\"tool_calls_errored\": 0", json);
        Assert.Contains("\"error\": null", json);
    }

    [Fact]
    public async Task TurnCompleted_CarriesBothInputAndOutputTokens()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace, keyring: new TestKeyring(),
            chatClientFactory: _ => new UsageReportingChatClient("hello", inputTokens: 3000, outputTokens: 500));

        host.Configuration.SetApiKey("test-key");
        host.Configuration.SetSelectedModel("openrouter/test-model");

        var session = host.CreateSession();
        var events = await CollectEventsAsync(session.RunTurnAsync("hello"));

        var completed = events.OfType<TurnCompleted>().Single();
        Assert.Equal(3000, completed.InputTokens);
        Assert.Equal(500, completed.OutputTokens);
    }

    // ── Helpers ──────────────────────────────────────────────────────

    private static Task<UrHost> CreateHostAsync(
        TempWorkspace workspace,
        TestKeyring? keyring = null,
        Func<string, IChatClient>? chatClientFactory = null) =>
        TestHostBuilder.CreateHostAsync(workspace, keyring, chatClientFactory);

    private static async Task<List<AgentLoopEvent>> CollectEventsAsync(IAsyncEnumerable<AgentLoopEvent> events)
    {
        var collected = new List<AgentLoopEvent>();
        await foreach (var evt in events)
            collected.Add(evt);

        return collected;
    }

    [Fact]
    public async Task RunTurnAsync_WhenLlmThrows_YieldsErrorEventInsteadOfPropagating()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace, chatClientFactory: _ => new ThrowingChatClient("API error"));

        host.Configuration.SetApiKey("test-key");
        host.Configuration.SetSelectedModel("openrouter/test-model");

        var session = host.CreateSession();
        var events = await CollectEventsAsync(session.RunTurnAsync("hello"));

        var error = Assert.Single(events);
        var errorEvent = Assert.IsType<TurnError>(error);
        Assert.Equal("API error", errorEvent.Message);
        Assert.True(errorEvent.IsFatal);
    }

    [Fact]
    public async Task RunTurnAsync_WhenLlmThrowsAfterPartialOutput_YieldsChunksThenErrorThenStops()
    {
        // Verifies two things: (1) ResponseChunk events emitted before the throw
        // are preserved, and (2) the stream terminates after the Error — no stray
        // events follow it.
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace, chatClientFactory: _ => new PartiallyThrowingChatClient("mid-stream failure"));

        host.Configuration.SetApiKey("test-key");
        host.Configuration.SetSelectedModel("openrouter/test-model");

        var session = host.CreateSession();
        var events = await CollectEventsAsync(session.RunTurnAsync("hello"));

        Assert.Equal(2, events.Count);
        Assert.IsType<ResponseChunk>(events[0]);
        var errorEvent = Assert.IsType<TurnError>(events[1]);
        Assert.Equal("mid-stream failure", errorEvent.Message);
        Assert.True(errorEvent.IsFatal);
    }

    [Fact]
    public async Task RunTurnAsync_WhenCancelled_PropagatesOperationCanceledException()
    {
        // OperationCanceledException must propagate, not be swallowed as an Error event.
        // Cancellation is intentional; callers (TUI Ctrl+C) need to distinguish it from
        // a real failure.
        using var workspace = new TempWorkspace();
        using var cts = new CancellationTokenSource();
        var host = await CreateHostAsync(workspace, chatClientFactory: _ => new CancellingChatClient(cts));

        host.Configuration.SetApiKey("test-key");
        host.Configuration.SetSelectedModel("openrouter/test-model");

        var session = host.CreateSession();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(async () =>
        {
            await foreach (var _ in session.RunTurnAsync("hello", cts.Token))
            {
            }
        });
    }

    [Fact]
    public async Task RunTurnAsync_SubagentEventEmitted_RelaysSubagentEventsToHostCallback()
    {
        // Regression test: BuildWrappedCallbacks() built a new TurnCallbacks with only
        // RequestPermissionAsync, silently dropping SubagentEventEmitted. This meant
        // SubagentRunner never had the relay callback and subagent events never reached
        // the parent UI — tool calls the subagent made were invisible to the host.
        using var workspace = new TempWorkspace();

        // Three sequential responses from the same client instance (parent and subagent share it):
        //   Call 1 — parent first turn: call run_subagent
        //   Call 2 — subagent inner loop: return text (the subagent's "work")
        //   Call 3 — parent second turn (after subagent result): final response
        var client = new SequentialResponseClient(
            [new FunctionCallContent("cid-1", SubagentTool.ToolName, new Dictionary<string, object?> { ["task"] = "do a thing" })],
            [new TextContent("subagent result")],
            [new TextContent("all done")]
        );

        var host = await CreateHostAsync(workspace, chatClientFactory: _ => client);
        host.Configuration.SetApiKey("test-key");
        host.Configuration.SetSelectedModel("openrouter/test-model");

        var relayedEvents = new List<AgentLoopEvent>();
        var callbacks = new TurnCallbacks
        {
            // Record every relayed sub-agent event — this is what the bug suppressed.
            SubagentEventEmitted = evt =>
            {
                relayedEvents.Add(evt);
                return ValueTask.CompletedTask;
            },
            // Auto-approve run_subagent so the tool actually executes.
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Once))
        };

        var session = host.CreateSession(callbacks);
        await CollectEventsAsync(session.RunTurnAsync("please delegate to a subagent"));

        // The subagent's ResponseChunk and TurnCompleted must have been relayed.
        // Before the fix, relayedEvents was always empty.
        Assert.NotEmpty(relayedEvents);
        Assert.Contains(relayedEvents, e => e is SubagentEvent { Inner: ResponseChunk });
        Assert.Contains(relayedEvents, e => e is SubagentEvent { Inner: TurnCompleted });
    }

    /// <summary>
    /// Returns a predetermined sequence of content lists for successive streaming calls.
    /// Both the parent AgentLoop and the subagent's AgentLoop share the same client
    /// instance, so call order reflects: parent call 1, subagent call 1, parent call 2, etc.
    /// </summary>
    private sealed class SequentialResponseClient(params IList<AIContent>[] responseSequence) : IChatClient
    {
        private int _callIndex;

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new NotSupportedException("Uses streaming only");

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            var index = Interlocked.Increment(ref _callIndex) - 1;
            var contents = index < responseSequence.Length
                ? responseSequence[index]
                : [new TextContent("(no more responses)")];

            yield return new ChatResponseUpdate { Role = ChatRole.Assistant, Contents = [..contents] };
            await Task.CompletedTask;
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }

    private sealed class FakeChatClient(string responseText) : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            return Task.FromResult(new ChatResponse(new ChatMessage(ChatRole.Assistant, responseText)));
        }

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            yield return new ChatResponseUpdate(ChatRole.Assistant, responseText);
            await Task.CompletedTask;
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;

        public void Dispose()
        {
        }
    }

    private sealed class ThrowingChatClient(string message) : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException(message);

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
// ReSharper disable HeuristicUnreachableCode — yield break is required to make this an async iterator method
#pragma warning disable CS0162
            await Task.CompletedTask;
            throw new InvalidOperationException(message);
            yield break;
#pragma warning restore CS0162
// ReSharper restore HeuristicUnreachableCode
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;

        public void Dispose()
        {
        }
    }

    /// <summary>
    /// Yields one text chunk, then throws — exercises the partial-output-before-error path.
    /// </summary>
    private sealed class PartiallyThrowingChatClient(string message) : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException(message);

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            yield return new ChatResponseUpdate(ChatRole.Assistant, "partial");
            await Task.CompletedTask;
            throw new InvalidOperationException(message);
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;

        public void Dispose()
        {
        }
    }

    /// <summary>
    /// Cancels the provided token source when iterated, simulating a Ctrl+C mid-stream.
    /// </summary>
    private sealed class CancellingChatClient(CancellationTokenSource cts) : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new NotSupportedException();

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            await Task.CompletedTask;
            await cts.CancelAsync();
            cancellationToken.ThrowIfCancellationRequested();
#pragma warning disable CS0162 // unreachable — required to make this an iterator method
            yield break;
#pragma warning restore CS0162
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;

        public void Dispose()
        {
        }
    }

    /// <summary>
    /// Chat client that reports token usage via UsageContent, allowing tests to verify
    /// that TurnCompleted carries both InputTokens and OutputTokens, and that session
    /// metrics accumulate correctly.
    /// </summary>
    private sealed class UsageReportingChatClient(
        string responseText,
        long inputTokens,
        long outputTokens) : IChatClient
    {
        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            return Task.FromResult(new ChatResponse(new ChatMessage(ChatRole.Assistant, responseText))
            {
                Usage = new UsageDetails { InputTokenCount = inputTokens, OutputTokenCount = outputTokens }
            });
        }

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            // Emit text content and usage in the same update, mirroring how real
            // providers report usage in the final streaming chunk.
            yield return new ChatResponseUpdate
            {
                Role = ChatRole.Assistant,
                Contents =
                [
                    new TextContent(responseText),
                    new UsageContent(new UsageDetails
                    {
                        InputTokenCount = inputTokens,
                        OutputTokenCount = outputTokens,
                    }),
                ],
            };
            await Task.CompletedTask;
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;

        public void Dispose()
        {
        }
    }
}
