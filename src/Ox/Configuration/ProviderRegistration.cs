using Microsoft.Extensions.DependencyInjection;
using Ur.Configuration.Keyring;
using Ur.Providers;

namespace Ox.Configuration;

/// <summary>
/// Registers <see cref="IProvider"/> singletons from a <see cref="ProviderConfig"/>.
///
/// The type-switch over provider types (openai-compatible, google, ollama) lives
/// here in Ox because the concrete provider classes are internal to Ur with
/// InternalsVisibleTo("Ox"). External consumers implement <see cref="IProvider"/>
/// from scratch and register directly via DI.
///
/// Called by Ox's Program.cs and shared by test infrastructure so the provider
/// construction logic is not duplicated.
/// </summary>
public static class ProviderRegistration
{
    /// <summary>
    /// Reads all entries from <paramref name="config"/> and registers the
    /// appropriate <see cref="IProvider"/> singleton for each one.
    /// Unknown provider types are silently skipped (forward-compatible with
    /// newer providers.json formats).
    /// </summary>
    public static IServiceCollection AddProvidersFromConfig(
        this IServiceCollection services,
        ProviderConfig config)
    {
        foreach (var name in config.ProviderNames)
        {
            var entry = config.GetEntry(name)!;

            switch (entry.Type)
            {
                case "openai-compatible":
                    var endpoint = entry.Endpoint;
                    services.AddSingleton<IProvider>(sp =>
                        new OpenAiCompatibleProvider(
                            name, endpoint, sp.GetRequiredService<IKeyring>()));
                    break;

                case "google":
                    services.AddSingleton<IProvider>(sp =>
                        new GoogleProvider(sp.GetRequiredService<IKeyring>()));
                    break;

                case "ollama":
                    var ollamaUri = entry.Endpoint ?? new Uri("http://localhost:11434");
                    services.AddSingleton<IProvider>(
                        new OllamaProvider(name, ollamaUri));
                    break;

                default:
                    // Unknown provider type — skip silently. The user may have
                    // a newer providers.json format than this version supports.
                    break;
            }
        }

        return services;
    }
}
