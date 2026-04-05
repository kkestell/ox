using System.Text.Json;

namespace Ur.Extensions;

/// <summary>
/// A UI-friendly snapshot of one discovered extension and its current runtime status.
/// </summary>
public sealed class ExtensionInfo
{
    /// <summary>
    /// Creates a new extension snapshot.
    /// </summary>
    public ExtensionInfo(
        string id,
        string name,
        ExtensionTier tier,
        string description,
        string version,
        bool defaultEnabled,
        bool desiredEnabled,
        bool isActive,
        bool hasOverride,
        string? loadError,
        IReadOnlyDictionary<string, JsonElement> settingsSchemas)
    {
        Id = id;
        Name = name;
        Tier = tier;
        Description = description;
        Version = version;
        DefaultEnabled = defaultEnabled;
        DesiredEnabled = desiredEnabled;
        IsActive = isActive;
        HasOverride = hasOverride;
        LoadError = loadError;
        SettingsSchemas = settingsSchemas;
    }

    /// <summary>
    /// Gets the stable serialized extension ID in the form <c>&lt;tier&gt;:&lt;name&gt;</c>.
    /// </summary>
    public string Id { get; }

    /// <summary>
    /// Gets the manifest name of the extension.
    /// </summary>
    public string Name { get; }

    /// <summary>
    /// Gets the tier from which the extension was discovered.
    /// </summary>
    public ExtensionTier Tier { get; }

    /// <summary>
    /// Gets the manifest description for the extension.
    /// Captured for potential UI display though not currently accessed by consumers.
    /// </summary>
    // ReSharper disable once UnusedAutoPropertyAccessor.Global — public API, retained for UI consumers
    public string Description { get; }

    /// <summary>
    /// Gets the manifest version for the extension.
    /// </summary>
    public string Version { get; }

    /// <summary>
    /// Gets a value indicating whether the extension is enabled by default for its tier.
    /// </summary>
    public bool DefaultEnabled { get; }

    /// <summary>
    /// Gets a value indicating whether the current effective state should enable the extension.
    /// </summary>
    public bool DesiredEnabled { get; }

    /// <summary>
    /// Gets a value indicating whether the extension is currently active in this process.
    /// </summary>
    public bool IsActive { get; }

    /// <summary>
    /// Gets a value indicating whether the effective state comes from a persisted override.
    /// </summary>
    public bool HasOverride { get; }

    /// <summary>
    /// Gets the last activation error captured in the current process, if any.
    /// </summary>
    public string? LoadError { get; }

    /// <summary>
    /// Gets the JSON Schema definitions declared by this extension, keyed by setting name.
    /// This mirrors the extension's settings registration and allows tooling to surface
    /// what configuration the extension accepts without loading Lua.
    /// </summary>
    // ReSharper disable once UnusedAutoPropertyAccessor.Global — public API, consumed by ExtensionCatalog.GetExtensionSettings() and CLI
    public IReadOnlyDictionary<string, JsonElement> SettingsSchemas { get; }
}
