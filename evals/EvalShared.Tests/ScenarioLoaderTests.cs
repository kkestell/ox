using EvalShared;

namespace EvalShared.Tests;

/// <summary>
/// Tests for <see cref="ScenarioLoader"/> YAML deserialization. Verifies that
/// the round-trip from YAML to <see cref="ScenarioDefinition"/> produces correct
/// typed objects for both repo-based and synthetic scenarios.
/// </summary>
public sealed class ScenarioLoaderTests
{
    [Fact]
    public void Load_RepositoryScenario_ParsesAllFields()
    {
        const string yaml = """
            name: click-semver-default
            complexity: medium
            models:
              - google/gemini-3.1-flash-lite-preview
              - zai-coding/glm-4.5-air
            repository:
              url: https://github.com/pallets/click
              commit: 04ef3a6f473deb2499721a8d11f92a7d2c0912f2
              fix_commit: 1458800409ed12076f18451889b0857db36aa522
            turns:
              - "Fix the semver.Version crash in help text."
              - "Run the tests to confirm."
            validation_rules:
              - type: command_succeeds
                command: pytest tests/test_options.py
            timeout_seconds: 300
            """;

        var scenario = ScenarioLoader.Load(yaml);

        Assert.Equal("click-semver-default", scenario.Name);
        Assert.Equal(ScenarioComplexity.Medium, scenario.Complexity);
        Assert.Equal(2, scenario.Models.Count);
        Assert.Equal("google/gemini-3.1-flash-lite-preview", scenario.Models[0]);
        Assert.Equal(2, scenario.Turns.Count);
        Assert.NotNull(scenario.Repository);
        Assert.Equal("https://github.com/pallets/click", scenario.Repository.Url);
        Assert.Equal("04ef3a6f473deb2499721a8d11f92a7d2c0912f2", scenario.Repository.Commit);
        Assert.Equal("1458800409ed12076f18451889b0857db36aa522", scenario.Repository.FixCommit);
        Assert.Null(scenario.WorkspaceFiles);
        Assert.Single(scenario.ValidationRules);
        var rule = Assert.IsType<CommandSucceedsRule>(scenario.ValidationRules[0]);
        Assert.Equal("pytest tests/test_options.py", rule.Command);
        Assert.Equal(300, scenario.TimeoutSeconds);
    }

    [Fact]
    public void Load_SyntheticScenario_ParsesWorkspaceFiles()
    {
        const string yaml = """
            name: create-file
            complexity: simple
            models:
              - fake/hello
            turns:
              - "Create a file called output.txt containing 'hello world'."
            workspace_files:
              - path: README.md
                content: "This is a test workspace."
            validation_rules:
              - type: file_exists
                path: output.txt
              - type: file_contains
                path: output.txt
                content: hello world
            """;

        var scenario = ScenarioLoader.Load(yaml);

        Assert.Equal("create-file", scenario.Name);
        Assert.Null(scenario.Repository);
        Assert.NotNull(scenario.WorkspaceFiles);
        Assert.Single(scenario.WorkspaceFiles);
        Assert.Equal("README.md", scenario.WorkspaceFiles[0].Path);
        Assert.Equal(2, scenario.ValidationRules.Count);
        Assert.IsType<FileExistsRule>(scenario.ValidationRules[0]);
        Assert.IsType<FileContainsRule>(scenario.ValidationRules[1]);
        Assert.Equal(120, scenario.TimeoutSeconds); // default
    }

    [Fact]
    public void Load_AllValidationRuleTypes_ParsesCorrectly()
    {
        const string yaml = """
            name: all-rules
            complexity: simple
            models:
              - fake/hello
            turns:
              - "Do the thing."
            validation_rules:
              - type: file_exists
                path: output.txt
              - type: file_not_exists
                path: temp.txt
              - type: file_contains
                path: output.txt
                content: expected content
              - type: file_matches
                path: output.txt
                pattern: "\\d+ items"
              - type: command_succeeds
                command: echo ok
              - type: command_output_contains
                command: echo hello world
                output: hello
            """;

        var scenario = ScenarioLoader.Load(yaml);

        Assert.Equal(6, scenario.ValidationRules.Count);
        Assert.IsType<FileExistsRule>(scenario.ValidationRules[0]);
        Assert.IsType<FileNotExistsRule>(scenario.ValidationRules[1]);
        Assert.IsType<FileContainsRule>(scenario.ValidationRules[2]);
        Assert.IsType<FileMatchesRule>(scenario.ValidationRules[3]);
        Assert.IsType<CommandSucceedsRule>(scenario.ValidationRules[4]);
        Assert.IsType<CommandOutputContainsRule>(scenario.ValidationRules[5]);
    }

    [Fact]
    public void Load_ComplexityVariants_AllParse()
    {
        foreach (var (yamlValue, expected) in new[]
        {
            ("simple", ScenarioComplexity.Simple),
            ("medium", ScenarioComplexity.Medium),
            ("complex", ScenarioComplexity.Complex),
        })
        {
            var yaml = $"""
                name: test
                complexity: {yamlValue}
                models: [fake/hello]
                turns: ["do it"]
                validation_rules:
                  - type: command_succeeds
                    command: echo ok
                """;

            var scenario = ScenarioLoader.Load(yaml);
            Assert.Equal(expected, scenario.Complexity);
        }
    }

    [Fact]
    public void Load_MissingRequiredField_Throws()
    {
        // Missing 'name'
        const string yaml = """
            complexity: simple
            models: [fake/hello]
            turns: ["do it"]
            validation_rules:
              - type: command_succeeds
                command: echo ok
            """;

        Assert.Throws<InvalidOperationException>(() => ScenarioLoader.Load(yaml));
    }

    [Fact]
    public void Load_MaxTurnsField_ParsesValue()
    {
        const string yaml = """
            name: capped-scenario
            complexity: simple
            models:
              - fake/hello
            turns:
              - "First turn."
              - "Second turn."
              - "Third turn."
            max_turns: 2
            validation_rules:
              - type: command_succeeds
                command: echo ok
            """;

        var scenario = ScenarioLoader.Load(yaml);

        Assert.Equal(2, scenario.MaxTurns);
    }

    [Fact]
    public void Load_NoMaxTurnsField_DefaultsToNull()
    {
        const string yaml = """
            name: uncapped-scenario
            complexity: simple
            models:
              - fake/hello
            turns:
              - "Do the thing."
            validation_rules:
              - type: command_succeeds
                command: echo ok
            """;

        var scenario = ScenarioLoader.Load(yaml);

        Assert.Null(scenario.MaxTurns);
    }

    [Fact]
    public void Load_BothRepositoryAndWorkspaceFiles_Throws()
    {
        const string yaml = """
            name: conflict
            complexity: simple
            models: [fake/hello]
            turns: ["do it"]
            repository:
              url: https://github.com/example/repo
              commit: abc123
            workspace_files:
              - path: file.txt
                content: hello
            validation_rules:
              - type: command_succeeds
                command: echo ok
            """;

        Assert.Throws<InvalidOperationException>(() => ScenarioLoader.Load(yaml));
    }
}
