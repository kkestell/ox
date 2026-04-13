using Microsoft.Extensions.AI;

namespace Ox.Agent.Providers;

/// <summary>
/// Maps provider name prefixes to <see cref="IProvider"/> implementations, and
/// dispatches model-ID-keyed chat-client construction / request-option configuration.
///
/// Populated at startup by DI registration — each provider is registered once.
/// At runtime, sessions call <see cref="CreateChatClient"/> and
/// <see cref="ConfigureChatOptions"/> with a fully-qualified <see cref="ModelId"/>
/// string ("provider/model"); the registry parses the prefix, looks up the provider,
/// and forwards the call.
///
/// Having this dispatch live on the registry (rather than on OxHost) means every
/// caller who already holds a ProviderRegistry can create clients directly — there
/// is no indirection through a god object just to parse a model ID and hand off
/// to the right provider.
/// </summary>
internal sealed class ProviderRegistry
{
    private readonly Dictionary<string, IProvider> _providers =
        new(StringComparer.OrdinalIgnoreCase);

    /// <summary>
    /// Registers a provider. Throws if a provider with the same name is already registered.
    /// </summary>
    public void Register(IProvider provider)
    {
        if (!_providers.TryAdd(provider.Name, provider))
            throw new InvalidOperationException(
                $"Provider '{provider.Name}' is already registered.");
    }

    /// <summary>
    /// Looks up a provider by name. Returns null if no provider matches.
    /// </summary>
    public IProvider? Get(string name) =>
        _providers.GetValueOrDefault(name);

    /// <summary>
    /// All registered provider names, for use in help text and error messages.
    /// </summary>
    public IReadOnlyCollection<string> ProviderNames => _providers.Keys;

    /// <summary>
    /// Parses the "provider/model" form and constructs a chat client from the
    /// matching provider. Throws a descriptive <see cref="InvalidOperationException"/>
    /// when the provider prefix is unknown — this is the outermost boundary where
    /// "no such provider" is an actionable misconfiguration.
    /// </summary>
    public IChatClient CreateChatClient(string modelId)
    {
        var parsed = ModelId.Parse(modelId);
        var provider = GetOrThrow(parsed.Provider);
        return provider.CreateChatClient(parsed.Model);
    }

    /// <summary>
    /// Applies the matching provider's default per-turn options to a freshly-built
    /// <see cref="ChatOptions"/>. Kept alongside <see cref="CreateChatClient"/> so
    /// sessions using a test chat-client override still get the real provider's
    /// runtime option policy for the selected model.
    /// </summary>
    public void ConfigureChatOptions(string modelId, ChatOptions options)
    {
        var parsed = ModelId.Parse(modelId);
        var provider = GetOrThrow(parsed.Provider);
        provider.ConfigureChatOptions(parsed.Model, options);
    }

    private IProvider GetOrThrow(string providerName) =>
        Get(providerName)
            ?? throw new InvalidOperationException(
                $"Unknown provider '{providerName}'. Known providers: {string.Join(", ", _providers.Keys)}");
}
