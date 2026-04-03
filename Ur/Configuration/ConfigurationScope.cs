namespace Ur.Configuration;

/// <summary>
/// Determines where a setting is persisted. User-level settings live in
/// ~/.ur/settings.json and apply everywhere; workspace-level settings live in
/// <workspace>/.ur/settings.json and override user settings for that workspace.
/// </summary>
public enum ConfigurationScope
{
    User,
    Workspace,
}
