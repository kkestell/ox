using System.Diagnostics;
using System.Text.Json;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.FileSystemGlobbing;
using Microsoft.Extensions.FileSystemGlobbing.Abstractions;

namespace Ur.Tools;

/// <summary>
/// Built-in tool that finds files by glob pattern within the workspace.
///
/// Prefers ripgrep (rg --files) for its native .gitignore support; falls back to
/// Microsoft.Extensions.FileSystemGlobbing when rg is unavailable, explicitly
/// excluding .git to avoid surfacing version-control internals.
/// </summary>
internal sealed class GlobTool(Workspace workspace) : AIFunction
{
    private const int MaxResults = 1000;
    private const int RipgrepTimeoutSeconds = 30;

    private static readonly JsonElement Schema = JsonDocument.Parse("""
        {
            "type": "object",
            "properties": {
                "pattern": {
                    "type": "string",
                    "description": "Glob pattern to match files against (e.g. **/*.cs, src/**/*.json)."
                },
                "path": {
                    "type": "string",
                    "description": "Subdirectory to scope the search. Defaults to workspace root."
                }
            },
            "required": ["pattern"],
            "additionalProperties": false
        }
        """).RootElement.Clone();

    public override string Name => "glob";
    public override string Description => "Find files by glob pattern within the workspace.";
    public override JsonElement JsonSchema => Schema;

    protected override async ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var pattern = ToolArgHelpers.GetRequiredString(arguments, "pattern");
        var subPath = ToolArgHelpers.GetOptionalString(arguments, "path");

        var searchRoot = ToolArgHelpers.ResolvePath(workspace.RootPath, subPath);

        if (!Directory.Exists(searchRoot))
            return "";

        if (ToolArgHelpers.IsRipgrepAvailable())
            return await SearchWithRipgrepAsync(pattern, searchRoot, cancellationToken);

        return SearchWithDotNet(pattern, searchRoot);
    }

    // ─── Ripgrep backend ──────────────────────────────────────────────

    private async Task<string> SearchWithRipgrepAsync(
        string pattern, string searchRoot, CancellationToken ct)
    {
        var psi = new ProcessStartInfo
        {
            FileName = "rg",
            WorkingDirectory = workspace.RootPath,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true
        };
        psi.ArgumentList.Add("--files");
        psi.ArgumentList.Add("--glob");
        psi.ArgumentList.Add(pattern);
        psi.ArgumentList.Add(searchRoot);

        using var process = Process.Start(psi)!;
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        cts.CancelAfter(TimeSpan.FromSeconds(RipgrepTimeoutSeconds));

        string output;
        try
        {
            output = await process.StandardOutput.ReadToEndAsync(cts.Token);
        }
        catch (OperationCanceledException)
        {
            process.Kill(entireProcessTree: true);
            return "[search timed out]";
        }

        await process.WaitForExitAsync(ct);

        // Convert absolute rg output paths to workspace-relative paths for consistency.
        var relativePaths = output.Split('\n')
            .Where(line => !string.IsNullOrEmpty(line))
            .Select(line => Path.GetRelativePath(workspace.RootPath, line))
            .OrderBy(p => p, StringComparer.Ordinal)
            .ToList();

        if (relativePaths.Count == 0)
            return "";

        var truncated = relativePaths.Count > MaxResults;
        var result = string.Join('\n', truncated ? relativePaths.Take(MaxResults) : relativePaths);

        if (truncated)
            result += $"\n[truncated: showing {MaxResults} of {relativePaths.Count} matches]";

        return result;
    }

    // ─── .NET fallback ────────────────────────────────────────────────

    private string SearchWithDotNet(string pattern, string searchRoot)
    {
        var matcher = new Matcher();
        matcher.AddInclude(pattern);
        // Exclude .git so version-control internals never appear in results.
        // Without rg we can't read .gitignore, but .git/ is the most common noise.
        matcher.AddExclude(".git/**");

        var directoryInfo = new DirectoryInfoWrapper(new DirectoryInfo(searchRoot));
        var matchResult = matcher.Execute(directoryInfo);

        // Convert matches to workspace-relative paths for consistency.
        var relativePaths = matchResult.Files
            .Select(m => Path.GetRelativePath(
                workspace.RootPath,
                Path.Combine(searchRoot, m.Path)))
            .OrderBy(p => p, StringComparer.Ordinal)
            .ToList();

        if (relativePaths.Count == 0)
            return "";

        var truncated = relativePaths.Count > MaxResults;
        var output = string.Join('\n', truncated ? relativePaths.Take(MaxResults) : relativePaths);

        if (truncated)
            output += $"\n[truncated: showing {MaxResults} of {relativePaths.Count} matches]";

        return output;
    }
}
