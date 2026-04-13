namespace EvalShared;

/// <summary>
/// A declarative eval scenario loaded from YAML. Describes a single-prompt task
/// for the Ox agent: the repo to clone (or files to create), the prompt to send,
/// and the validation rules that determine pass/fail.
///
/// Two workspace modes are supported:
///   - <see cref="Repository"/> — clones a real repo at a pinned commit. The workspace
///     is exactly the repo state at that commit (broken tests and all).
///   - <see cref="WorkspaceFiles"/> — writes synthetic files directly. Used for
///     simple/unit eval scenarios that don't need a full repo.
///
/// These are mutually exclusive. <see cref="RepositoryRef.FixCommit"/> is reference-only metadata
/// recording the ground-truth fix for human review — it's never used at runtime.
/// </summary>
public sealed class ScenarioDefinition
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public string? Category { get; init; }
    public required List<string> Models { get; init; }
    public required string Prompt { get; init; }
    public RepositoryRef? Repository { get; init; }
    public List<WorkspaceFile>? WorkspaceFiles { get; init; }
    public List<string>? SetupCommands { get; init; }
    public required List<ValidationRule> ValidationRules { get; init; }
    public int TimeoutSeconds { get; init; } = 120;

    /// <summary>
    /// Optional cap on how many AgentLoop iterations (LLM calls) the agent may make
    /// within the single headless turn before being aborted. Null means no cap.
    ///
    /// Use this alongside <see cref="TimeoutSeconds"/>: the timeout catches slow wall-
    /// clock runs, while the iteration cap catches agents stuck in a tool-call cycle.
    /// </summary>
    public int? MaxIterations { get; init; }
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
