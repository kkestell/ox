using System.ClientModel;
using Microsoft.Extensions.AI;
using OpenAI;
using Ur.Configuration.Keyring;

namespace Ur.Providers;

/// <summary>
/// Generic provider for any API that speaks the OpenAI protocol. Replaces the
/// three near-identical providers (OpenAI, OpenRouter, ZaiCoding) with a single
/// implementation parameterized by name and optional custom endpoint URI.
///
/// The provider name doubles as the OS keyring account for API key storage,
/// following the existing convention: service="ur", account=provider-name.
///
/// Models flagged with <c>"thinking": true</c> in providers.json, or whose ID
/// contains "deepseek-r1", are wrapped with <see cref="DeepSeekThinkingChatClient"/>
/// to extract <c>&lt;think&gt;…&lt;/think&gt;</c> blocks from the text stream and
/// surface them as <see cref="Microsoft.Extensions.AI.TextReasoningContent"/>.
/// This keeps the thinking-enable logic inside the provider layer where provider
/// quirks belong, rather than leaking it into AgentLoop.
/// </summary>
internal sealed class OpenAiCompatibleProvider : IProvider
{
    private const string SecretService = "ur";

    private readonly string _name;
    private readonly Uri? _endpoint;
    private readonly IKeyring _keyring;

    // Model IDs (without provider prefix) that emit <think>...</think> blocks in
    // normal TextContent and therefore need the extraction wrapper.
    private readonly IReadOnlySet<string> _thinkingModelIds;

    /// <param name="name">
    /// The provider name from providers.json (e.g. "openai", "openrouter", "zai-coding").
    /// Used as the keyring account and the prefix in model IDs.
    /// </param>
    /// <param name="endpoint">
    /// Optional custom endpoint URI. When null, the standard OpenAI endpoint is used.
    /// OpenRouter and ZaiCoding set this to their respective API base URLs.
    /// </param>
    /// <param name="keyring">OS keyring for API key resolution.</param>
    /// <param name="thinkingModelIds">
    /// Set of model IDs (without provider prefix) that need the DeepSeek thinking
    /// wrapper. Populated from <c>"thinking": true</c> entries in providers.json.
    /// </param>
    public OpenAiCompatibleProvider(
        string name,
        Uri? endpoint,
        IKeyring keyring,
        IReadOnlySet<string>? thinkingModelIds = null)
    {
        _name = name;
        _endpoint = endpoint;
        _keyring = keyring;
        _thinkingModelIds = thinkingModelIds ?? new HashSet<string>(StringComparer.OrdinalIgnoreCase);
    }

    public string Name => _name;
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, _name)
            ?? throw new InvalidOperationException(
                $"No API key configured for '{_name}'. Run: ur config set-api-key <key> --provider {_name}");

        IChatClient chatClient;
        if (_endpoint is not null)
        {
            // Custom endpoint — three-arg constructor with options.
            var options = new OpenAIClientOptions { Endpoint = _endpoint };
            chatClient = new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey), options)
                .AsIChatClient();
        }
        else
        {
            // Standard OpenAI endpoint — two-arg constructor.
            chatClient = new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey))
                .AsIChatClient();
        }

        // Apply the <think>-extraction wrapper for models that embed reasoning traces
        // in normal text output. Matches explicit providers.json flags or the
        // well-known "deepseek-r1" model-ID fragment.
        var needsThinkingWrapper = _thinkingModelIds.Contains(model)
            || model.Contains("deepseek-r1", StringComparison.OrdinalIgnoreCase);

        return needsThinkingWrapper ? new DeepSeekThinkingChatClient(chatClient) : chatClient;
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, _name);
        return string.IsNullOrWhiteSpace(key)
            ? $"No API key for '{_name}'. Run: ur config set-api-key <key> --provider {_name}"
            : null;
    }
}
