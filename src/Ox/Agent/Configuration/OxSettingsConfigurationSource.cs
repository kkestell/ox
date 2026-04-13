using Microsoft.Extensions.Configuration;

namespace Ox.Agent.Configuration;

/// <summary>
/// Configuration source that creates a <see cref="OxSettingsConfigurationProvider"/>
/// for a single settings JSON file. Two instances are registered in the configuration
/// pipeline — one for user-level settings and one for workspace-level settings —
/// with the workspace source added second so its values take priority.
/// </summary>
internal sealed class OxSettingsConfigurationSource(string? filePath) : IConfigurationSource
{
    public string? FilePath { get; } = filePath;

    public IConfigurationProvider Build(IConfigurationBuilder builder) =>
        new OxSettingsConfigurationProvider(FilePath);
}
