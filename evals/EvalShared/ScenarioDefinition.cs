namespace EvalShared;

/// <summary>
/// A declarative eval scenario loaded from YAML. Describes a multi-turn task
/// for the Ox agent: the repo to clone (or files to create), the turns to send,
/// and the validation rules that determine pass/fail.
///
/// Two workspace modes are supported:
///   - <see cref="Repository"/> — clones a real repo at a pinned commit. The workspace
///     is exactly the repo state at that commit (broken tests and all).
///   - <see cref="WorkspaceFiles"/> — writes synthetic files directly. Used for
///     simple/unit eval scenarios that don't need a full repo.
///
/// These are mutually exclusive. <see cref="FixCommit"/> is reference-only metadata
/// recording the ground-truth fix for human review — it's never used at runtime.
/// </summary>
public sealed class ScenarioDefinition
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public string? Category { get; init; }
    public required ScenarioComplexity Complexity { get; init; }
    public required List<string> Models { get; init; }
    public required List<string> Turns { get; init; }
    public RepositoryRef? Repository { get; init; }
    public List<WorkspaceFile>? WorkspaceFiles { get; init; }
    public required List<ValidationRule> ValidationRules { get; init; }
    public int TimeoutSeconds { get; init; } = 120;

    /// <summary>
    /// Optional cap on the number of turns (--turn args) the agent processes.
    /// Null means no cap — all turns defined in the scenario are sent.
    ///
    /// Use this as a safety rail alongside <see cref="TimeoutSeconds"/>: a time
    /// limit catches slow runs, while a turn limit catches agents that attempt
    /// more back-and-forth than the scenario expects.
    /// </summary>
    public int? MaxTurns { get; init; }
}

public enum ScenarioComplexity
{
    Simple,
    Medium,
    Complex,
}

/// <summary>
/// A Git repository to clone at a specific commit. The commit pins the workspace
/// state to a known-broken point so the eval is reproducible.
/// </summary>
public sealed class RepositoryRef
{
    public required string Url { get; init; }
    public required string Commit { get; init; }

    /// <summary>
    /// The commit that fixed the issue — recorded as metadata for human reference.
    /// Never used at runtime.
    /// </summary>
    public string? FixCommit { get; init; }
}

/// <summary>
/// A file to write into the workspace for synthetic (non-repo) scenarios.
/// </summary>
public sealed class WorkspaceFile
{
    public required string Path { get; init; }
    public required string Content { get; init; }
}
