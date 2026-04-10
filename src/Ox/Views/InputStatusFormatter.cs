namespace Ox.Views;

/// <summary>
/// Pure formatter for the composer status line. Keeping this outside the
/// Terminal.Gui view type lets tests verify the user-visible text contract
/// without triggering UI initialization side effects.
/// </summary>
internal static class InputStatusFormatter
{
    public static string? Compose(int? contextUsagePercent, string? modelId)
    {
        var hasPercent = contextUsagePercent is not null;
        var hasModel = !string.IsNullOrWhiteSpace(modelId);

        if (!hasPercent && !hasModel)
            return null;

        if (hasPercent && hasModel)
            return $"{contextUsagePercent}%  {modelId}";

        return hasPercent
            ? $"{contextUsagePercent}%"
            : modelId;
    }
}
