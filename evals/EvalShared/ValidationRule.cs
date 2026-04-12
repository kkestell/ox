using System.Text.Json.Serialization;

namespace EvalShared;

/// <summary>
/// Base type for validation rules. Each subtype represents a specific assertion
/// that EvalRunner checks against the workspace after the agent finishes all turns.
///
/// The <see cref="Type"/> discriminator is serialized in YAML as the <c>type</c> field
/// and controls deserialization to the correct concrete subtype in
/// <see cref="ScenarioLoader"/>.
/// </summary>
[JsonDerivedType(typeof(FileExistsRule))]
[JsonDerivedType(typeof(FileNotExistsRule))]
[JsonDerivedType(typeof(FileContainsRule))]
[JsonDerivedType(typeof(FileMatchesRule))]
[JsonDerivedType(typeof(CommandSucceedsRule))]
[JsonDerivedType(typeof(CommandOutputContainsRule))]
public abstract class ValidationRule
{
    public abstract string Type { get; }
}

public sealed class FileExistsRule : ValidationRule
{
    public override string Type => "file_exists";
    public required string Path { get; init; }
}

public sealed class FileNotExistsRule : ValidationRule
{
    public override string Type => "file_not_exists";
    public required string Path { get; init; }
}

public sealed class FileContainsRule : ValidationRule
{
    public override string Type => "file_contains";
    public required string Path { get; init; }
    public required string Content { get; init; }
}

public sealed class FileMatchesRule : ValidationRule
{
    public override string Type => "file_matches";
    public required string Path { get; init; }
    public required string Pattern { get; init; }
}

public sealed class CommandSucceedsRule : ValidationRule
{
    public override string Type => "command_succeeds";
    public required string Command { get; init; }
}

public sealed class CommandOutputContainsRule : ValidationRule
{
    public override string Type => "command_output_contains";
    public required string Command { get; init; }
    public required string Output { get; init; }
}
