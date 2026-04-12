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
    ///
    /// When <paramref name="streamOutput"/> is true, each line of the container's stderr
    /// is immediately written to the host's stderr (prefixed with a scenario × model tag)
    /// so the developer can watch agent events in real time without waiting for the container
    /// to exit. When false (the default), stderr is buffered silently — the existing behavior.
    /// Stdout is always buffered; it is not shown to the user.
    /// </summary>
    public static async Task<ContainerRunResult> RunAsync(
        ScenarioDefinition scenario,
        string model,
        string workspacePath,
        string providersJsonPath,
        CancellationToken ct,
        bool streamOutput = false)
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

        // Resolve to absolute paths now — podman is invoked with WorkingDirectory set to
        // the temp workspace, so relative paths would resolve against that directory instead
        // of the caller's cwd. :Z relabels the volume for SELinux (private label per
        // container); without it, SELinux denies access even when Unix permissions allow it.
        var absWorkspace = Path.GetFullPath(workspacePath);
        var absProviders = Path.GetFullPath(providersJsonPath);

        // Mount workspace read-write so Ox can write session files and modify the repo.
        psi.ArgumentList.Add("-v");
        psi.ArgumentList.Add($"{absWorkspace}:{ContainerWorkspace}:rw,Z");

        // Mount providers.json read-only into the default user data directory so Ox
        // finds it without any CLI override. The directory is pre-created in the image.
        psi.ArgumentList.Add("-v");
        psi.ArgumentList.Add($"{absProviders}:/root/.ur/providers.json:ro,Z");

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

        // Single prompt: headless eval mode has exactly one user message. The
        // agent works autonomously from this point — no further user messages.
        psi.ArgumentList.Add("--prompt");
        psi.ArgumentList.Add(scenario.Prompt);

        // Optional iteration cap: forwarded straight to AgentLoop so the agent
        // can't spin in a tool-call cycle indefinitely.
        if (scenario.MaxIterations.HasValue)
        {
            psi.ArgumentList.Add("--max-iterations");
            psi.ArgumentList.Add(scenario.MaxIterations.Value.ToString(System.Globalization.CultureInfo.InvariantCulture));
        }

        using var process = new Process { StartInfo = psi };
        process.Start();

        var stdoutTask = process.StandardOutput.ReadToEndAsync(ct);

        string stderr;
        if (streamOutput)
        {
            // Print the scenario × model header once, then stream raw lines.
            // The header gives attribution without repeating it on each event.
            var tag = $"[{scenario.Name} × {model}]";
            Console.Error.WriteLine(tag);
            var stderrBuilder = new System.Text.StringBuilder();

            // WaitForExitAsync does not guarantee that all ErrorDataReceived events
            // have fired when it returns (unlike the synchronous WaitForExit which does).
            // We use a TCS to detect the EOF sentinel (null data) that .NET fires after
            // the pipe is closed to signal the async read is truly complete.
            var stderrEof = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);

            process.ErrorDataReceived += (_, e) =>
            {
                if (e.Data is null)
                {
                    stderrEof.TrySetResult();
                    return;
                }
                stderrBuilder.AppendLine(e.Data);
                // Skip awaiting-approval lines — they are noise during streaming.
                if (!e.Data.Contains("[awaiting-approval]"))
                    Console.Error.WriteLine(e.Data);
            };

            process.BeginErrorReadLine();
            await process.WaitForExitAsync(ct);
            await stderrEof.Task;
            stderr = stderrBuilder.ToString();
        }
        else
        {
            // Buffered path (existing behavior): collect stderr without printing.
            var stderrTask = process.StandardError.ReadToEndAsync(ct);
            await process.WaitForExitAsync(ct);
            stderr = await stderrTask;
        }

        var stdout = await stdoutTask;

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
