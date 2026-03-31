using System.Runtime.CompilerServices;
using Ur.AgentLoop;
using Ur.Providers;
using Ur.Tui;

namespace Ur.Tui.Tests;

internal sealed class TestChatBackend : IChatBackend
{
    private string? _apiKey;
    private string? _selectedModelId;

    public UrChatReadiness Readiness => new(GetBlockingIssues());

    public IReadOnlyList<ModelInfo> AvailableModels { get; } =
    [
        new("anthropic/claude-sonnet-4-6", "Claude Sonnet 4.6", 200_000, 8_192, 0.000003m, 0.000015m, []),
        new("anthropic/claude-opus-4-6", "Claude Opus 4.6", 200_000, 8_192, 0.000015m, 0.000075m, []),
        new("openai/gpt-4o", "GPT-4o", 128_000, 16_384, 0.0000025m, 0.00001m, []),
        new("openai/gpt-4o-mini", "GPT-4o Mini", 128_000, 16_384, 0.00000015m, 0.0000006m, []),
        new("google/gemini-2.5-pro", "Gemini 2.5 Pro", 1_000_000, 65_536, 0.00000125m, 0.00001m, []),
    ];

    public string? SelectedModelId => _selectedModelId;

    public Task SetApiKeyAsync(string apiKey)
    {
        _apiKey = apiKey;
        return Task.CompletedTask;
    }

    public Task SetSelectedModelAsync(string modelId)
    {
        _selectedModelId = modelId;
        return Task.CompletedTask;
    }

    public IChatSession CreateSession() => new TestChatSession();

    private List<UrChatBlockingIssue> GetBlockingIssues()
    {
        var issues = new List<UrChatBlockingIssue>();

        if (string.IsNullOrWhiteSpace(_apiKey))
            issues.Add(UrChatBlockingIssue.MissingApiKey);

        if (string.IsNullOrWhiteSpace(_selectedModelId))
            issues.Add(UrChatBlockingIssue.MissingModelSelection);

        return issues;
    }
}

internal sealed class TestChatSession : IChatSession
{
    private static readonly string[] Responses =
    [
        "I can help you with that! Let me think about this for a moment.",
        "That's an interesting question. Here's what I know about the topic.",
    ];

    private int _responseIndex;

    public async IAsyncEnumerable<AgentLoopEvent> RunTurnAsync(
        string input,
        [EnumeratorCancellation] CancellationToken ct)
    {
        var response = Responses[_responseIndex % Responses.Length];
        _responseIndex++;

        var words = response.Split(' ');
        foreach (var word in words)
        {
            ct.ThrowIfCancellationRequested();
            yield return new ResponseChunk { Text = word + " " };
            await Task.Delay(10, ct);
        }
    }
}
