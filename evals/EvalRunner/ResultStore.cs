using System.Data;
using Dapper;
using EvalShared;
using Microsoft.Data.Sqlite;

namespace EvalRunner;

/// <summary>
/// SQLite persistence for eval run results. Creates the schema on first open and
/// provides insert + query methods. Each row captures the full metrics snapshot
/// plus the raw session JSONL and metrics JSON as text blobs for historical analysis.
/// </summary>
public sealed class ResultStore : IDisposable
{
    private readonly SqliteConnection _connection;

    public ResultStore(string dbPath)
    {
        _connection = new SqliteConnection($"Data Source={dbPath}");
        _connection.Open();
        InitializeSchema();
    }

    private void InitializeSchema()
    {
        _connection.Execute("""
            CREATE TABLE IF NOT EXISTS eval_runs (
                id                 INTEGER PRIMARY KEY AUTOINCREMENT,
                scenario_name      TEXT    NOT NULL,
                model              TEXT    NOT NULL,
                timestamp          TEXT    NOT NULL,
                passed             INTEGER NOT NULL,
                turns              INTEGER NOT NULL,
                input_tokens       INTEGER NOT NULL,
                output_tokens      INTEGER NOT NULL,
                tool_calls_total   INTEGER NOT NULL,
                tool_calls_errored INTEGER NOT NULL,
                tool_error_rate    REAL    NOT NULL,
                duration_seconds   REAL    NOT NULL,
                error              TEXT,
                session_jsonl      TEXT    NOT NULL,
                metrics_json       TEXT    NOT NULL
            );

            CREATE TABLE IF NOT EXISTS validation_failures (
                id        INTEGER PRIMARY KEY AUTOINCREMENT,
                run_id    INTEGER NOT NULL REFERENCES eval_runs(id),
                rule_type TEXT    NOT NULL,
                message   TEXT    NOT NULL
            );
            """);
    }

    /// <summary>
    /// Persists a completed eval run and its validation failures in a single transaction.
    /// tool_error_rate is computed here to avoid division-by-zero in callers.
    /// </summary>
    public async Task SaveRunAsync(EvalResult result, string sessionJsonl, string metricsJson)
    {
        using var transaction = await _connection.BeginTransactionAsync();

        var toolErrorRate = result.ToolCallsTotal > 0
            ? result.ToolCallsErrored / (double)result.ToolCallsTotal
            : 0.0;

        var runId = await _connection.QuerySingleAsync<long>("""
            INSERT INTO eval_runs (
                scenario_name, model, timestamp, passed, turns,
                input_tokens, output_tokens, tool_calls_total, tool_calls_errored,
                tool_error_rate, duration_seconds, error, session_jsonl, metrics_json
            ) VALUES (
                @ScenarioName, @Model, @Timestamp, @Passed, @Turns,
                @InputTokens, @OutputTokens, @ToolCallsTotal, @ToolCallsErrored,
                @ToolErrorRate, @DurationSeconds, @Error, @SessionJsonl, @MetricsJson
            );
            SELECT last_insert_rowid();
            """,
            new
            {
                result.ScenarioName,
                result.Model,
                Timestamp = DateTimeOffset.UtcNow.ToString("O"),
                Passed = result.Passed ? 1 : 0,
                result.Turns,
                result.InputTokens,
                result.OutputTokens,
                result.ToolCallsTotal,
                result.ToolCallsErrored,
                ToolErrorRate = toolErrorRate,
                result.DurationSeconds,
                result.Error,
                SessionJsonl = sessionJsonl,
                MetricsJson = metricsJson,
            },
            transaction);

        foreach (var failure in result.ValidationFailures)
        {
            await _connection.ExecuteAsync("""
                INSERT INTO validation_failures (run_id, rule_type, message)
                VALUES (@RunId, @RuleType, @Message);
                """,
                new { RunId = runId, failure.RuleType, failure.Message },
                transaction);
        }

        await transaction.CommitAsync();
    }

    /// <summary>
    /// Loads all eval runs from the last N days, most recent first.
    /// </summary>
    public async Task<IReadOnlyList<StoredRun>> LoadRecentAsync(int days)
    {
        var cutoff = DateTimeOffset.UtcNow.AddDays(-days).ToString("O");

        var runs = (await _connection.QueryAsync<StoredRun>("""
            SELECT id AS Id, scenario_name AS ScenarioName, model AS Model,
                   timestamp AS Timestamp, passed AS Passed, turns AS Turns,
                   input_tokens AS InputTokens, output_tokens AS OutputTokens,
                   tool_calls_total AS ToolCallsTotal, tool_calls_errored AS ToolCallsErrored,
                   tool_error_rate AS ToolErrorRate, duration_seconds AS DurationSeconds,
                   error AS Error
            FROM eval_runs
            WHERE timestamp >= @Cutoff
            ORDER BY timestamp DESC;
            """,
            new { Cutoff = cutoff })).ToList();

        return runs;
    }

    public void Dispose()
    {
        _connection.Dispose();
    }
}

/// <summary>
/// Read-only projection of an eval_runs row for reporting queries.
/// Does not include the large session_jsonl/metrics_json blobs.
/// </summary>
public sealed record StoredRun
{
    public long Id { get; init; }
    public required string ScenarioName { get; init; }
    public required string Model { get; init; }
    public required string Timestamp { get; init; }
    public bool Passed { get; init; }
    public int Turns { get; init; }
    public long InputTokens { get; init; }
    public long OutputTokens { get; init; }
    public int ToolCallsTotal { get; init; }
    public int ToolCallsErrored { get; init; }
    public double ToolErrorRate { get; init; }
    public double DurationSeconds { get; init; }
    public string? Error { get; init; }
}
