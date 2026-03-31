using System.Text.Json;
using Lua;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;

namespace Ur.Extensions;

/// <summary>
/// A loaded extension. Holds metadata, Lua runtime state, registered tools,
/// and enable/disable lifecycle. Created by <see cref="ExtensionLoader"/>.
/// </summary>
public sealed class Extension
{
    private readonly List<AIFunction> _tools = [];
    private LuaState? _luaState;

    internal Extension(
        string name,
        string description,
        string version,
        ExtensionTier tier,
        string directory,
        IReadOnlyDictionary<string, JsonElement> settingsSchemas)
    {
        Name = name;
        Description = description;
        Version = version;
        Tier = tier;
        Directory = directory;
        SettingsSchemas = settingsSchemas;
    }

    public string Name { get; }
    public string Description { get; }
    public string Version { get; }
    public ExtensionTier Tier { get; }
    public string Directory { get; }
    public bool Enabled { get; private set; }
    public IReadOnlyList<AIFunction> Tools => _tools;
    public IReadOnlyDictionary<string, JsonElement> SettingsSchemas { get; }

    internal LuaState? LuaState
    {
        get => _luaState;
        set => _luaState = value;
    }

    /// <summary>
    /// Registers all tools into the registry and marks the extension as enabled.
    /// </summary>
    public void Enable(ToolRegistry registry)
    {
        foreach (var tool in _tools)
            registry.Register(tool);
        Enabled = true;
    }

    /// <summary>
    /// Removes all tools from the registry and marks the extension as disabled.
    /// </summary>
    public void Disable(ToolRegistry registry)
    {
        foreach (var tool in _tools)
            registry.Remove(tool.Name);
        Enabled = false;
    }

    /// <summary>
    /// Called during main.lua execution to record a tool registered by the extension.
    /// </summary>
    internal void RegisterTool(AIFunction tool) => _tools.Add(tool);
}
