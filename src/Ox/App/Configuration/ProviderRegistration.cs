using Microsoft.Extensions.DependencyInjection;
using Ox.Agent.Configuration.Keyring;
using Ox.Agent.Providers;

namespace Ox.App.Configuration;

/// <summary>
/// Registers <see cref="IProvider"/> singletons from a <see cref="ProviderConfig"/>.
///
/// Uses key-based dispatch: the JSON key in providers.json determines which
/// concrete provider to construct. Built-in keys ("openai", "google", etc.)
/// map to dedicated provider projects. Unknown keys fall back to the generic
/// <see cref="Ox.Agent.Providers.OpenAiCompatible.OpenAiCompatibleProvider"/> for
/// any OpenAI-protocol-compatible service.
///
/// Called by Ox's Program.cs and shared by test infrastructure so the provider
/// construction logic is not duplicated.
/// </summary>
public static class ProviderRegistration
{
    /// <summary>
    /// Reads all entries from <paramref name="config"/> and registers the
    /// appropriate <see cref="IProvider"/> singleton for each one.
    /// Built-in provider keys are matched first; unknown keys use the
    /// OpenAI-compatible fallback (requires a <c>url</c> in providers.json).
    /// </summary>
    public static IServiceCollection AddProvidersFromConfig(
        this IServiceCollection services,
        ProviderConfig config)
    {
        foreach (var name in config.ProviderNames)
        {
            var entry = config.GetEntry(name)!;

            // Key-based dispatch: the JSON key determines the provider type.
            // Built-in providers have hardcoded defaults; unknown keys fall
            // through to the generic OpenAI-compatible provider.
            switch (name)
            {
                case "openai":
                    var openAiEndpoint = entry.Endpoint;
                    services.AddSingleton<IProvider>(sp =>
                        new Ox.Agent.Providers.OpenAI.OpenAiProvider(
                            sp.GetRequiredService<IKeyring>(), openAiEndpoint));
                    break;

                case "google":
                    services.AddSingleton<IProvider>(sp =>
                        new Ox.Agent.Providers.Google.GoogleProvider(
                            sp.GetRequiredService<IKeyring>()));
                    break;

                case "openrouter":
                    var orEndpoint = entry.Endpoint;
                    services.AddSingleton<IProvider>(sp =>
                        new Ox.Agent.Providers.OpenRouter.OpenRouterProvider(
                            sp.GetRequiredService<IKeyring>(), orEndpoint));
                    break;

                case "ollama":
                    var ollamaEndpoint = entry.Endpoint;
                    services.AddSingleton<IProvider>(
                        new Ox.Agent.Providers.Ollama.OllamaProvider(ollamaEndpoint));
                    break;

                case "zai-coding":
                    var zaiEndpoint = entry.Endpoint;
                    services.AddSingleton<IProvider>(sp =>
                        new Ox.Agent.Providers.ZaiCoding.ZaiCodingProvider(
                            sp.GetRequiredService<IKeyring>(), zaiEndpoint));
                    break;

                default:
                    // Unknown key — treat as a custom OpenAI-compatible provider.
                    // Requires a url field so we know where to send requests.
                    if (entry.Endpoint is null) break;
                    var customName = name;
                    var customDisplayName = !string.IsNullOrEmpty(entry.Name) ? entry.Name : customName;
                    var customEndpoint = entry.Endpoint;
                    services.AddSingleton<IProvider>(sp =>
                        new Ox.Agent.Providers.OpenAiCompatible.OpenAiCompatibleProvider(
                            customName, customDisplayName, customEndpoint, sp.GetRequiredService<IKeyring>()));
                    break;
            }
        }

        return services;
    }
}
