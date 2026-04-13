using Microsoft.Extensions.AI;
using Ox.Agent.AgentLoop;
using Ox.Agent.Providers.Fake;
using Ox.Tests.TestSupport;

namespace Ox.Tests.Agent.Providers;

/// <summary>
/// Tests for the fake provider, fake chat client, and scenario infrastructure.
/// Verifies that the fake provider participates in the real provider pipeline
/// and produces deterministic responses.
/// </summary>
public sealed class FakeProviderTests
{
    // ── FakeProvider basics ───────────────────────────────────────────

    [Fact]
    public void FakeProvider_Name_IsFake()
    {
        var provider = new FakeProvider();
        Assert.Equal("fake", provider.Name);
    }

    [Fact]
    public void FakeProvider_RequiresApiKey_IsFalse()
    {
        var provider = new FakeProvider();
        Assert.False(provider.RequiresApiKey);
    }

    [Fact]
    public void FakeProvider_GetBlockingIssue_ReturnsNull()
    {
        var provider = new FakeProvider();
        Assert.Null(provider.GetBlockingIssue());
    }

    [Fact]
    public void FakeProvider_CreateChatClient_WithBuiltInScenario_Succeeds()
    {
        var provider = new FakeProvider();
        var client = provider.CreateChatClient("hello");
        Assert.NotNull(client);
    }

    [Fact]
    public void FakeProvider_CreateChatClient_WithUnknownScenario_Throws()
    {
        var provider = new FakeProvider();
        Assert.Throws<ArgumentException>(() => provider.CreateChatClient("nonexistent-scenario"));
    }

    // ── FakeChatClient text streaming ────────────────────────────────

    [Fact]
    public async Task FakeChatClient_StreamsTextChunks()
    {
        var scenario = new FakeScenario
        {
            Name = "test",
            Turns =
            [
                new FakeScenarioTurn
                {
                    TextChunks = ["Hello, ", "world!"],
                    InputTokens = 5,
                    OutputTokens = 10,
                }
            ]
        };

        var client = new FakeChatClient(scenario);
        var updates = new List<ChatResponseUpdate>();

        await foreach (var update in client.GetStreamingResponseAsync([]))
            updates.Add(update);

        // Should get 2 text chunks + 1 usage update.
        Assert.Equal(3, updates.Count);

        // Verify text content.
        Assert.Equal("Hello, ", updates[0].Text);
        Assert.Equal("world!", updates[1].Text);

        // Verify usage data.
        var usage = updates[2].Contents.OfType<UsageContent>().Single();
        Assert.Equal(5, usage.Details.InputTokenCount);
        Assert.Equal(10, usage.Details.OutputTokenCount);
    }

    [Fact]
    public async Task FakeChatClient_StreamsToolCall()
    {
        var scenario = new FakeScenario
        {
            Name = "test",
            Turns =
            [
                new FakeScenarioTurn
                {
                    ToolCall = new FakeToolCall
                    {
                        Name = "read_file",
                        ArgumentsJson = """{"path": "test.txt"}""",
                    }
                }
            ]
        };

        var client = new FakeChatClient(scenario);
        var updates = new List<ChatResponseUpdate>();

        await foreach (var update in client.GetStreamingResponseAsync([]))
            updates.Add(update);

        var fcc = updates
            .SelectMany(u => u.Contents)
            .OfType<FunctionCallContent>()
            .Single();

        Assert.Equal("read_file", fcc.Name);
        Assert.Equal("test.txt", fcc.Arguments?["path"]?.ToString());
    }

    [Fact]
    public async Task FakeChatClient_SimulatesError()
    {
        var scenario = new FakeScenario
        {
            Name = "test",
            Turns =
            [
                new FakeScenarioTurn
                {
                    SimulateError = true,
                    ErrorMessage = "Boom!",
                }
            ]
        };

        var client = new FakeChatClient(scenario);

        var ex = await Assert.ThrowsAsync<InvalidOperationException>(async () =>
        {
            await foreach (var _ in client.GetStreamingResponseAsync([]))
            {
            }
        });

        Assert.Equal("Boom!", ex.Message);
    }

    [Fact]
    public async Task FakeChatClient_MultiTurn_PopsInOrder()
    {
        var scenario = new FakeScenario
        {
            Name = "test",
            Turns =
            [
                new FakeScenarioTurn { TextChunks = ["first"] },
                new FakeScenarioTurn { TextChunks = ["second"] },
            ]
        };

        var client = new FakeChatClient(scenario);

        // First call yields "first".
        var first = new List<ChatResponseUpdate>();
        await foreach (var u in client.GetStreamingResponseAsync([]))
            first.Add(u);
        Assert.Equal("first", first[0].Text);

        // Second call yields "second".
        var second = new List<ChatResponseUpdate>();
        await foreach (var u in client.GetStreamingResponseAsync([]))
            second.Add(u);
        Assert.Equal("second", second[0].Text);
    }

    [Fact]
    public async Task FakeChatClient_ExceedsTurns_ThrowsWithClearError()
    {
        var scenario = new FakeScenario
        {
            Name = "test-single",
            Turns =
            [
                new FakeScenarioTurn { TextChunks = ["only turn"] },
            ]
        };

        var client = new FakeChatClient(scenario);

        // Consume the only turn.
        await foreach (var _ in client.GetStreamingResponseAsync([]))
        {
        }

        // Second call should throw.
        var ex = await Assert.ThrowsAsync<InvalidOperationException>(async () =>
        {
            await foreach (var _ in client.GetStreamingResponseAsync([]))
            {
            }
        });

        Assert.Contains("test-single", ex.Message);
        Assert.Contains("1 turn(s)", ex.Message);
    }

    // ── Built-in scenarios ───────────────────────────────────────────

    [Theory]
    [InlineData("hello")]
    [InlineData("long-response")]
    [InlineData("tool-call")]
    [InlineData("permission-tool-call")]
    [InlineData("error")]
    [InlineData("multi-turn")]
    public void BuiltInScenarios_AllLoadSuccessfully(string name)
    {
        var scenario = BuiltInScenarios.Get(name);
        Assert.NotNull(scenario);
        Assert.Equal(name, scenario.Name);
        Assert.NotEmpty(scenario.Turns);
    }

    [Fact]
    public void BuiltInScenarios_UnknownName_ReturnsNull()
    {
        Assert.Null(BuiltInScenarios.Get("nonexistent"));
    }

    // ── End-to-end through the real OxHost pipeline ──────────────────

    [Fact]
    public async Task FakeProvider_ThroughOxHost_StreamsDeterministicResponse()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(
            workspace,
            keyring: new TestKeyring(),
            fakeProvider: new FakeProvider(),
            selectedModelOverride: "fake/hello");

        // Readiness should pass — the fake provider needs no API key.
        Assert.True(host.Configuration.Readiness.CanRunTurns);
        Assert.Equal("fake/hello", host.Configuration.SelectedModelId);

        var session = host.CreateSession();
        var events = new List<AgentLoopEvent>();
        await foreach (var evt in session.RunTurnAsync("hi"))
            events.Add(evt);

        // Should get ResponseChunk events for the text + a TurnCompleted.
        Assert.Contains(events, e => e is ResponseChunk);
        Assert.Contains(events, e => e is TurnCompleted);
    }

    [Fact]
    public async Task FakeProvider_SelectedModelOverride_DoesNotPersistToSettings()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(
            workspace,
            keyring: new TestKeyring(),
            fakeProvider: new FakeProvider(),
            selectedModelOverride: "fake/hello");

        // The override should be active.
        Assert.Equal("fake/hello", host.Configuration.SelectedModelId);

        // But persisted settings should not contain the fake model.
        var settingsPath = workspace.UserSettingsPath;
        if (File.Exists(settingsPath))
        {
            var content = await File.ReadAllTextAsync(settingsPath);
            Assert.DoesNotContain("fake", content);
        }
    }
}
