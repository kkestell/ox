namespace Ox.Agent.Providers;

/// <summary>
/// Splits a raw model identifier into its provider prefix and model portion.
///
/// Convention: the first slash-delimited segment is the provider, the remainder
/// is the model name passed to that provider's SDK. This means OpenRouter models
/// (which use their own slash-delimited namespacing like "anthropic/claude-3.5-sonnet")
/// are addressed as "openrouter/anthropic/claude-3.5-sonnet" — the provider owns
/// everything after the first slash.
/// </summary>
internal readonly record struct ModelId(string Provider, string Model)
{
    /// <summary>
    /// Parses a raw model string like "openai/gpt-5-nano" or
    /// "openrouter/anthropic/claude-3.5-sonnet" into its provider and model parts.
    /// </summary>
    /// <exception cref="ArgumentException">
    /// Thrown when the input is null, empty, or contains no slash separator.
    /// </exception>
    public static ModelId Parse(string raw)
    {
        if (string.IsNullOrWhiteSpace(raw))
            throw new ArgumentException("Model ID cannot be empty.", nameof(raw));

        var slashIndex = raw.IndexOf('/');
        if (slashIndex < 1 || slashIndex == raw.Length - 1)
            throw new ArgumentException(
                $"Model ID '{raw}' must be in 'provider/model' format (e.g. 'openai/gpt-5-nano').",
                nameof(raw));

        return new ModelId(
            Provider: raw[..slashIndex],
            Model: raw[(slashIndex + 1)..]);
    }
}
