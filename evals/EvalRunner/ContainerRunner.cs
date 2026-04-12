using System.Diagnostics;
using System.Text.Json;
using EvalShared;

namespace EvalRunner;

/// <summary>
/// Orchestrates a single eval run inside a Podman container. Builds the podman run
/// command from the scenario definition, executes it, then extracts the session JSONL
/// and metrics JSON from the workspace volume. If the container crashes before writing
/// those files, returns a synthetic failure result.
/// </summary>
public static class ContainerRunner
{
    private const string ImageName = "ox-eval";
    private const string ContainerWorkspace = "/workspace";

    /// <summary>
    /// Runs a single scenario × model eval in a Podman container. Returns the eval
    /// result plus the raw session/metrics artifacts for storage.
    /// </summary>
    public static async Task<ContainerRunResult> RunAsync(
        ScenarioDefinition scenario,
        string model,
        string workspacePath,
        string providersJsonPath,
        CancellationToken ct)
    {
        var psi = new ProcessStartInfo
        {
            FileName = "podman",
            WorkingDirectory = workspacePath,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true,
        };

        // Build argument list. Using ArgumentList avoids shell escaping issues.
        psi.ArgumentList.Add("run");
        psi.ArgumentList.Add("--rm");

        // Timeout enforced by podman — kills the container if it exceeds the limit.
        psi.ArgumentList.Add("--timeout");
        psi.ArgumentList.Add(scenario.TimeoutSeconds.ToString(System.Globalization.CultureInfo.InvariantCulture));

        // Mount workspace read-write so Ox can write session files and modify the repo.
        psi.ArgumentList.Add("-v");
        psi.ArgumentList.Add($"{workspacePath}:{ContainerWorkspace}:rw");

        // Mount providers.json read-only so Ox can discover configured providers.
        psi.ArgumentList.Add("-v");
        psi.ArgumentList.Add($"{providersJsonPath}:/eval/providers.json:ro");

        // Pass all UR_API_KEY_* env vars into the container for EnvironmentKeyring.
        foreach (var key in Environment.GetEnvironmentVariables().Keys.Cast<string>())
        {
            if (key.StartsWith("UR_API_KEY_", StringComparison.OrdinalIgnoreCase))
            {
                psi.ArgumentList.Add("-e");
                psi.ArgumentList.Add(key);
            }
        }

        psi.ArgumentList.Add(ImageName);

        // Ox CLI args: headless mode with YOLO permissions and specific model.
        psi.ArgumentList.Add("--headless");
        psi.ArgumentList.Add("--yolo");
        psi.ArgumentList.Add("--model");
        psi.ArgumentList.Add(model);

        // Each turn is a separate --turn arg.
        foreach (var turn in scenario.Turns)
        {
            psi.ArgumentList.Add("--turn");
            psi.ArgumentList.Add(turn);
        }

        using var process = new Process { StartInfo = psi };
        process.Start();

        var stdoutTask = process.StandardOutput.ReadToEndAsync(ct);
        var stderrTask = process.StandardError.ReadToEndAsync(ct);

        await process.WaitForExitAsync(ct);

        var stdout = await stdoutTask;
        var stderr = await stderrTask;

        // Locate session artifacts in the workspace's .ur/sessions/ directory.
        var sessionsDir = Path.Combine(workspacePath, ".ur", "sessions");
        var (sessionJsonl, metricsJson) = FindSessionArtifacts(sessionsDir);

        // If the container crashed before writing metrics, produce a synthetic failure.
        if (metricsJson is null || sessionJsonl is null)
        {
            return new ContainerRunResult
            {
                Result = new EvalResult
                {
                    ScenarioName = scenario.Name,
                    Model = model,
                    Passed = false,
                    Turns = 0,
                    InputTokens = 0,
                    OutputTokens = 0,
                    ToolCallsTotal = 0,
                    ToolCallsErrored = 0,
                    DurationSeconds = 0,
                    ValidationFailures = [],
                    Error = $"Container exited {process.ExitCode} without writing metrics. Stderr: {stderr}",
                },
                SessionJsonl = sessionJsonl ?? "",
                MetricsJson = metricsJson ?? "",
                ContainerExitCode = process.ExitCode,
            };
        }

        // Parse the metrics JSON to populate the result fields.
        var metrics = ParseMetrics(metricsJson);

        // If the metrics file indicates a crash (Error is set), skip validation —
        // the workspace state may be partial and running validation would produce
        // misleading failures.
        if (metrics.Error is not null)
        {
            return new ContainerRunResult
            {
                Result = new EvalResult
                {
                    ScenarioName = scenario.Name,
                    Model = model,
                    Passed = false,
                    Turns = metrics.Turns,
                    InputTokens = metrics.InputTokens,
                    OutputTokens = metrics.OutputTokens,
                    ToolCallsTotal = metrics.ToolCallsTotal,
                    ToolCallsErrored = metrics.ToolCallsErrored,
                    DurationSeconds = metrics.DurationSeconds,
                    ValidationFailures = [],
                    Error = metrics.Error,
                },
                SessionJsonl = sessionJsonl,
                MetricsJson = metricsJson,
                ContainerExitCode = process.ExitCode,
            };
        }

        // Run validation rules against the workspace. Only reached when the agent
        // completed without a fatal error.
        var failures = await ValidationRunner.RunAsync(scenario.ValidationRules, workspacePath, ct);

        return new ContainerRunResult
        {
            Result = new EvalResult
            {
                ScenarioName = scenario.Name,
                Model = model,
                Passed = failures.Count == 0,
                Turns = metrics.Turns,
                InputTokens = metrics.InputTokens,
                OutputTokens = metrics.OutputTokens,
                ToolCallsTotal = metrics.ToolCallsTotal,
                ToolCallsErrored = metrics.ToolCallsErrored,
                DurationSeconds = metrics.DurationSeconds,
                ValidationFailures = failures,
                Error = null,
            },
            SessionJsonl = sessionJsonl,
            MetricsJson = metricsJson,
            ContainerExitCode = process.ExitCode,
        };
    }

    /// <summary>
    /// Finds the most recent session JSONL and metrics JSON in the sessions directory.
    /// Returns null for either if not found.
    /// </summary>
    private static (string? SessionJsonl, string? MetricsJson) FindSessionArtifacts(string sessionsDir)
    {
        if (!Directory.Exists(sessionsDir))
            return (null, null);

        // Expect exactly one session file per eval run.
        var jsonlFiles = Directory.GetFiles(sessionsDir, "*.jsonl");
        var metricsFiles = Directory.GetFiles(sessionsDir, "*.metrics.json");

        var sessionJsonl = jsonlFiles.Length > 0
            ? File.ReadAllText(jsonlFiles[^1]) // most recent if multiple
            : null;

        var metricsJson = metricsFiles.Length > 0
            ? File.ReadAllText(metricsFiles[^1])
            : null;

        return (sessionJsonl, metricsJson);
    }

    /// <summary>
    /// Parses the metrics JSON into a lightweight struct for field extraction.
    /// Uses the snake_case field names that SessionMetrics writes.
    /// </summary>
    private static MetricsSnapshot ParseMetrics(string json)
    {
        using var doc = JsonDocument.Parse(json);
        var root = doc.RootElement;

        return new MetricsSnapshot
        {
            Turns = root.GetProperty("turns").GetInt32(),
            InputTokens = root.GetProperty("input_tokens").GetInt64(),
            OutputTokens = root.GetProperty("output_tokens").GetInt64(),
            ToolCallsTotal = root.GetProperty("tool_calls_total").GetInt32(),
            ToolCallsErrored = root.GetProperty("tool_calls_errored").GetInt32(),
            DurationSeconds = root.GetProperty("duration_seconds").GetDouble(),
            Error = root.TryGetProperty("error", out var errorProp) && errorProp.ValueKind != JsonValueKind.Null
                ? errorProp.GetString()
                : null,
        };
    }

    private struct MetricsSnapshot
    {
        public int Turns;
        public long InputTokens;
        public long OutputTokens;
        public int ToolCallsTotal;
        public int ToolCallsErrored;
        public double DurationSeconds;
        public string? Error;
    }
}

/// <summary>
/// Bundles a container run's result with the raw artifacts for SQLite storage.
/// </summary>
public sealed record ContainerRunResult
{
    public required EvalResult Result { get; init; }
    public required string SessionJsonl { get; init; }
    public required string MetricsJson { get; init; }
    public required int ContainerExitCode { get; init; }
}
