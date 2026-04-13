using System.Text;
using EvalShared;

namespace EvalRunner.Tests;

/// <summary>
/// Ensures the host can decode command-validation failures emitted from the
/// container script without depending on Podman in unit tests.
/// </summary>
public sealed class ContainerValidationReportTests : IDisposable
{
    private readonly string _workspace = Path.Combine(
        Path.GetTempPath(),
        "eval-runner-tests",
        Guid.NewGuid().ToString("N"));

    public ContainerValidationReportTests()
    {
        Directory.CreateDirectory(Path.Combine(_workspace, ContainerEvalScriptBuilder.ArtifactsDirectoryName));
    }

    public void Dispose()
    {
        if (Directory.Exists(_workspace))
            Directory.Delete(_workspace, recursive: true);
    }

    [Fact]
    public void LoadFailures_DecodesBase64Messages()
    {
        var reportPath = ContainerValidationReport.GetHostReportPath(_workspace);
        var message = "Command failed (exit 1): pytest tests/test_slots.py\nboom";
        var encoded = Convert.ToBase64String(Encoding.UTF8.GetBytes(message));
        File.WriteAllText(reportPath, $"command_succeeds\t{encoded}\n");

        var failures = ContainerValidationReport.LoadFailures(_workspace);

        var failure = Assert.Single(failures);
        Assert.Equal("command_succeeds", failure.RuleType);
        Assert.Equal(message, failure.Message);
    }
}
