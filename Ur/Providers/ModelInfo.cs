namespace Ur.Providers;

/// <summary>
/// Model metadata fetched from the OpenRouter API.
/// </summary>
public sealed record ModelInfo(
    string Id,
    string Name,
    int ContextLength,
    int MaxOutputTokens,
    decimal InputCostPerToken,
    decimal OutputCostPerToken,
    IReadOnlyList<string> SupportedParameters,
    string? Modality)
{
    public decimal InputCostPerMToken => InputCostPerToken * 1_000_000;
    public decimal OutputCostPerMToken => OutputCostPerToken * 1_000_000;
}
