using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Configuration.Keyring;

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
        Assert.Contains(UrChatBlockingIssue.MissingApiKey, readiness.BlockingIssues);
        Assert.Contains(UrChatBlockingIssue.MissingModelSelection, readiness.BlockingIssues);
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

        var ex = await Assert.ThrowsAsync<UrChatNotReadyException>(async () =>
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
        Assert.True(reopened!.IsPersisted);
        Assert.Equal(2, reopened.Messages.Count);
        Assert.Equal("test-model", reopened.ActiveModelId);
    }

    private static Task<UrHost> CreateHostAsync(
        TempWorkspace workspace,
        IKeyring? keyring = null,
        Func<string, IChatClient>? chatClientFactory = null) =>
        UrHost.StartAsync(
            workspace.WorkspacePath,
            keyring ?? new TestKeyring(),
            workspace.UserSettingsPath,
            chatClientFactory,
            tools: null);

    private static async Task<List<AgentLoopEvent>> CollectEventsAsync(IAsyncEnumerable<AgentLoopEvent> events)
    {
        var collected = new List<AgentLoopEvent>();
        await foreach (var evt in events)
            collected.Add(evt);

        return collected;
    }

    private sealed class TempWorkspace : IDisposable
    {
        public string WorkspacePath { get; }
        public string UserSettingsPath { get; }

        public TempWorkspace()
        {
            WorkspacePath = Path.Combine(Path.GetTempPath(), "ur-tests", Guid.NewGuid().ToString("N"));
            UserSettingsPath = Path.Combine(Path.GetTempPath(), "ur-tests", Guid.NewGuid().ToString("N"), "settings.json");
            Directory.CreateDirectory(WorkspacePath);
        }

        public void Dispose()
        {
            TryDelete(Path.GetDirectoryName(UserSettingsPath)!);
            TryDelete(WorkspacePath);
        }

        private static void TryDelete(string path)
        {
            if (Directory.Exists(path))
                Directory.Delete(path, recursive: true);
        }
    }

    private sealed class TestKeyring : IKeyring
    {
        private readonly Dictionary<(string Service, string Account), string> _secrets = new();

        public string? GetSecret(string service, string account) =>
            _secrets.TryGetValue((service, account), out var secret) ? secret : null;

        public void SetSecret(string service, string account, string secret) =>
            _secrets[(service, account)] = secret;

        public void DeleteSecret(string service, string account) =>
            _secrets.Remove((service, account));
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
}
