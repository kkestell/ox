using EvalShared;

namespace EvalRunner.Tests;

/// <summary>
/// Covers the harness-specific orchestration that the shared validation tests do
/// not see: how evals enter the container and where command-based validation is
/// expected to run.
/// </summary>
public sealed class ContainerEvalScriptBuilderTests
{
    [Fact]
    public void Build_FakeModel_UsesFakeProviderFlagInsteadOfModelOverride()
    {
        var scenario = new ScenarioDefinition
        {
            Name = "smoke",
            Models = ["fake/hello"],
            Prompt = "Say hello.",
            ValidationRules = [],
            TimeoutSeconds = 60,
        };

        var args = OxHeadlessArgsBuilder.Build("fake/hello", scenario);

        Assert.Contains("--fake-provider", args);
        Assert.DoesNotContain("--model", args);
        Assert.Contains("hello", args);
    }

    [Fact]
    public void Build_LiveModel_UsesModelOverride()
    {
        var scenario = new ScenarioDefinition
        {
            Name = "smoke",
            Models = ["google/gemini-3.1-flash-lite-preview"],
            Prompt = "Do the thing.",
            ValidationRules = [],
            TimeoutSeconds = 60,
            MaxIterations = 7,
        };

        var args = OxHeadlessArgsBuilder.Build("google/gemini-3.1-flash-lite-preview", scenario);

        Assert.Contains("--model", args);
        Assert.DoesNotContain("--fake-provider", args);
        Assert.Contains("google/gemini-3.1-flash-lite-preview", args);
        Assert.Contains("--max-iterations", args);
        Assert.Contains("7", args);
    }

    [Fact]
    public void Build_WithCommandValidation_EmbedsValidationInContainerScript()
    {
        var scenario = new ScenarioDefinition
        {
            Name = "validation",
            Models = ["fake/hello"],
            Prompt = "Say hello.",
            SetupCommands = ["python3 -m pip install --break-system-packages -e . pytest"],
            ValidationRules =
            [
                new CommandSucceedsRule { Command = "pytest --version" },
                new FileExistsRule { Path = "README.md" }
            ],
            TimeoutSeconds = 60,
        };

        var script = ContainerEvalScriptBuilder.Build(scenario);

        Assert.Contains("pytest --version", script, StringComparison.Ordinal);
        Assert.Contains("python3 -m pip install --break-system-packages -e . pytest", script, StringComparison.Ordinal);
        Assert.Contains(ContainerEvalScriptBuilder.ValidationReportFileName, script, StringComparison.Ordinal);
        Assert.Contains("Ox \"$@\"", script, StringComparison.Ordinal);
    }
}
