using System.Text.Json;

namespace Ur.Providers;

/// <summary>
/// A specific LLM in the provider registry.
/// </summary>
public sealed class ModelDefinition
{
    public required string Id { get; init; }
    public required string ProviderId { get; init; }
    public required ModelProperties Properties { get; init; }
    public required JsonElement SettingsSchema { get; init; }
}
