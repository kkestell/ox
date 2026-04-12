namespace Ur.Configuration;

/// <summary>
/// Strongly-typed options for core Ur settings. File-based settings (Model,
/// TurnsToKeepToolResults) are bound from the "ur" section of IConfiguration
/// via <c>Configure&lt;UrOptions&gt;</c>. Runtime values (WorkspacePath,
/// UserDataDirectory, etc.) are set by the host via the <c>Action&lt;UrOptions&gt;</c>
/// callback in <c>AddUr()</c>.
/// </summary>
public sealed class UrOptions
{
    /// <summary>
    /// The user's selected model identifier (e.g. "openai/gpt-4o").
    /// Bound from the "ur:model" configuration key. Null when unset.
    /// </summary>
    public string? Model { get; set; }

    /// <summary>
    /// How many recent assistant turns' tool results are preserved verbatim
    /// during the <c>BuildLlmMessages</c> projection. Tool results in
    /// older turns are replaced with "[Tool result cleared]" to reclaim context
    /// window space without mutating the persisted message history.
    /// Bound from "ur:turnsToKeepToolResults". Default is 3.
    /// </summary>
    public int TurnsToKeepToolResults { get; set; } = 3;

    /// <summary>
    /// Root of the workspace directory. Determines where .ur/ state, sessions,
    /// and skills are stored. Set by the host at startup.
    /// </summary>
    public string WorkspacePath { get; set; } = "";

    /// <summary>
    /// User data directory (~/.ur/ by default). Contains skills, user settings,
    /// and permissions. Null means use platform default.
    /// </summary>
    public string? UserDataDirectory { get; set; }

    /// <summary>
    /// User settings file path. Null means use default within UserDataDirectory.
    /// </summary>
    public string? UserSettingsPath { get; set; }

    /// <summary>
    /// Ephemeral override for the selected model ID. When set, UrConfiguration
    /// returns this value instead of reading from persisted settings. Lets
    /// headless/test mode select a model at startup without rewriting settings.
    /// </summary>
    public string? SelectedModelOverride { get; set; }
}
