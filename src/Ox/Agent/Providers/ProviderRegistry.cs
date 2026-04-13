namespace Ox.Agent.Providers;

/// <summary>
/// Maps provider name prefixes to <see cref="IProvider"/> implementations.
///
/// Populated at startup by DI registration — each provider is registered once.
/// At runtime, <see cref="Hosting.OxHost.CreateChatClient"/> parses a <see cref="ModelId"/>
/// and looks up the provider here to delegate client construction.
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
}
