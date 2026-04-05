using System.Text.Json;
using Lua;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;

namespace Ur.Extensions;

/// <summary>
/// Lifecycle states for an extension's runtime. Only <see cref="Active"/> has a
/// live <see cref="LuaState"/>; the others represent idle or error conditions.
/// </summary>
internal enum ExtensionState
{
    /// <summary>Extension is not running — no Lua runtime, no tools.</summary>
    Inactive,
    /// <summary>Extension is loaded and running — Lua runtime and tools are live.</summary>
    Active,
    /// <summary>Activation was attempted but failed — error message is set.</summary>
    Failed
}

/// <summary>
/// A discovered extension together with its transient runtime state.
/// </summary>
public sealed class Extension
{
    private readonly List<AIFunction> _tools = [];
    private readonly ExtensionDescriptor _descriptor;

    internal Extension(ExtensionDescriptor descriptor)
    {
        _descriptor = descriptor;
    }

    public string Id => _descriptor.Id.ToString();
    public string Name => _descriptor.Name;
    public string Description => _descriptor.Description;
    public string Version => _descriptor.Version;
    public ExtensionTier Tier => _descriptor.Tier;
    public string Directory => _descriptor.Directory;
    public bool DefaultEnabled => _descriptor.DefaultEnabled;
    public bool DesiredEnabled { get; private set; }
    public bool HasOverride { get; private set; }
    public bool IsActive { get; private set; }
    public string? LoadError { get; private set; }
    public IReadOnlyList<AIFunction> Tools => _tools;
    public IReadOnlyDictionary<string, JsonElement> SettingsSchemas => _descriptor.SettingsSchemas;

    internal ExtensionId ExtensionId => _descriptor.Id;

    internal LuaState? LuaState { get; private set; }

    internal void SetDesiredState(bool desiredEnabled, bool hasOverride)
    {
        DesiredEnabled = desiredEnabled;
        HasOverride = hasOverride;
    }

    /// <summary>
    /// Transitions the extension to the given lifecycle state. Centralizes all
    /// state-mutation logic so callers (primarily <see cref="ExtensionLoader"/>)
    /// don't manipulate individual properties directly.
    ///
    /// <paramref name="lua"/> is required when transitioning to <see cref="ExtensionState.Active"/>
    /// with a Lua runtime (pass null for manifest-only extensions with no main.lua).
    /// <paramref name="error"/> is required when transitioning to <see cref="ExtensionState.Failed"/>.
    /// </summary>
    internal void ApplyState(ExtensionState state, LuaState? lua = null, string? error = null)
    {
        switch (state)
        {
            case ExtensionState.Active:
                // If a new Lua runtime is provided, assign it and mark active.
                // If lua is null, this is a manifest-only activation (no main.lua).
                if (lua is not null)
                    LuaState = lua;
                IsActive = true;
                LoadError = null;
                break;

            case ExtensionState.Inactive:
                ResetRuntimeState();
                LoadError = null;
                break;

            case ExtensionState.Failed:
                ResetRuntimeState();
                LoadError = error;
                break;

            default:
                throw new ArgumentOutOfRangeException(nameof(state), state, null);
        }
    }

    internal void RegisterTool(AIFunction tool) => _tools.Add(tool);

    /// <summary>
    /// Copies this extension's tools into the given registry with the correct
    /// permission metadata. Called per-session when building a fresh tool set.
    /// </summary>
    internal void RegisterToolsInto(ToolRegistry registry)
    {
        // Lua-defined tools are registered as WriteInWorkspace — the conservative safe
        // default. A future extension API could let tools self-declare their operation type.
        foreach (var tool in _tools)
            registry.Register(tool, extensionId: Id);
    }

    private void ResetRuntimeState()
    {
        _tools.Clear();
        LuaState?.Dispose();
        LuaState = null;
        IsActive = false;
    }
}
