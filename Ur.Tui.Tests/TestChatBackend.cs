using System.Runtime.CompilerServices;
using Ur.AgentLoop;
using Ur.Extensions;
using Ur.Providers;
using Ur.Tui;

namespace Ur.Tui.Tests;

internal sealed class TestChatBackend : IChatBackend
{
    private string? _apiKey;
    private string? _selectedModelId;
    private readonly List<UrExtensionInfo> _extensions =
    [
        new(
            "system:sample.system",
            "sample.system",
            ExtensionTier.System,
            "System extension",
            "1.0.0",
            defaultEnabled: true,
            desiredEnabled: true,
            isActive: true,
            hasOverride: false,
            loadError: null),
        new(
            "user:sample.user",
            "sample.user",
            ExtensionTier.User,
            "User extension",
            "1.0.0",
            defaultEnabled: true,
            desiredEnabled: true,
            isActive: true,
            hasOverride: false,
            loadError: null),
        new(
            "workspace:sample.workspace",
            "sample.workspace",
            ExtensionTier.Workspace,
            "Workspace extension",
            "1.0.0",
            defaultEnabled: false,
            desiredEnabled: false,
            isActive: false,
            hasOverride: false,
            loadError: null),
    ];

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
    public int SetExtensionEnabledCallCount { get; private set; }
    public int ResetExtensionCallCount { get; private set; }
    public string? FailNextMutationMessage { get; set; }
    public string? ActivationFailureMessage { get; set; }

    public IReadOnlyList<UrExtensionInfo> ListExtensions() =>
        _extensions.ToList();

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

    public Task<UrExtensionInfo> SetExtensionEnabledAsync(
        string extensionId,
        bool enabled,
        CancellationToken ct = default)
    {
        ct.ThrowIfCancellationRequested();
        SetExtensionEnabledCallCount++;
        ThrowIfMutationFails();

        var index = FindExtensionIndex(extensionId);
        var existing = _extensions[index];
        var hasOverride = enabled != existing.DefaultEnabled;
        var updated = new UrExtensionInfo(
            existing.Id,
            existing.Name,
            existing.Tier,
            existing.Description,
            existing.Version,
            existing.DefaultEnabled,
            enabled,
            isActive: enabled,
            hasOverride,
            loadError: null);

        if (enabled && ActivationFailureMessage is not null)
        {
            updated = new UrExtensionInfo(
                existing.Id,
                existing.Name,
                existing.Tier,
                existing.Description,
                existing.Version,
                existing.DefaultEnabled,
                desiredEnabled: true,
                isActive: false,
                hasOverride: true,
                loadError: ActivationFailureMessage);
        }

        _extensions[index] = updated;
        return Task.FromResult(updated);
    }

    public Task<UrExtensionInfo> ResetExtensionAsync(
        string extensionId,
        CancellationToken ct = default)
    {
        ct.ThrowIfCancellationRequested();
        ResetExtensionCallCount++;
        ThrowIfMutationFails();

        var index = FindExtensionIndex(extensionId);
        var existing = _extensions[index];
        var updated = new UrExtensionInfo(
            existing.Id,
            existing.Name,
            existing.Tier,
            existing.Description,
            existing.Version,
            existing.DefaultEnabled,
            existing.DefaultEnabled,
            isActive: existing.DefaultEnabled,
            hasOverride: false,
            loadError: null);

        _extensions[index] = updated;
        return Task.FromResult(updated);
    }

    public IChatSession CreateSession() => new TestChatSession();

    public void SetExtensions(params UrExtensionInfo[] extensions)
    {
        _extensions.Clear();
        _extensions.AddRange(extensions);
    }

    private List<UrChatBlockingIssue> GetBlockingIssues()
    {
        var issues = new List<UrChatBlockingIssue>();

        if (string.IsNullOrWhiteSpace(_apiKey))
            issues.Add(UrChatBlockingIssue.MissingApiKey);

        if (string.IsNullOrWhiteSpace(_selectedModelId))
            issues.Add(UrChatBlockingIssue.MissingModelSelection);

        return issues;
    }

    private int FindExtensionIndex(string extensionId)
    {
        var index = _extensions.FindIndex(extension => extension.Id == extensionId);
        return index >= 0
            ? index
            : throw new ArgumentException($"Unknown extension ID '{extensionId}'.", nameof(extensionId));
    }

    private void ThrowIfMutationFails()
    {
        if (FailNextMutationMessage is null)
            return;

        var message = FailNextMutationMessage;
        FailNextMutationMessage = null;
        throw new InvalidOperationException(message);
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
