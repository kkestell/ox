using System.Text.Json;

namespace Ur.Extensions;

internal sealed class ExtensionDescriptor
{
    public ExtensionDescriptor(
        ExtensionId id,
        string description,
        string version,
        string directory,
        IReadOnlyDictionary<string, JsonElement> settingsSchemas)
    {
        Id = id;
        Description = description;
        Version = version;
        Directory = directory;
        SettingsSchemas = settingsSchemas;
    }

    public ExtensionId Id { get; }
    public string Name => Id.Name;
    public string Description { get; }
    public string Version { get; }
    public ExtensionTier Tier => Id.Tier;
    public string Directory { get; }
    public IReadOnlyDictionary<string, JsonElement> SettingsSchemas { get; }
    public bool DefaultEnabled => Tier is not ExtensionTier.Workspace;
}
