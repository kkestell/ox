namespace Ur.Providers;

/// <summary>
/// An LLM API backend in the provider registry.
/// </summary>
public sealed class ProviderDefinition
{
    public required string Id { get; init; }
    public required string DisplayName { get; init; }
    public required IReadOnlyList<string> ModelIds { get; init; }
}
