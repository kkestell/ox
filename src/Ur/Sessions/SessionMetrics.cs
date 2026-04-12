using System.Text.Json.Serialization;

namespace Ur.Sessions;

/// <summary>
/// Accumulated metrics for a completed session, written alongside the session JSONL
/// as {sessionId}.metrics.json. Both TUI and headless sessions produce this file
/// so metrics collection is generic — eval infrastructure reads it but doesn't
/// depend on how the session was driven.
///
/// All fields are required. <see cref="Error"/> is null on success. Tool error rate
/// is intentionally absent — consumers compute it as
/// <c>ToolCallsErrored / (double)ToolCallsTotal</c> to avoid dividing by zero
/// when no tools were called.
/// </summary>
internal sealed record SessionMetrics
{
    [JsonPropertyName("turns")]
    public required int Turns { get; init; }

    [JsonPropertyName("input_tokens")]
    public required long InputTokens { get; init; }

    [JsonPropertyName("output_tokens")]
    public required long OutputTokens { get; init; }

    [JsonPropertyName("tool_calls_total")]
    public required int ToolCallsTotal { get; init; }

    [JsonPropertyName("tool_calls_errored")]
    public required int ToolCallsErrored { get; init; }

    [JsonPropertyName("duration_seconds")]
    public required double DurationSeconds { get; init; }

    [JsonPropertyName("error")]
    public required string? Error { get; init; }
}
