namespace Ur.Providers;

public sealed record ModelProperties(
    int MaxContextLength,
    int MaxOutputLength,
    decimal CostPerInputToken,
    decimal CostPerOutputToken,
    bool SupportsToolCalling,
    bool SupportsStreaming);
