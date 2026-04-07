namespace Ur.Skills;

/// <summary>
/// Represents a single built-in slash command known to the Ur runtime.
///
/// Built-in commands take priority over skills in slash command resolution and
/// autocomplete. They are defined in code (not loaded from disk) and are always
/// available regardless of the workspace or user configuration.
/// </summary>
public sealed record BuiltInCommand(string Name, string Description);
