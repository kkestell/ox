namespace Ur.Configuration;

/// <summary>
/// Strongly-typed options for core Ur settings, bound to the "ur" section of
/// <see cref="Microsoft.Extensions.Configuration.IConfiguration"/>. Currently
/// holds only the selected model ID; extension settings stay on IConfiguration
/// with string-based access since their schemas are dynamic.
/// </summary>
public sealed class UrOptions
{
    /// <summary>
    /// The user's selected model identifier (e.g. "openai/gpt-4o").
    /// Bound from the "ur:model" configuration key. Null when unset.
    /// </summary>
    public string? Model { get; set; }
}
