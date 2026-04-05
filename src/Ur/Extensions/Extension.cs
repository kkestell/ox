using System.Text.Json;
using Lua;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Permissions;

namespace Ur.Extensions;

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

    internal LuaState? LuaState { get; set; }

    internal void SetDesiredState(bool desiredEnabled, bool hasOverride)
    {
        DesiredEnabled = desiredEnabled;
        HasOverride = hasOverride;
    }

    internal void MarkActivated()
    {
        IsActive = true;
        LoadError = null;
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
            registry.Register(tool, OperationType.WriteInWorkspace, extensionId: Id);
    }

    internal void MarkActivationFailed(string message)
    {
        ResetRuntimeState();
        LoadError = message;
    }

    internal void MarkDeactivated()
    {
        ResetRuntimeState();
        LoadError = null;
    }

    internal void ResetRuntimeState()
    {
        _tools.Clear();
        LuaState?.Dispose();
        LuaState = null;
        IsActive = false;
    }
}
