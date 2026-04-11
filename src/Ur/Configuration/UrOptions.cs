namespace Ur.Configuration;

/// <summary>
/// Strongly-typed options for core Ur settings, bound to the "ur" section of
/// <see cref="Microsoft.Extensions.Configuration.IConfiguration"/>. Currently
/// holds only the selected model ID.
/// </summary>
public sealed class UrOptions
{
    /// <summary>
    /// The user's selected model identifier (e.g. "openai/gpt-4o").
    /// Bound from the "ur:model" configuration key. Null when unset.
    /// </summary>
    public string? Model { get; set; }

    /// <summary>
    /// How many recent assistant turns' tool results are preserved verbatim
    /// during the <see cref="BuildLlmMessages"/> projection. Tool results in
    /// older turns are replaced with "[Tool result cleared]" to reclaim context
    /// window space without mutating the persisted message history.
    /// Bound from "ur:turnsToKeepToolResults". Default is 3.
    /// </summary>
    public int TurnsToKeepToolResults { get; set; } = 3;
}
