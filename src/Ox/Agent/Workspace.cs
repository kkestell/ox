namespace Ox.Agent;

/// <summary>
/// A directory on disk that scopes sessions, configuration, and skills.
/// </summary>
internal sealed class Workspace(string rootPath)
{
    // RootPath normalizes the input: GetFullPath resolves . and relative segments.
    public string RootPath { get; } = Path.GetFullPath(rootPath);
    private string OxDirectory => Path.Combine(RootPath, ".ox");
    public string SessionsDirectory => Path.Combine(OxDirectory, "sessions");
    public string SkillsDirectory => Path.Combine(OxDirectory, "skills");
    public string SettingsPath => Path.Combine(OxDirectory, "settings.json");
    public string PermissionsPath => Path.Combine(OxDirectory, "permissions.jsonl");

    /// <summary>
    /// Ensures the .ox directory structure exists.
    /// </summary>
    public void EnsureDirectories()
    {
        Directory.CreateDirectory(SessionsDirectory);
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
