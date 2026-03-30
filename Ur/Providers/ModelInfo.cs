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
    IReadOnlyList<string> SupportedParameters);
