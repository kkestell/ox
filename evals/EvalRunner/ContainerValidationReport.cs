using System.Text;
using EvalShared;

namespace EvalRunner;

/// <summary>
/// Reads command-validation failures emitted by the eval container.
///
/// The on-disk format is a simple TSV with the rule type in column 1 and a
/// base64-encoded UTF-8 message in column 2. The format is intentionally
/// primitive so the container script can write it with only standard Unix tools.
/// </summary>
internal static class ContainerValidationReport
{
    internal static string GetHostReportPath(string workspacePath) =>
        Path.Combine(
            workspacePath,
            ContainerEvalScriptBuilder.ArtifactsDirectoryName,
            ContainerEvalScriptBuilder.ValidationReportFileName);

    internal static List<ValidationFailure> LoadFailures(string workspacePath)
    {
        var reportPath = GetHostReportPath(workspacePath);
        if (!File.Exists(reportPath))
            return [];

        var failures = new List<ValidationFailure>();
        foreach (var line in File.ReadLines(reportPath))
        {
            if (string.IsNullOrWhiteSpace(line))
                continue;

            var parts = line.Split('\t', 2);
            if (parts.Length != 2)
                throw new InvalidOperationException($"Malformed validation report line: {line}");

            var message = Encoding.UTF8.GetString(Convert.FromBase64String(parts[1]));
            failures.Add(new ValidationFailure(parts[0], message));
        }

        return failures;
    }
}
