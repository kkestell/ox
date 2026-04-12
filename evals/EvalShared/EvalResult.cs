namespace EvalShared;

/// <summary>
/// The result of a single scenario × model evaluation. Combines session metrics
/// (token counts, tool calls, duration) with validation results (which rules passed
/// or failed). This is EvalRunner's view of a completed run — it maps directly to
/// the SQLite <c>eval_runs</c> table.
/// </summary>
public sealed record EvalResult
{
    public required string ScenarioName { get; init; }
    public required string Model { get; init; }
    public required bool Passed { get; init; }
    public required int Turns { get; init; }
    public required long InputTokens { get; init; }
    public required long OutputTokens { get; init; }
    public required int ToolCallsTotal { get; init; }
    public required int ToolCallsErrored { get; init; }
    public required double DurationSeconds { get; init; }
    public required List<ValidationFailure> ValidationFailures { get; init; }
    public string? Error { get; init; }
}
