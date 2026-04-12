using System.Diagnostics;
using System.Text.RegularExpressions;

namespace EvalShared;

/// <summary>
/// Failure detail from a single validation rule evaluation.
/// </summary>
public sealed record ValidationFailure(string RuleType, string Message);

/// <summary>
/// Evaluates <see cref="ValidationRule"/>s against a workspace directory after the
/// agent finishes all turns. File rules use System.IO; command rules spawn
/// <c>bash -c "{command}"</c> with CWD set to the workspace.
///
/// Lives in EvalShared (no Ur dependency) so both EvalRunner and tests can use it.
/// </summary>
public static class ValidationRunner
{
    private static readonly TimeSpan CommandTimeout = TimeSpan.FromSeconds(15);

    public static async Task<List<ValidationFailure>> RunAsync(
        IReadOnlyList<ValidationRule> rules,
        string workspacePath,
        CancellationToken ct = default)
    {
        var failures = new List<ValidationFailure>();

        foreach (var rule in rules)
        {
            var failure = rule switch
            {
                FileExistsRule r => EvalFileExists(r, workspacePath),
                FileNotExistsRule r => EvalFileNotExists(r, workspacePath),
                FileContainsRule r => await EvalFileContainsAsync(r, workspacePath, ct),
                FileMatchesRule r => await EvalFileMatchesAsync(r, workspacePath, ct),
                CommandSucceedsRule r => await EvalCommandSucceedsAsync(r, workspacePath, ct),
                CommandOutputContainsRule r => await EvalCommandOutputContainsAsync(r, workspacePath, ct),
                _ => new ValidationFailure(rule.Type, $"Unknown rule type: {rule.Type}"),
            };

            if (failure is not null)
                failures.Add(failure);
        }

        return failures;
    }

    /// <summary>
    /// Resolves a relative path within the workspace and ensures it doesn't escape
    /// via traversal (e.g. "../../etc/passwd"). Returns null with a failure if the
    /// resolved path is outside the workspace.
    /// </summary>
    private static (string? ResolvedPath, ValidationFailure? Failure) ResolvePath(
        string ruleType, string relativePath, string workspace)
    {
        var resolved = Path.GetFullPath(Path.Combine(workspace, relativePath));
        var workspaceRoot = Path.GetFullPath(workspace);

        // Ensure the resolved path is within the workspace directory.
        if (!resolved.StartsWith(workspaceRoot, StringComparison.Ordinal))
            return (null, new ValidationFailure(ruleType, $"Path traversal not allowed: {relativePath}"));

        return (resolved, null);
    }

    private static ValidationFailure? EvalFileExists(FileExistsRule rule, string workspace)
    {
        var (path, traversalFailure) = ResolvePath(rule.Type, rule.Path, workspace);
        if (traversalFailure is not null)
            return traversalFailure;

        return File.Exists(path)
            ? null
            : new ValidationFailure(rule.Type, $"File does not exist: {rule.Path}");
    }

    private static ValidationFailure? EvalFileNotExists(FileNotExistsRule rule, string workspace)
    {
        var (path, traversalFailure) = ResolvePath(rule.Type, rule.Path, workspace);
        if (traversalFailure is not null)
            return traversalFailure;

        return !File.Exists(path)
            ? null
            : new ValidationFailure(rule.Type, $"File should not exist but does: {rule.Path}");
    }

    private static async Task<ValidationFailure?> EvalFileContainsAsync(
        FileContainsRule rule, string workspace, CancellationToken ct)
    {
        var (path, traversalFailure) = ResolvePath(rule.Type, rule.Path, workspace);
        if (traversalFailure is not null)
            return traversalFailure;

        if (!File.Exists(path))
            return new ValidationFailure(rule.Type, $"File does not exist: {rule.Path}");

        var content = await File.ReadAllTextAsync(path, ct);
        return content.Contains(rule.Content, StringComparison.Ordinal)
            ? null
            : new ValidationFailure(rule.Type, $"File '{rule.Path}' does not contain expected content");
    }

    private static async Task<ValidationFailure?> EvalFileMatchesAsync(
        FileMatchesRule rule, string workspace, CancellationToken ct)
    {
        var (path, traversalFailure) = ResolvePath(rule.Type, rule.Path, workspace);
        if (traversalFailure is not null)
            return traversalFailure;

        if (!File.Exists(path))
            return new ValidationFailure(rule.Type, $"File does not exist: {rule.Path}");

        var content = await File.ReadAllTextAsync(path, ct);
        return Regex.IsMatch(content, rule.Pattern)
            ? null
            : new ValidationFailure(rule.Type, $"File '{rule.Path}' does not match pattern: {rule.Pattern}");
    }

    private static async Task<ValidationFailure?> EvalCommandSucceedsAsync(
        CommandSucceedsRule rule, string workspace, CancellationToken ct)
    {
        var (exitCode, _, stderr) = await RunCommandAsync(rule.Command, workspace, ct);
        return exitCode == 0
            ? null
            : new ValidationFailure(rule.Type, $"Command failed (exit {exitCode}): {rule.Command}\n{stderr}");
    }

    private static async Task<ValidationFailure?> EvalCommandOutputContainsAsync(
        CommandOutputContainsRule rule, string workspace, CancellationToken ct)
    {
        var (exitCode, stdout, stderr) = await RunCommandAsync(rule.Command, workspace, ct);
        if (exitCode != 0)
            return new ValidationFailure(rule.Type, $"Command failed (exit {exitCode}): {rule.Command}\n{stderr}");

        return stdout.Contains(rule.Output, StringComparison.Ordinal)
            ? null
            : new ValidationFailure(rule.Type, $"Command output does not contain: {rule.Output}");
    }

    private static async Task<(int ExitCode, string Stdout, string Stderr)> RunCommandAsync(
        string command, string workingDirectory, CancellationToken ct)
    {
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        cts.CancelAfter(CommandTimeout);

        // Use ArgumentList instead of Arguments to avoid shell-escaping issues.
        // ArgumentList passes each element as a separate argv entry, so the command
        // string reaches bash exactly as-is without needing quote escaping.
        var psi = new ProcessStartInfo
        {
            FileName = "bash",
            WorkingDirectory = workingDirectory,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true,
        };
        psi.ArgumentList.Add("-c");
        psi.ArgumentList.Add(command);

        using var process = new Process { StartInfo = psi };
        process.Start();

        // Read stdout and stderr concurrently to avoid deadlocks when both
        // buffers fill up.
        var stdoutTask = process.StandardOutput.ReadToEndAsync(cts.Token);
        var stderrTask = process.StandardError.ReadToEndAsync(cts.Token);

        try
        {
            await process.WaitForExitAsync(cts.Token);
        }
        catch (OperationCanceledException)
        {
            process.Kill(entireProcessTree: true);
            return (-1, "", $"Command timed out after {CommandTimeout.TotalSeconds}s");
        }

        var stdout = await stdoutTask;
        var stderr = await stderrTask;
        return (process.ExitCode, stdout, stderr);
    }

}
