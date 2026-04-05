using System.Diagnostics;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.FileSystemGlobbing;
using Microsoft.Extensions.FileSystemGlobbing.Abstractions;

namespace Ur.Tools;

/// <summary>
/// Built-in tool that searches file contents by regex pattern.
/// Prefers ripgrep (rg) for performance when available, falling back to
/// .NET Regex for portability. The performance difference matters for
/// content search across large repos; glob-style file filtering uses
/// the FileSystemGlobbing package in both paths.
/// </summary>
internal sealed class GrepTool(Workspace workspace) : AIFunction
{
    private const int RipgrepTimeoutSeconds = 30;

    private static readonly JsonElement Schema = JsonDocument.Parse("""
        {
            "type": "object",
            "properties": {
                "pattern": {
                    "type": "string",
                    "description": "Regex pattern to search for in file contents."
                },
                "path": {
                    "type": "string",
                    "description": "Subdirectory to scope the search. Defaults to workspace root."
                },
                "include": {
                    "type": "string",
                    "description": "Glob filter for files to search (e.g. *.cs, **/*.json)."
                },
                "context_lines": {
                    "type": "integer",
                    "description": "Number of context lines to show before and after each match. Default 0."
                }
            },
            "required": ["pattern"],
            "additionalProperties": false
        }
        """).RootElement.Clone();

    // Lazily detect whether ripgrep is available. null = not yet checked.
    private static bool? _ripgrepAvailable;

    public override string Name => "grep";
    public override string Description => "Search file contents by regex pattern. Uses ripgrep if available, otherwise falls back to .NET regex.";
    public override JsonElement JsonSchema => Schema;

    protected override async ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var pattern = ToolArgHelpers.GetRequiredString(arguments, "pattern");
        var subPath = ToolArgHelpers.GetOptionalString(arguments, "path");
        var include = ToolArgHelpers.GetOptionalString(arguments, "include");
        var contextLines = ToolArgHelpers.GetOptionalInt(arguments, "context_lines") ?? 0;

        var searchRoot = ToolArgHelpers.ResolvePath(workspace.RootPath, subPath);

        if (!workspace.Contains(searchRoot))
            throw new InvalidOperationException($"Path is outside the workspace: {subPath}");

        if (!Directory.Exists(searchRoot))
            return "";

        if (IsRipgrepAvailable())
            return await SearchWithRipgrep(pattern, searchRoot, include, contextLines, cancellationToken);

        return SearchWithDotNet(pattern, searchRoot, include, contextLines);
    }

    // ─── Ripgrep backend ──────────────────────────────────────────────

    private async Task<string> SearchWithRipgrep(
        string pattern, string searchRoot, string? include, int contextLines,
        CancellationToken cancellationToken)
    {
        var args = new List<string>
        {
            "--line-number",
            "--no-heading",
            "--color", "never"
        };

        if (contextLines > 0)
        {
            args.Add("--context");
            args.Add(contextLines.ToString());
        }

        if (!string.IsNullOrEmpty(include))
        {
            args.Add("--glob");
            args.Add(include);
        }

        args.Add("--");
        args.Add(pattern);
        args.Add(searchRoot);

        var psi = new ProcessStartInfo
        {
            FileName = "rg",
            WorkingDirectory = workspace.RootPath,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true
        };

        foreach (var arg in args)
            psi.ArgumentList.Add(arg);

        using var process = Process.Start(psi)!;

        // Read output with a timeout to avoid hanging on pathological searches.
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
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

        await process.WaitForExitAsync(cancellationToken);

        // Make paths workspace-relative for cleaner output. Strip the prefix
        // only at the start of each line to avoid corrupting match content
        // that happens to contain the search root path.
        var prefix = searchRoot + Path.DirectorySeparatorChar;
        output = string.Join('\n', output.Split('\n')
            .Select(line => line.StartsWith(prefix, StringComparison.Ordinal)
                ? line[prefix.Length..]
                : line));

        return ToolArgHelpers.TruncateOutput(output);
    }

    // ─── .NET regex fallback ──────────────────────────────────────────

    private string SearchWithDotNet(
        string pattern, string searchRoot, string? include, int contextLines)
    {
        var files = EnumerateFiles(searchRoot, include);
        var regex = new Regex(pattern, RegexOptions.Compiled, TimeSpan.FromSeconds(5));
        var sb = new StringBuilder();
        var lineCount = 0;

        foreach (var file in files)
        {
            if (sb.Length >= ToolArgHelpers.MaxOutputBytes || lineCount >= ToolArgHelpers.MaxOutputLines)
                break;

            string[] lines;
            try
            {
                lines = File.ReadAllLines(file);
            }
            catch
            {
                // Skip files that can't be read (binary, locked, etc.)
                continue;
            }

            var relativePath = Path.GetRelativePath(workspace.RootPath, file);

            for (var i = 0; i < lines.Length; i++)
            {
                if (sb.Length >= ToolArgHelpers.MaxOutputBytes || lineCount >= ToolArgHelpers.MaxOutputLines)
                    break;

                if (!regex.IsMatch(lines[i]))
                    continue;

                // Emit context lines before the match.
                var contextStart = Math.Max(0, i - contextLines);
                var contextEnd = Math.Min(lines.Length - 1, i + contextLines);

                for (var c = contextStart; c <= contextEnd; c++)
                {
                    if (lineCount >= ToolArgHelpers.MaxOutputLines)
                        break;

                    // Use : for the match line, - for context lines (same as rg).
                    var separator = c == i ? ":" : "-";
                    sb.AppendLine($"{relativePath}:{c + 1}{separator}{lines[c]}");
                    lineCount++;
                }
            }
        }

        var result = sb.ToString().TrimEnd();
        if (lineCount >= ToolArgHelpers.MaxOutputLines || sb.Length >= ToolArgHelpers.MaxOutputBytes)
            result += "\n[truncated]";

        return result;
    }

    /// <summary>
    /// Enumerate files in the search root, optionally filtered by a glob pattern.
    /// </summary>
    private static IEnumerable<string> EnumerateFiles(string searchRoot, string? include)
    {
        if (string.IsNullOrEmpty(include))
            return Directory.EnumerateFiles(searchRoot, "*", SearchOption.AllDirectories);

        var matcher = new Matcher();
        matcher.AddInclude(include);
        var directoryInfo = new DirectoryInfoWrapper(new DirectoryInfo(searchRoot));
        var result = matcher.Execute(directoryInfo);
        return result.Files.Select(m => Path.Combine(searchRoot, m.Path));
    }

    // ─── Shared helpers ───────────────────────────────────────────────

    private static bool IsRipgrepAvailable()
    {
        if (_ripgrepAvailable.HasValue)
            return _ripgrepAvailable.Value;

        try
        {
            var psi = new ProcessStartInfo
            {
                FileName = "rg",
                Arguments = "--version",
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true
            };
            using var process = Process.Start(psi);
            process?.WaitForExit(3000);
            _ripgrepAvailable = process?.ExitCode == 0;
        }
        catch
        {
            _ripgrepAvailable = false;
        }

        return _ripgrepAvailable.Value;
    }

    /// <summary>
    /// Allows tests to force a specific backend by overriding the ripgrep detection.
    /// </summary>
    internal static void SetRipgrepAvailable(bool? available)
    {
        _ripgrepAvailable = available;
    }
}
