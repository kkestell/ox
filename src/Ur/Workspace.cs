namespace Ur;

/// <summary>
/// A directory on disk that scopes sessions, configuration, and workspace extensions.
/// </summary>
internal sealed class Workspace(string rootPath)
{
    // RootPath normalizes the input: GetFullPath resolves . and relative segments.
    public string RootPath { get; } = Path.GetFullPath(rootPath);
    private string UrDirectory => Path.Combine(RootPath, ".ur");
    public string SessionsDirectory => Path.Combine(UrDirectory, "sessions");
    public string ExtensionsDirectory => Path.Combine(UrDirectory, "extensions");
    public string SkillsDirectory => Path.Combine(UrDirectory, "skills");
    public string SettingsPath => Path.Combine(UrDirectory, "settings.json");
    public string PermissionsPath => Path.Combine(UrDirectory, "permissions.jsonl");
    public string StateHash => Extensions.ExtensionOverrideStore.ComputeWorkspaceHash(RootPath);

    /// <summary>
    /// Ensures the .ur directory structure exists.
    /// </summary>
    public void EnsureDirectories()
    {
        Directory.CreateDirectory(SessionsDirectory);
        Directory.CreateDirectory(ExtensionsDirectory);
    }

    /// <summary>
    /// Returns true if the given path is within this workspace.
    /// </summary>
    public bool Contains(string path)
    {
        var fullPath = Path.GetFullPath(path);
        return fullPath.StartsWith(RootPath + Path.DirectorySeparatorChar, StringComparison.Ordinal)
            || fullPath == RootPath;
    }
}
