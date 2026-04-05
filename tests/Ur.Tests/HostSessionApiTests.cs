using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

public class HostSessionApiTests
{
    [Fact]
    public async Task StartAsync_WithoutApiKeyOrModel_ReportsReadinessBlockers()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        var readiness = host.Configuration.Readiness;

        Assert.False(readiness.CanRunTurns);
        Assert.Contains(ChatBlockingIssue.MissingApiKey, readiness.BlockingIssues);
        Assert.Contains(ChatBlockingIssue.MissingModelSelection, readiness.BlockingIssues);
        Assert.Empty(host.ListSessions());
    }

    [Fact]
    public async Task Configuration_ModelSelection_WritesUserAndWorkspaceScopes()
    {
        using var workspace = new TempWorkspace();
        var host = await CreateHostAsync(workspace);

        await host.Configuration.SetSelectedModelAsync("user-model");
        Assert.Equal("user-model", host.Configuration.SelectedModelId);
        Assert.Contains("\"ur.model\": \"user-model\"", await File.ReadAllTextAsync(workspace.UserSettingsPath));

        await host.Configuration.SetSelectedModelAsync("workspace-model", ConfigurationScope.Workspace);
        Assert.Equal("workspace-model", host.Configuration.SelectedModelId);
        Assert.Contains(
            "\"ur.model\": \"workspace-model\"",
            await File.ReadAllTextAsync(Path.Combine(workspace.WorkspacePath, ".ur", "settings.json")));

        await host.Configuration.ClearSelectedModelAsync(ConfigurationScope.Workspace);
        Assert.Equal("user-model", host.Configuration.SelectedModelId);

        await host.Configuration.ClearSelectedModelAsync();
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

        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("test-model");

        var session = host.CreateSession();
        Assert.False(session.IsPersisted);
        Assert.Empty(host.ListSessions());

        var events = await CollectEventsAsync(session.RunTurnAsync("hello"));

        Assert.True(session.IsPersisted);
        Assert.Equal("test-model", session.ActiveModelId);
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
        Assert.Equal(2, File.ReadAllLines(sessionPath).Length);

        var reopened = await host.OpenSessionAsync(session.Id);
        Assert.NotNull(reopened);
        Assert.True(reopened.IsPersisted);
        Assert.Equal(2, reopened.Messages.Count);
        Assert.Equal("test-model", reopened.ActiveModelId);
    }

    [Fact]
    public async Task Extensions_List_IncludesDisabledAndActiveEntriesInStableOrder()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.SystemExtensionsPath,
            "system-ext",
            extensionName: "sample.system",
            toolName: "sample_system_echo",
            settingKey: "sample.system.mode");
        await env.WriteSampleExtensionAsync(
            env.UserExtensionsPath,
            "user-ext",
            extensionName: "sample.user",
            toolName: "sample_user_echo",
            settingKey: "sample.user.mode");
        await env.WriteSampleExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-ext",
            extensionName: "sample.workspace",
            toolName: "sample_workspace_echo",
            settingKey: "sample.workspace.mode");

        var host = await env.StartHostAsync();

        // ReSharper disable ParameterOnlyUsedForPreconditionCheck.Local — Assert.Collection lambda validates properties
        Assert.Collection(
            host.Extensions.List(),
            extension =>
            {
                Assert.Equal("system:sample.system", extension.Id);
                Assert.True(extension.DefaultEnabled);
                Assert.True(extension.DesiredEnabled);
                Assert.True(extension.IsActive);
            },
            extension =>
            {
                Assert.Equal("user:sample.user", extension.Id);
                Assert.True(extension.DefaultEnabled);
                Assert.True(extension.DesiredEnabled);
                Assert.True(extension.IsActive);
            },
            extension =>
            {
                Assert.Equal("workspace:sample.workspace", extension.Id);
                Assert.False(extension.DefaultEnabled);
                Assert.False(extension.DesiredEnabled);
                Assert.False(extension.IsActive);
            }
            );
        // ReSharper restore ParameterOnlyUsedForPreconditionCheck.Local
    }

    [Fact]
    public async Task Extensions_SetEnabledAsync_EnablesWorkspaceExtensionAndPersistsWorkspaceOverride()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-ext",
            extensionName: "sample.workspace",
            toolName: "sample_workspace_echo",
            settingKey: "sample.workspace.mode");
        var host = await env.StartHostAsync();

        var updated = await host.Extensions.SetEnabledAsync("workspace:sample.workspace", enabled: true);

        Assert.True(updated.DesiredEnabled);
        Assert.True(updated.IsActive);
        Assert.True(updated.HasOverride);
        Assert.NotNull(host.BuildSessionToolRegistry("test").Get("sample_workspace_echo"));
        Assert.Contains("\"workspace:sample.workspace\": true", await File.ReadAllTextAsync(env.WorkspaceOverridesPath));
    }

    [Fact]
    public async Task Extensions_SetEnabledAsync_DisablesUserExtensionAndPersistsGlobalOverride()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.UserExtensionsPath,
            "user-ext",
            extensionName: "sample.user",
            toolName: "sample_user_echo",
            settingKey: "sample.user.mode");
        var host = await env.StartHostAsync();

        var updated = await host.Extensions.SetEnabledAsync("user:sample.user", enabled: false);

        Assert.False(updated.DesiredEnabled);
        Assert.False(updated.IsActive);
        Assert.True(updated.HasOverride);
        Assert.Null(host.BuildSessionToolRegistry("test").Get("sample_user_echo"));
        Assert.Contains("\"user:sample.user\": false", await File.ReadAllTextAsync(env.GlobalOverridesPath));
    }

    [Fact]
    public async Task Extensions_ResetAsync_ClearsOverrideAndRestoresTierDefaultBehavior()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-ext",
            extensionName: "sample.workspace",
            toolName: "sample_workspace_echo",
            settingKey: "sample.workspace.mode");
        var host = await env.StartHostAsync();
        await host.Extensions.SetEnabledAsync("workspace:sample.workspace", enabled: true);

        var updated = await host.Extensions.ResetAsync("workspace:sample.workspace");

        Assert.False(updated.DesiredEnabled);
        Assert.False(updated.IsActive);
        Assert.False(updated.HasOverride);
        Assert.Null(host.BuildSessionToolRegistry("test").Get("sample_workspace_echo"));
        Assert.False(File.Exists(env.WorkspaceOverridesPath));
    }

    [Fact]
    public async Task Extensions_SetEnabledAsync_ActivationFailureSurfacesLoadErrorWithoutCrashingHost()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteExtensionAsync(
            env.WorkspaceExtensionsPath,
            "broken-workspace",
            """
            return {
              name = "sample.workspace",
              version = "1.0.0"
            }
            """,
            """
            this is not valid lua
            """);
        var host = await env.StartHostAsync();

        var updated = await host.Extensions.SetEnabledAsync("workspace:sample.workspace", enabled: true);

        Assert.True(updated.DesiredEnabled);
        Assert.False(updated.IsActive);
        Assert.True(updated.HasOverride);
        Assert.NotNull(updated.LoadError);
    }

    [Fact]
    public async Task Extensions_SetEnabledAsync_UnknownExtensionIdFailsCleanly()
    {
        using var env = new TempExtensionEnvironment();
        var host = await env.StartHostAsync();

        var ex = await Assert.ThrowsAsync<ArgumentException>(() =>
            host.Extensions.SetEnabledAsync("workspace:missing", enabled: true));

        Assert.Equal("extensionId", ex.ParamName);
    }

    [Fact]
    public async Task StartAsync_GlobalOverrideFileDisablesUserExtensionOnStartup()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.UserExtensionsPath,
            "user-ext",
            extensionName: "sample.user",
            toolName: "sample_user_echo",
            settingKey: "sample.user.mode");
        await env.WriteGlobalOverridesAsync(
            """
            {
              "version": 1,
              "extensions": {
                "user:sample.user": false
              }
            }
            """);

        var host = await env.StartHostAsync();
        var extension = Assert.Single(host.Extensions.List());

        Assert.False(extension.DesiredEnabled);
        Assert.False(extension.IsActive);
        Assert.True(extension.HasOverride);
        Assert.Null(host.BuildSessionToolRegistry("test").Get("sample_user_echo"));
    }

    [Fact]
    public async Task StartAsync_WorkspaceOverrideFileEnablesWorkspaceExtensionOnStartup()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-ext",
            extensionName: "sample.workspace",
            toolName: "sample_workspace_echo",
            settingKey: "sample.workspace.mode");
        await env.WriteWorkspaceOverridesAsync(
            $$"""
            {
              "version": 1,
              "workspacePath": "{{env.WorkspacePath}}",
              "extensions": {
                "workspace:sample.workspace": true
              }
            }
            """);

        var host = await env.StartHostAsync();
        var extension = Assert.Single(host.Extensions.List());

        Assert.True(extension.DesiredEnabled);
        Assert.True(extension.IsActive);
        Assert.True(extension.HasOverride);
        Assert.NotNull(host.BuildSessionToolRegistry("test").Get("sample_workspace_echo"));
    }

    [Fact]
    public async Task StartAsync_MalformedOverrideFileFallsBackToDefaultsWithoutCrashing()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.WorkspaceExtensionsPath,
            "workspace-ext",
            extensionName: "sample.workspace",
            toolName: "sample_workspace_echo",
            settingKey: "sample.workspace.mode");
        await env.WriteWorkspaceOverridesAsync("{ not json");

        var host = await env.StartHostAsync();
        var extension = Assert.Single(host.Extensions.List());

        Assert.False(extension.DesiredEnabled);
        Assert.False(extension.IsActive);
        Assert.False(extension.HasOverride);
        Assert.Null(extension.LoadError);
    }

    [Fact]
    public async Task Extensions_PersistOnlyDeltasNotRedundantDefaults()
    {
        using var env = new TempExtensionEnvironment();
        await env.WriteSampleExtensionAsync(
            env.UserExtensionsPath,
            "user-ext",
            extensionName: "sample.user",
            toolName: "sample_user_echo",
            settingKey: "sample.user.mode");
        var host = await env.StartHostAsync();

        await host.Extensions.SetEnabledAsync("user:sample.user", enabled: false);
        await host.Extensions.ResetAsync("user:sample.user");

        Assert.False(File.Exists(env.GlobalOverridesPath));
    }

    private static Task<UrHost> CreateHostAsync(
        TempWorkspace workspace,
        TestKeyring? keyring = null,
        Func<string, IChatClient>? chatClientFactory = null) =>
        UrHost.StartAsync(
            workspace.WorkspacePath,
            keyring ?? new TestKeyring(),
            workspace.UserSettingsPath,
            chatClientFactory,
            userDataDirectory: workspace.UserDataDirectory);

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

        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("test-model");

        var session = host.CreateSession();
        var events = await CollectEventsAsync(session.RunTurnAsync("hello"));

        var error = Assert.Single(events);
        var errorEvent = Assert.IsType<Error>(error);
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

        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("test-model");

        var session = host.CreateSession();
        var events = await CollectEventsAsync(session.RunTurnAsync("hello"));

        Assert.Equal(2, events.Count);
        Assert.IsType<ResponseChunk>(events[0]);
        var errorEvent = Assert.IsType<Error>(events[1]);
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

        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("test-model");

        var session = host.CreateSession();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(async () =>
        {
            await foreach (var _ in session.RunTurnAsync("hello", cts.Token))
            {
            }
        });
    }

    private sealed class FakeChatClient : IChatClient
    {
        private readonly string _responseText;

        public FakeChatClient(string responseText)
        {
            _responseText = responseText;
        }

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            return Task.FromResult(new ChatResponse(new ChatMessage(ChatRole.Assistant, _responseText)));
        }

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            yield return new ChatResponseUpdate(ChatRole.Assistant, _responseText);
            await Task.CompletedTask;
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;

        public void Dispose()
        {
        }
    }

    private sealed class ThrowingChatClient : IChatClient
    {
        private readonly string _message;

        public ThrowingChatClient(string message)
        {
            _message = message;
        }

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException(_message);

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
// ReSharper disable HeuristicUnreachableCode — yield break is required to make this an async iterator method
#pragma warning disable CS0162
            await Task.CompletedTask;
            throw new InvalidOperationException(_message);
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
    private sealed class PartiallyThrowingChatClient : IChatClient
    {
        private readonly string _message;

        public PartiallyThrowingChatClient(string message)
        {
            _message = message;
        }

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            throw new InvalidOperationException(_message);

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            yield return new ChatResponseUpdate(ChatRole.Assistant, "partial");
            await Task.CompletedTask;
            throw new InvalidOperationException(_message);
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;

        public void Dispose()
        {
        }
    }

    /// <summary>
    /// Cancels the provided token source when iterated, simulating a Ctrl+C mid-stream.
    /// </summary>
    private sealed class CancellingChatClient : IChatClient
    {
        private readonly CancellationTokenSource _cts;

        public CancellingChatClient(CancellationTokenSource cts)
        {
            _cts = cts;
        }

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
            _cts.Cancel();
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
}
