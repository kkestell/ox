using System.Diagnostics;
using EvalShared;

namespace EvalRunner;

/// <summary>
/// Prepares workspace directories for eval runs. For repository-based scenarios,
/// clones the repo at the exact pinned commit. For synthetic scenarios, writes the
/// declared workspace files directly. All workspaces are created under a temp
/// directory that the caller is responsible for cleaning up.
/// </summary>
public static class WorkspaceBuilder
{
    /// <summary>
    /// Creates a workspace directory for the given scenario. Returns the absolute
    /// path to the workspace root. The caller must delete this directory after use.
    /// </summary>
    public static async Task<string> BuildAsync(ScenarioDefinition scenario, CancellationToken ct)
    {
        var workspace = Path.Combine(Path.GetTempPath(), $"ox-eval-{Guid.NewGuid():N}");
        Directory.CreateDirectory(workspace);

        if (scenario.Repository is { } repo)
        {
            // Clone at the exact pinned commit so the workspace state is reproducible.
            // --no-checkout avoids writing files before we select the right commit.
            await RunGitAsync($"clone --no-checkout {repo.Url} .", workspace, ct);
            await RunGitAsync($"checkout {repo.Commit}", workspace, ct);
        }
        else if (scenario.WorkspaceFiles is { Count: > 0 } files)
        {
            // Synthetic workspace — write declared files directly.
            foreach (var file in files)
            {
                var fullPath = Path.Combine(workspace, file.Path);
                var dir = Path.GetDirectoryName(fullPath);
                if (dir is not null)
                    Directory.CreateDirectory(dir);

                await File.WriteAllTextAsync(fullPath, file.Content, ct);
            }
        }

        return workspace;
    }

    private static async Task RunGitAsync(string args, string workingDirectory, CancellationToken ct)
    {
        var psi = new ProcessStartInfo
        {
            FileName = "git",
            WorkingDirectory = workingDirectory,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true,
        };

        // Use ArgumentList for safe argument passing — no shell escaping needed.
        foreach (var arg in args.Split(' ', StringSplitOptions.RemoveEmptyEntries))
            psi.ArgumentList.Add(arg);

        using var process = new Process();
        process.StartInfo = psi;
        process.Start();

        var stderr = await process.StandardError.ReadToEndAsync(ct);
        await process.WaitForExitAsync(ct);

        if (process.ExitCode != 0)
            throw new InvalidOperationException($"git {args} failed (exit {process.ExitCode}): {stderr}");
    }
}
