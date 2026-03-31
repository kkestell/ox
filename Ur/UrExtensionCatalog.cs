using System.Threading;
using Ur.AgentLoop;
using Ur.Extensions;

namespace Ur;

/// <summary>
/// Provides listing and enablement management for extensions discovered in the current host.
/// </summary>
public sealed class UrExtensionCatalog
{
    private readonly IReadOnlyList<Extension> _extensions;
    private readonly Dictionary<ExtensionId, Extension> _extensionsById;
    private readonly Dictionary<ExtensionId, bool> _globalOverrides;
    private readonly Dictionary<ExtensionId, bool> _workspaceOverrides;
    private readonly ExtensionOverrideStore _overrideStore;
    private readonly ToolRegistry _toolRegistry;
    private readonly SemaphoreSlim _gate = new(1, 1);

    internal UrExtensionCatalog(
        IReadOnlyList<Extension> extensions,
        ExtensionOverrideStore overrideStore,
        ToolRegistry toolRegistry,
        IReadOnlyDictionary<ExtensionId, bool> globalOverrides,
        IReadOnlyDictionary<ExtensionId, bool> workspaceOverrides)
    {
        _extensions = extensions;
        _overrideStore = overrideStore;
        _toolRegistry = toolRegistry;
        _extensionsById = extensions.ToDictionary(extension => extension.ExtensionId);
        _globalOverrides = new Dictionary<ExtensionId, bool>(globalOverrides);
        _workspaceOverrides = new Dictionary<ExtensionId, bool>(workspaceOverrides);
    }

    internal static async Task<UrExtensionCatalog> CreateAsync(
        IReadOnlyList<Extension> extensions,
        ExtensionOverrideStore overrideStore,
        ToolRegistry toolRegistry,
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
                await ExtensionLoader.ActivateAsync(extension, toolRegistry, ct)
                    .ConfigureAwait(false);
            }
        }

        return new UrExtensionCatalog(
            extensions,
            overrideStore,
            toolRegistry,
            snapshot.Global,
            snapshot.Workspace);
    }

    /// <summary>
    /// Returns a stable snapshot of every discovered extension for this host.
    /// </summary>
    public IReadOnlyList<UrExtensionInfo> List() =>
        _extensions.Select(ToInfo).ToList();

    /// <summary>
    /// Sets whether the given extension should be enabled for its native scope.
    /// </summary>
    public async Task<UrExtensionInfo> SetEnabledAsync(
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
                await ExtensionLoader.ActivateAsync(extension, _toolRegistry, ct)
                    .ConfigureAwait(false);
            }
            else
            {
                ExtensionLoader.Deactivate(extension, _toolRegistry);
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
    public async Task<UrExtensionInfo> ResetAsync(
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
                await ExtensionLoader.ActivateAsync(extension, _toolRegistry, ct)
                    .ConfigureAwait(false);
            }
            else
            {
                ExtensionLoader.Deactivate(extension, _toolRegistry);
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
            ReplaceOverrides(_workspaceOverrides, updatedOverrides);
            return;
        }

        await _overrideStore.WriteGlobalAsync(updatedOverrides, ct).ConfigureAwait(false);
        ReplaceOverrides(_globalOverrides, updatedOverrides);
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

    private static void ReplaceOverrides(
        Dictionary<ExtensionId, bool> destination,
        Dictionary<ExtensionId, bool> source)
    {
        destination.Clear();
        foreach (var (id, enabled) in source)
            destination[id] = enabled;
    }

    private static UrExtensionInfo ToInfo(Extension extension) =>
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
            extension.LoadError);
}
