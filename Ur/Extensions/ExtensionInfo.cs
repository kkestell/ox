namespace Ur.Extensions;

/// <summary>
/// Metadata for a loaded extension.
/// </summary>
public sealed class ExtensionInfo
{
    public required string Name { get; init; }
    public required string? Description { get; init; }
    public required string? Version { get; init; }
    public required ExtensionTier Tier { get; init; }
    public required string Directory { get; init; }
    public bool Enabled { get; set; }
}
