using System.Text.Json;

namespace Ur.Extensions;

internal sealed class ExtensionDescriptor(
    ExtensionId id,
    string description,
    string version,
    string directory,
    IReadOnlyDictionary<string, JsonElement> settingsSchemas)
{
    public ExtensionId Id { get; } = id;
    public string Name => Id.Name;
    public string Description { get; } = description;
    public string Version { get; } = version;
    public ExtensionTier Tier => Id.Tier;
    public string Directory { get; } = directory;
    public IReadOnlyDictionary<string, JsonElement> SettingsSchemas { get; } = settingsSchemas;
    public bool DefaultEnabled => Tier is not ExtensionTier.Workspace;
}
