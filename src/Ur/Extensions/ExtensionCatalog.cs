using System.Text.Json;
using Ur.AgentLoop;

namespace Ur.Extensions;

/// <summary>
/// Provides listing and enablement management for extensions discovered in the current host.
/// </summary>
public sealed class ExtensionCatalog
{
    private readonly IReadOnlyList<Extension> _extensions;
    private readonly Dictionary<ExtensionId, Extension> _extensionsById;
    private readonly Dictionary<ExtensionId, bool> _globalOverrides;
    private readonly Dictionary<ExtensionId, bool> _workspaceOverrides;
    private readonly ExtensionOverrideStore _overrideStore;
    private readonly SemaphoreSlim _gate = new(1, 1);

    private ExtensionCatalog(
        IReadOnlyList<Extension> extensions,
        ExtensionOverrideStore overrideStore,
        IReadOnlyDictionary<ExtensionId, bool> globalOverrides,
        IReadOnlyDictionary<ExtensionId, bool> workspaceOverrides)
    {
        _extensions = extensions;
        _overrideStore = overrideStore;
        _extensionsById = extensions.ToDictionary(extension => extension.ExtensionId);
        _globalOverrides = new Dictionary<ExtensionId, bool>(globalOverrides);
        _workspaceOverrides = new Dictionary<ExtensionId, bool>(workspaceOverrides);
    }

    internal static async Task<ExtensionCatalog> CreateAsync(
        IReadOnlyList<Extension> extensions,
        ExtensionOverrideStore overrideStore,
        CancellationToken ct = default)
    {
        var snapshot = await overrideStore.LoadAsync(ct).ConfigureAwait(false);

        foreach (var extension in extensions)
        {
            var overrides = extension.Tier is ExtensionTier.Workspace
                ? snapshot.Workspace
                : snapshot.Global;
            var hasOverride = overrides.TryGetValue(extension.ExtensionId, out var enabled);
            var desiredEnabled = hasOverride ? enabled : extension.DefaultEnabled;

            extension.SetDesiredState(desiredEnabled, hasOverride);
            if (desiredEnabled)
            {
                await ExtensionLoader.ActivateAsync(extension, ct)
                    .ConfigureAwait(false);
            }
        }

        return new ExtensionCatalog(
            extensions,
            overrideStore,
            snapshot.Global,
            snapshot.Workspace);
    }

    /// <summary>
    /// Registers all active extension tools into the given registry. Called per-session
    /// when building a fresh tool set — each session gets its own snapshot of tools.
    /// </summary>
    internal void RegisterActiveToolsInto(ToolRegistry registry)
    {
        foreach (var extension in _extensions)
        {
            if (extension.IsActive)
                extension.RegisterToolsInto(registry);
        }
    }

    /// <summary>
    /// Returns a stable snapshot of every discovered extension for this host.
    /// </summary>
    public IReadOnlyList<ExtensionInfo> List() =>
        _extensions.Select(ToInfo).ToList();

    /// <summary>
    /// Sets whether the given extension should be enabled for its native scope.
    /// </summary>
    public async Task<ExtensionInfo> SetEnabledAsync(
        string extensionId,
        bool enabled,
        CancellationToken ct = default)
    {
        var extension = GetRequiredExtension(extensionId);

        await _gate.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            await PersistDesiredStateAsync(extension, enabled, ct).ConfigureAwait(false);
            extension.SetDesiredState(enabled, enabled != extension.DefaultEnabled);

            if (enabled)
            {
                await ExtensionLoader.ActivateAsync(extension, ct)
                    .ConfigureAwait(false);
            }
            else
            {
                ExtensionLoader.Deactivate(extension);
            }

            return ToInfo(extension);
        }
        finally
        {
            _gate.Release();
        }
    }

    /// <summary>
    /// Removes any persisted override for the given extension and restores the tier default.
    /// </summary>
    public async Task<ExtensionInfo> ResetAsync(
        string extensionId,
        CancellationToken ct = default)
    {
        var extension = GetRequiredExtension(extensionId);
        var defaultEnabled = extension.DefaultEnabled;

        await _gate.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            await PersistDesiredStateAsync(extension, defaultEnabled, ct).ConfigureAwait(false);
            extension.SetDesiredState(defaultEnabled, hasOverride: false);

            if (defaultEnabled)
            {
                await ExtensionLoader.ActivateAsync(extension, ct)
                    .ConfigureAwait(false);
            }
            else
            {
                ExtensionLoader.Deactivate(extension);
            }

            return ToInfo(extension);
        }
        finally
        {
            _gate.Release();
        }
    }

    private async Task PersistDesiredStateAsync(
        Extension extension,
        bool desiredEnabled,
        CancellationToken ct)
    {
        var overrides = extension.Tier is ExtensionTier.Workspace
            ? _workspaceOverrides
            : _globalOverrides;
        var hasOverride = desiredEnabled != extension.DefaultEnabled;
        var updatedOverrides = new Dictionary<ExtensionId, bool>(overrides);

        if (hasOverride)
            updatedOverrides[extension.ExtensionId] = desiredEnabled;
        else
            updatedOverrides.Remove(extension.ExtensionId);

        if (extension.Tier is ExtensionTier.Workspace)
        {
            await _overrideStore.WriteWorkspaceAsync(updatedOverrides, ct).ConfigureAwait(false);
            CopyOverrides(_workspaceOverrides, updatedOverrides);
            return;
        }

        await _overrideStore.WriteGlobalAsync(updatedOverrides, ct).ConfigureAwait(false);
        CopyOverrides(_globalOverrides, updatedOverrides);
    }

    private Extension GetRequiredExtension(string extensionId)
    {
        if (!ExtensionId.TryParse(extensionId, out var parsedId) ||
            !_extensionsById.TryGetValue(parsedId, out var extension))
        {
            throw new ArgumentException(
                $"Unknown extension ID '{extensionId}'.",
                nameof(extensionId));
        }

        return extension;
    }

    /// <summary>
    /// Returns the settings schemas declared by a specific extension, keyed by setting name.
    /// Throws <see cref="ArgumentException"/> if the extension ID is not recognised.
    /// This is the public API counterpart to the per-session tool registration path;
    /// it allows CLI and UI tooling to inspect extension configuration without a running session.
    /// </summary>
    public IReadOnlyDictionary<string, JsonElement> GetExtensionSettings(string extensionId)
    {
        var extension = GetRequiredExtension(extensionId);
        return extension.SettingsSchemas;
    }

    /// <summary>
    /// Copies all entries from <paramref name="source"/> into <paramref name="destination"/>,
    /// clearing any existing entries first. Mutates in-place rather than swapping the
    /// reference so that the field stays pointing at the same dictionary instance.
    /// </summary>
    private static void CopyOverrides(
        Dictionary<ExtensionId, bool> destination,
        Dictionary<ExtensionId, bool> source)
    {
        destination.Clear();
        foreach (var (id, enabled) in source)
            destination[id] = enabled;
    }

    private static ExtensionInfo ToInfo(Extension extension) =>
        new(
            extension.Id,
            extension.Name,
            extension.Tier,
            extension.Description,
            extension.Version,
            extension.DefaultEnabled,
            extension.DesiredEnabled,
            extension.IsActive,
            extension.HasOverride,
            extension.LoadError,
            extension.SettingsSchemas);
}
