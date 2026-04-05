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
    private LuaState? _luaState;

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
    public bool Enabled => IsActive;
    public string? LoadError { get; private set; }
    public IReadOnlyList<AIFunction> Tools => _tools;
    public IReadOnlyDictionary<string, JsonElement> SettingsSchemas => _descriptor.SettingsSchemas;

    internal ExtensionId ExtensionId => _descriptor.Id;

    internal LuaState? LuaState
    {
        get => _luaState;
        set => _luaState = value;
    }

    internal void SetDesiredState(bool desiredEnabled, bool hasOverride)
    {
        DesiredEnabled = desiredEnabled;
        HasOverride = hasOverride;
    }

    internal void MarkActivated(ToolRegistry registry)
    {
        // Lua-defined tools are registered as WriteInWorkspace — the conservative safe
        // default. A future extension API could let tools self-declare their operation type.
        foreach (var tool in _tools)
            registry.Register(tool, OperationType.WriteInWorkspace, extensionId: Id);

        IsActive = true;
        LoadError = null;
    }

    internal void RegisterTool(AIFunction tool) => _tools.Add(tool);

    internal void MarkActivationFailed(ToolRegistry registry, string message)
    {
        ResetRuntimeState(registry);
        LoadError = message;
    }

    internal void MarkDeactivated(ToolRegistry registry)
    {
        ResetRuntimeState(registry);
        LoadError = null;
    }

    internal void ResetRuntimeState(ToolRegistry registry)
    {
        foreach (var tool in _tools)
            registry.Remove(tool.Name);

        _tools.Clear();
        _luaState?.Dispose();
        _luaState = null;
        IsActive = false;
    }
}
