using EvalShared;

namespace EvalRunner;

/// <summary>
/// CLI entry point for the eval runner. Loads scenario YAML files, expands them
/// into scenario × model pairs, drives each through a Podman container, validates
/// results, persists to SQLite, and optionally generates a Markdown report.
///
/// Usage:
///   dotnet run --project evals/EvalRunner -- [options]
///
/// Options:
///   --scenarios &lt;dir&gt;      Scenario YAML directory (default: evals/scenarios/)
///   --filter &lt;glob&gt;        Filter by scenario name (substring match)
///   --models &lt;m1,m2&gt;       Override model list for all scenarios
///   --providers &lt;path&gt;     Path to providers.json (default: providers.json)
///   --db &lt;path&gt;            SQLite database path (default: evals/results/evals.db)
///   --report               Write Markdown report after run (default: true)
///   --no-report            Skip Markdown report generation
///   --stream-output        Print container stderr to host terminal in real time
/// </summary>
public static class Program
{
    public static async Task<int> Main(string[] args)
    {
        var options = ParseOptions(args);

        if (!Directory.Exists(options.ScenariosDir))
        {
            Console.Error.WriteLine($"Scenarios directory not found: {options.ScenariosDir}");
            return 1;
        }

        if (!File.Exists(options.ProvidersJsonPath))
        {
            Console.Error.WriteLine($"providers.json not found: {options.ProvidersJsonPath}");
            return 1;
        }

        // Load and filter scenarios.
        var scenarioFiles = Directory.GetFiles(options.ScenariosDir, "*.yaml");
        if (scenarioFiles.Length == 0)
        {
            Console.Error.WriteLine($"No .yaml files found in {options.ScenariosDir}");
            return 1;
        }

        var scenarios = new List<ScenarioDefinition>();
        foreach (var file in scenarioFiles.OrderBy(f => f))
        {
            var yaml = await File.ReadAllTextAsync(file);
            var scenario = ScenarioLoader.Load(yaml);

            // Apply name filter (substring match).
            if (options.NameFilter is { } nf && !scenario.Name.Contains(nf, StringComparison.OrdinalIgnoreCase))
                continue;

            scenarios.Add(scenario);
        }

        if (scenarios.Count == 0)
        {
            Console.Error.WriteLine("No scenarios matched the filters.");
            return 1;
        }

        // Ensure the results directory exists.
        var dbDir = Path.GetDirectoryName(options.DbPath);
        if (dbDir is not null)
            Directory.CreateDirectory(dbDir);

        using var store = new ResultStore(options.DbPath);
        var cts = new CancellationTokenSource();

        Console.CancelKeyPress += (_, e) =>
        {
            e.Cancel = true;
            cts.Cancel();
        };

        var totalPairs = 0;
        var totalPassed = 0;
        var totalFailed = 0;

        // Expand scenario × model pairs and run sequentially. Sequential execution
        // avoids API rate limits and makes output easier to follow.
        foreach (var scenario in scenarios)
        {
var models = options.ModelOverrides is { } overrides
                    ? overrides.ToList()
                    : scenario.Models;

            foreach (var model in models)
            {
                if (cts.Token.IsCancellationRequested)
                    break;

                totalPairs++;
                Console.WriteLine($"[{totalPairs}] {scenario.Name} × {model}");

                string? workspacePath = null;
                try
                {
                    // Build workspace (clone repo or write synthetic files).
                    workspacePath = await WorkspaceBuilder.BuildAsync(scenario, cts.Token);

                    // Run in container.
                    var containerResult = await ContainerRunner.RunAsync(
                        scenario, model, workspacePath, options.ProvidersJsonPath, cts.Token,
                        streamOutput: options.StreamOutput);

                    // Persist result.
                    await store.SaveRunAsync(
                        containerResult.Result,
                        containerResult.SessionJsonl,
                        containerResult.MetricsJson);

                    if (containerResult.Result.Passed)
                    {
                        totalPassed++;
                        Console.WriteLine($"  PASS ({containerResult.Result.DurationSeconds:F1}s)");
                    }
                    else
                    {
                        totalFailed++;
                        if (containerResult.Result.Error is not null)
                        {
                            Console.WriteLine($"  FAIL: {containerResult.Result.Error}");
                        }
                        else
                        {
                            Console.WriteLine($"  FAIL: {containerResult.Result.ValidationFailures.Count} validation failure(s)");
                            foreach (var f in containerResult.Result.ValidationFailures)
                                Console.WriteLine($"    [{f.RuleType}] {f.Message}");
                        }
                    }
                }
                catch (OperationCanceledException)
                {
                    Console.Error.WriteLine("  Cancelled.");
                    break;
                }
                catch (Exception ex)
                {
                    totalFailed++;
                    Console.Error.WriteLine($"  ERROR: {ex.Message}");
                }
                finally
                {
                    // Clean up the workspace temp directory.
                    if (workspacePath is not null && Directory.Exists(workspacePath))
                    {
                        try { Directory.Delete(workspacePath, recursive: true); }
                        catch { /* best effort cleanup */ }
                    }
                }
            }
        }

        // Summary
        Console.WriteLine();
        Console.WriteLine($"Total: {totalPairs} | Passed: {totalPassed} | Failed: {totalFailed}");

        // Generate report.
        if (options.WriteReport)
        {
            var reportPath = Path.Combine(
                Path.GetDirectoryName(options.DbPath) ?? "evals/results",
                $"report-{DateTime.UtcNow:yyyy-MM-dd}.md");

            await ReportGenerator.WriteReportAsync(store, reportPath);
            Console.WriteLine($"Report written to {reportPath}");
        }

        return totalFailed > 0 ? 1 : 0;
    }

    private static RunnerOptions ParseOptions(string[] args)
    {
        var options = new RunnerOptions();

        for (var i = 0; i < args.Length; i++)
        {
            switch (args[i])
            {
                case "--scenarios" when i + 1 < args.Length:
                    options.ScenariosDir = args[++i];
                    break;
                case "--filter" when i + 1 < args.Length:
                    options.NameFilter = args[++i];
                    break;
                case "--models" when i + 1 < args.Length:
                    options.ModelOverrides = args[++i].Split(',', StringSplitOptions.RemoveEmptyEntries);
                    break;
                case "--providers" when i + 1 < args.Length:
                    options.ProvidersJsonPath = args[++i];
                    break;
                case "--db" when i + 1 < args.Length:
                    options.DbPath = args[++i];
                    break;
                case "--report":
                    options.WriteReport = true;
                    break;
                case "--no-report":
                    options.WriteReport = false;
                    break;
                case "--stream-output":
                    options.StreamOutput = true;
                    break;
            }
        }

        return options;
    }

    private sealed class RunnerOptions
    {
        public string ScenariosDir { get; set; } = "evals/scenarios/";
        public string? NameFilter { get; set; }
        public string[]? ModelOverrides { get; set; }
        public string ProvidersJsonPath { get; set; } = "providers.json";
        public string DbPath { get; set; } = "evals/results/evals.db";
        public bool WriteReport { get; set; } = true;
        // When true, ContainerRunner streams container stderr to the host terminal
        // in real time so the developer can watch agent events during eval runs.
        public bool StreamOutput { get; set; }
    }
}
