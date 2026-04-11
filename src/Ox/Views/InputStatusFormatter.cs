namespace Ox.Views;

/// <summary>
/// Composes the right-aligned status text that appears on the input area's
/// status line: context fill percentage + model ID.
/// </summary>
public static class InputStatusFormatter
{
    /// <summary>
    /// Format the combined status string. Returns null when neither percentage
    /// nor model is available (e.g. before the first configuration).
    /// </summary>
    /// <param name="contextPercent">
    /// Context window fill percentage (0–100), or null if no turn has completed yet.
    /// </param>
    /// <param name="modelId">
    /// Active model ID (e.g. "google/gemini-3-flash-preview"), or null if unknown.
    /// </param>
    public static string? Compose(int? contextPercent, string? modelId)
    {
        if (contextPercent is not null && modelId is not null)
            return $"{contextPercent}%  {modelId}";

        if (modelId is not null)
            return modelId;

        return null;
    }
}
