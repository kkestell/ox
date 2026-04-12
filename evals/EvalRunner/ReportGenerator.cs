using System.Globalization;
using System.Text;

namespace EvalRunner;

/// <summary>
/// Generates a Markdown report from recent eval run data. Groups results by
/// scenario × model and computes aggregate statistics: pass rate, average turns,
/// average tokens (in/out), average tool error rate, and average duration.
/// </summary>
public static class ReportGenerator
{
    /// <summary>
    /// Writes a Markdown report to the specified output path. Reads recent runs
    /// from the result store and formats them as a table with one row per
    /// scenario × model combination.
    /// </summary>
    public static async Task WriteReportAsync(ResultStore store, string outputPath, int lookbackDays = 30)
    {
        var runs = await store.LoadRecentAsync(lookbackDays);

        if (runs.Count == 0)
        {
            await File.WriteAllTextAsync(outputPath, "# Eval Report\n\nNo runs found.\n");
            return;
        }

        // Group by scenario × model to compute aggregates.
        var groups = runs
            .GroupBy(r => (r.ScenarioName, r.Model))
            .OrderBy(g => g.Key.ScenarioName)
            .ThenBy(g => g.Key.Model)
            .ToList();

        var sb = new StringBuilder();
        sb.AppendLine("# Eval Report");
        sb.AppendLine();
        sb.AppendLine(CultureInfo.InvariantCulture, $"Generated: {DateTimeOffset.UtcNow:yyyy-MM-dd HH:mm:ss} UTC");
        sb.AppendLine(CultureInfo.InvariantCulture, $"Runs analyzed: {runs.Count} (last {lookbackDays} days)");
        sb.AppendLine();

        // Summary table
        sb.AppendLine("## Results");
        sb.AppendLine();
        sb.AppendLine("| Scenario | Model | Pass Rate | Avg Turns | Avg Input Tokens | Avg Output Tokens | Avg Tool Error Rate | Avg Duration |");
        sb.AppendLine("|----------|-------|-----------|-----------|-----------------|-------------------|--------------------:|-------------:|");

        foreach (var group in groups)
        {
            var items = group.ToList();
            var passRate = items.Count(r => r.Passed) / (double)items.Count;
            var avgTurns = items.Average(r => r.Turns);
            var avgInputTokens = items.Average(r => r.InputTokens);
            var avgOutputTokens = items.Average(r => r.OutputTokens);
            var avgToolErrorRate = items.Average(r => r.ToolErrorRate);
            var avgDuration = items.Average(r => r.DurationSeconds);

            sb.AppendLine(CultureInfo.InvariantCulture,
                $"| {group.Key.ScenarioName} " +
                $"| {group.Key.Model} " +
                $"| {passRate:P0} ({items.Count(r => r.Passed)}/{items.Count}) " +
                $"| {avgTurns:F1} " +
                $"| {avgInputTokens:F0} " +
                $"| {avgOutputTokens:F0} " +
                $"| {avgToolErrorRate:P1} " +
                $"| {avgDuration:F1}s |");
        }

        sb.AppendLine();

        // Per-model summary
        var modelGroups = runs
            .GroupBy(r => r.Model)
            .OrderBy(g => g.Key)
            .ToList();

        sb.AppendLine("## Per-Model Summary");
        sb.AppendLine();
        sb.AppendLine("| Model | Pass Rate | Avg Duration | Avg Tokens (In/Out) |");
        sb.AppendLine("|-------|-----------|-------------:|--------------------:|");

        foreach (var mg in modelGroups)
        {
            var items = mg.ToList();
            var passRate = items.Count(r => r.Passed) / (double)items.Count;
            var avgDuration = items.Average(r => r.DurationSeconds);
            var avgIn = items.Average(r => r.InputTokens);
            var avgOut = items.Average(r => r.OutputTokens);

            sb.AppendLine(CultureInfo.InvariantCulture,
                $"| {mg.Key} " +
                $"| {passRate:P0} ({items.Count(r => r.Passed)}/{items.Count}) " +
                $"| {avgDuration:F1}s " +
                $"| {avgIn:F0}/{avgOut:F0} |");
        }

        sb.AppendLine();

        // Failures section
        var failedRuns = runs.Where(r => !r.Passed).ToList();
        if (failedRuns.Count > 0)
        {
            sb.AppendLine("## Failures");
            sb.AppendLine();
            foreach (var run in failedRuns.Take(20)) // Cap at 20 to keep report readable
            {
                sb.AppendLine(CultureInfo.InvariantCulture, $"- **{run.ScenarioName}** ({run.Model}): {run.Error ?? "validation failed"}");
            }
            sb.AppendLine();
        }

        var dir = Path.GetDirectoryName(outputPath);
        if (dir is not null)
            Directory.CreateDirectory(dir);

        await File.WriteAllTextAsync(outputPath, sb.ToString());
    }
}
