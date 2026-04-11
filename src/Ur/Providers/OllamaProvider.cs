using Microsoft.Extensions.AI;
using OllamaSharp;
using Ur.Settings;

namespace Ur.Providers;

/// <summary>
/// Provider for local Ollama models. No API key is needed — Ollama runs locally.
/// The endpoint URI defaults to http://localhost:11434 but can be overridden via
/// the "ollama.uri" setting.
///
/// <see cref="OllamaApiClient"/> directly implements <see cref="IChatClient"/>,
/// so no adapter is needed.
///
/// Takes a <see cref="SettingsWriter"/> rather than <see cref="Configuration.UrConfiguration"/>
/// to avoid a circular dependency: UrConfiguration depends on the ProviderRegistry,
/// and the registry contains this provider.
/// </summary>
internal sealed class OllamaProvider : IProvider
{
    /// <summary>
    /// The settings key for the Ollama endpoint URI. Registered in
    /// <see cref="Hosting.ServiceCollectionExtensions.RegisterCoreSchemas"/>.
    /// </summary>
    internal const string UriSettingKey = "ollama.uri";

    private static readonly Uri DefaultUri = new("http://localhost:11434");

    private readonly SettingsWriter _settingsWriter;

    public OllamaProvider(SettingsWriter settingsWriter)
    {
        _settingsWriter = settingsWriter;
    }

    public string Name => "ollama";
    public bool RequiresApiKey => false;

    public IChatClient CreateChatClient(string model)
    {
        var uri = ResolveUri();
        return new OllamaApiClient(uri, model);
    }

    /// <summary>
    /// Ollama needs no API key, so it's always ready. A future enhancement could
    /// ping the Ollama endpoint to verify it's reachable, but that would add
    /// latency to every readiness check.
    /// </summary>
    public string? GetBlockingIssue() => null;

    private Uri ResolveUri()
    {
        var element = _settingsWriter.Get(UriSettingKey);
        var uriString = element is { ValueKind: System.Text.Json.JsonValueKind.String } je
            ? je.GetString()
            : null;

        if (string.IsNullOrWhiteSpace(uriString))
            return DefaultUri;

        return Uri.TryCreate(uriString, UriKind.Absolute, out var uri)
            ? uri
            : DefaultUri;
    }
}
