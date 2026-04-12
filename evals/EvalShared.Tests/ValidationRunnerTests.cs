namespace EvalShared.Tests;

/// <summary>
/// Tests for <see cref="ValidationRunner"/>. Each test creates a temp directory,
/// sets up the workspace state, and evaluates the relevant rule type.
/// </summary>
public sealed class ValidationRunnerTests : IDisposable
{
    private readonly string _workspace = Path.Combine(
        Path.GetTempPath(), "eval-validation-tests", Guid.NewGuid().ToString("N"));

    public ValidationRunnerTests()
    {
        Directory.CreateDirectory(_workspace);
    }

    public void Dispose()
    {
        if (Directory.Exists(_workspace))
            Directory.Delete(_workspace, recursive: true);
    }

    [Fact]
    public async Task FileExistsRule_Passes_WhenFileExists()
    {
        await File.WriteAllTextAsync(Path.Combine(_workspace, "output.txt"), "content");

        var failures = await ValidationRunner.RunAsync(
            [new FileExistsRule { Path = "output.txt" }], _workspace);

        Assert.Empty(failures);
    }

    [Fact]
    public async Task FileExistsRule_Fails_WhenFileDoesNotExist()
    {
        var failures = await ValidationRunner.RunAsync(
            [new FileExistsRule { Path = "missing.txt" }], _workspace);

        var failure = Assert.Single(failures);
        Assert.Equal("file_exists", failure.RuleType);
        Assert.Contains("missing.txt", failure.Message);
    }

    [Fact]
    public async Task FileNotExistsRule_Passes_WhenFileAbsent()
    {
        var failures = await ValidationRunner.RunAsync(
            [new FileNotExistsRule { Path = "gone.txt" }], _workspace);

        Assert.Empty(failures);
    }

    [Fact]
    public async Task FileNotExistsRule_Fails_WhenFilePresent()
    {
        await File.WriteAllTextAsync(Path.Combine(_workspace, "still-here.txt"), "");

        var failures = await ValidationRunner.RunAsync(
            [new FileNotExistsRule { Path = "still-here.txt" }], _workspace);

        Assert.Single(failures);
    }

    [Fact]
    public async Task FileContainsRule_Passes_WhenContentPresent()
    {
        await File.WriteAllTextAsync(Path.Combine(_workspace, "data.txt"), "hello world foo");

        var failures = await ValidationRunner.RunAsync(
            [new FileContainsRule { Path = "data.txt", Content = "world" }], _workspace);

        Assert.Empty(failures);
    }

    [Fact]
    public async Task FileContainsRule_Fails_WhenContentAbsent()
    {
        await File.WriteAllTextAsync(Path.Combine(_workspace, "data.txt"), "not here");

        var failures = await ValidationRunner.RunAsync(
            [new FileContainsRule { Path = "data.txt", Content = "missing" }], _workspace);

        Assert.Single(failures);
    }

    [Fact]
    public async Task FileMatchesRule_Passes_WhenPatternMatches()
    {
        await File.WriteAllTextAsync(Path.Combine(_workspace, "data.txt"), "Found 42 items");

        var failures = await ValidationRunner.RunAsync(
            [new FileMatchesRule { Path = "data.txt", Pattern = @"\d+ items" }], _workspace);

        Assert.Empty(failures);
    }

    [Fact]
    public async Task FileMatchesRule_Fails_WhenPatternDoesNotMatch()
    {
        await File.WriteAllTextAsync(Path.Combine(_workspace, "data.txt"), "no numbers");

        var failures = await ValidationRunner.RunAsync(
            [new FileMatchesRule { Path = "data.txt", Pattern = @"\d+" }], _workspace);

        Assert.Single(failures);
    }

    [Fact]
    public async Task CommandSucceedsRule_Passes_WhenExitZero()
    {
        var failures = await ValidationRunner.RunAsync(
            [new CommandSucceedsRule { Command = "echo ok" }], _workspace);

        Assert.Empty(failures);
    }

    [Fact]
    public async Task CommandSucceedsRule_Fails_WhenExitNonZero()
    {
        var failures = await ValidationRunner.RunAsync(
            [new CommandSucceedsRule { Command = "exit 1" }], _workspace);

        Assert.Single(failures);
    }

    [Fact]
    public async Task CommandOutputContainsRule_Passes_WhenOutputMatches()
    {
        var failures = await ValidationRunner.RunAsync(
            [new CommandOutputContainsRule { Command = "echo hello world", Output = "hello" }], _workspace);

        Assert.Empty(failures);
    }

    [Fact]
    public async Task CommandOutputContainsRule_Fails_WhenOutputDoesNotMatch()
    {
        var failures = await ValidationRunner.RunAsync(
            [new CommandOutputContainsRule { Command = "echo foo", Output = "bar" }], _workspace);

        Assert.Single(failures);
    }

    [Fact]
    public async Task MultipleRules_ReportsAllFailures()
    {
        var rules = new List<ValidationRule>
        {
            new FileExistsRule { Path = "missing1.txt" },
            new FileExistsRule { Path = "missing2.txt" },
            new CommandSucceedsRule { Command = "echo ok" }, // passes
        };

        var failures = await ValidationRunner.RunAsync(rules, _workspace);

        Assert.Equal(2, failures.Count);
    }

    [Fact]
    public async Task FileExistsRule_PathTraversal_Rejected()
    {
        var failures = await ValidationRunner.RunAsync(
            [new FileExistsRule { Path = "../../etc/passwd" }], _workspace);

        var failure = Assert.Single(failures);
        Assert.Contains("Path traversal", failure.Message);
    }

    [Fact]
    public async Task FileContainsRule_PathTraversal_Rejected()
    {
        var failures = await ValidationRunner.RunAsync(
            [new FileContainsRule { Path = "../../../etc/shadow", Content = "root" }], _workspace);

        var failure = Assert.Single(failures);
        Assert.Contains("Path traversal", failure.Message);
    }
}
