using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Tests.TestSupport;

namespace Ur.Tests.Skills;

/// <summary>
/// Integration tests for slash command handling in UrSession. These verify
/// the full path: user types "/skill-name args" → UrSession expands the skill
/// → the expanded content is sent to the LLM.
/// </summary>
public sealed class SkillSessionTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "ur-skill-session-tests",
        Guid.NewGuid().ToString("N"));

    private string WorkspacePath => Path.Combine(_root, "workspace");
    private string UserDataDirectory => Path.Combine(_root, "user-data");
    private string UserSettingsPath => Path.Combine(UserDataDirectory, "settings.json");
    private string WorkspaceSkillsDir => Path.Combine(WorkspacePath, ".ur", "skills");

    // Holds the DI host so it's disposed when the test class is disposed.
    private TempWorkspace? _hostWorkspace;

    public SkillSessionTests()
    {
        Directory.CreateDirectory(WorkspacePath);
        Directory.CreateDirectory(UserDataDirectory);
    }

    public void Dispose()
    {
        _hostWorkspace?.Dispose();

        if (Directory.Exists(_root))
            Directory.Delete(_root, recursive: true);
    }

    private void WriteWorkspaceSkill(string name, string content)
    {
        var dir = Path.Combine(WorkspaceSkillsDir, name);
        Directory.CreateDirectory(dir);
        File.WriteAllText(Path.Combine(dir, "SKILL.md"), content);
    }

    private async Task<UrHost> StartHostAsync(IChatClient? client = null)
    {
        // Non-owning workspace wrapping the local paths. The host is attached to
        // it and disposed via _hostWorkspace in the test class Dispose.
        _hostWorkspace = new TempWorkspace(WorkspacePath, UserDataDirectory, UserSettingsPath);
        return await TestHostBuilder.CreateHostAsync(
                _hostWorkspace,
                chatClientFactory: _ => client ?? new FakeChatClient("OK"))
            .ConfigureAwait(false);
    }

    private static async Task<List<AgentLoopEvent>> CollectAsync(IAsyncEnumerable<AgentLoopEvent> events)
    {
        var collected = new List<AgentLoopEvent>();
        await foreach (var evt in events)
            collected.Add(evt);
        return collected;
    }

    // ─── Slash command invocation ─────────────────────────────────────

    [Fact]
    public async Task SlashCommand_ValidSkill_ExpandsAndSendsToModel()
    {
        WriteWorkspaceSkill("hello", "---\nname: hello\n---\nHello, $ARGUMENTS!");

        // Use a chat client that captures the user message it receives.
        var capturingClient = new CapturingChatClient();
        var host = await StartHostAsync(capturingClient);
        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("openrouter/test-model");

        var session = host.CreateSession();
        var events = await CollectAsync(session.RunTurnAsync("/hello world"));

        // Should NOT yield an error — the skill expanded successfully.
        Assert.DoesNotContain(events, e => e is TurnError);

        // The user message sent to the LLM should contain the expanded content.
        Assert.NotNull(capturingClient.LastMessages);
        var userMsg = capturingClient.LastMessages
            .LastOrDefault(m => m.Role == ChatRole.User);
        Assert.NotNull(userMsg);
        var text = userMsg.Text;
        Assert.Contains("<command-name>/hello</command-name>", text);
        Assert.Contains("Hello, world!", text);
    }

    [Fact]
    public async Task SlashCommand_UnknownSkill_YieldsErrorEvent()
    {
        // No skills loaded — any /command should fail.
        var host = await StartHostAsync();
        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("openrouter/test-model");

        var session = host.CreateSession();
        var events = await CollectAsync(session.RunTurnAsync("/nonexistent"));

        var error = Assert.Single(events.OfType<TurnError>());
        Assert.Contains("Unknown command", error.Message);
        Assert.False(error.IsFatal);

        // No messages should have been persisted — the turn aborted early.
        Assert.Empty(session.Messages);
    }

    [Fact]
    public async Task SlashCommand_UserInvocableFalse_YieldsErrorEvent()
    {
        WriteWorkspaceSkill("hidden", "---\nname: hidden\nuser-invocable: false\n---\nSecret");

        var host = await StartHostAsync();
        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("openrouter/test-model");

        var session = host.CreateSession();
        var events = await CollectAsync(session.RunTurnAsync("/hidden"));

        var error = Assert.Single(events.OfType<TurnError>());
        Assert.Contains("Unknown command", error.Message);
        Assert.Empty(session.Messages);
    }

    [Fact]
    public async Task RegularInput_NoSlash_PassesThroughUnchanged()
    {
        WriteWorkspaceSkill("hello", "---\nname: hello\n---\nExpanded");

        var capturingClient = new CapturingChatClient();
        var host = await StartHostAsync(capturingClient);
        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("openrouter/test-model");

        var session = host.CreateSession();
        await CollectAsync(session.RunTurnAsync("just a normal message"));

        // The user message should be the raw input, not expanded.
        Assert.NotNull(capturingClient.LastMessages);
        var userMsg = capturingClient.LastMessages
            .LastOrDefault(m => m.Role == ChatRole.User);
        Assert.NotNull(userMsg);
        Assert.Equal("just a normal message", userMsg.Text);
    }

    // ─── Test doubles ─────────────────────────────────────────────────

    /// <summary>
    /// A chat client that echoes "OK" and captures the messages it received,
    /// so tests can inspect what the agent loop actually sent to the LLM.
    /// </summary>
    private sealed class CapturingChatClient : IChatClient
    {
        public List<ChatMessage>? LastMessages { get; private set; }

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default)
        {
            LastMessages = messages.ToList();
            return Task.FromResult(new ChatResponse(new ChatMessage(ChatRole.Assistant, "OK")));
        }

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken cancellationToken = default)
        {
            LastMessages = messages.ToList();
            yield return new ChatResponseUpdate(ChatRole.Assistant, "OK");
            await Task.CompletedTask;
        }

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }

    /// <summary>
    /// Simple chat client that returns a fixed response. Same as the one in
    /// HostSessionApiTests but duplicated here for test isolation.
    /// </summary>
    private sealed class FakeChatClient(string response) : IChatClient
    {

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken cancellationToken = default) =>
            Task.FromResult(new ChatResponse(new ChatMessage(ChatRole.Assistant, response)));

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
}
