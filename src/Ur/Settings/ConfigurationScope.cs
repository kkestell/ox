namespace Ur.Settings;

/// <summary>
/// Determines where a setting is persisted. User-level settings live in
/// ~/.ox/settings.json and apply everywhere; workspace-level settings live in
/// $WORKSPACE/.ox/settings.json and override user settings for that workspace.
/// </summary>
public enum ConfigurationScope
{
    User,
    Workspace
}
