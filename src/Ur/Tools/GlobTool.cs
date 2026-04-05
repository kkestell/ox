using System.Text.Json;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.FileSystemGlobbing;
using Microsoft.Extensions.FileSystemGlobbing.Abstractions;

namespace Ur.Tools;

/// <summary>
/// Built-in tool that finds files by glob pattern within the workspace.
/// Uses Microsoft.Extensions.FileSystemGlobbing for proper ** support,
/// returning matching paths relative to the workspace root.
/// </summary>
internal sealed class GlobTool : AIFunction
{
    private const int MaxResults = 1000;

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

    private readonly Workspace _workspace;

    public GlobTool(Workspace workspace)
    {
        _workspace = workspace;
    }

    public override string Name => "glob";
    public override string Description => "Find files by glob pattern within the workspace.";
    public override JsonElement JsonSchema => Schema;

    protected override ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var pattern = ToolArgHelpers.GetRequiredString(arguments, "pattern");
        var subPath = ToolArgHelpers.GetOptionalString(arguments, "path");

        var searchRoot = ToolArgHelpers.ResolvePath(_workspace.RootPath, subPath);

        if (!_workspace.Contains(searchRoot))
            throw new InvalidOperationException($"Path is outside the workspace: {subPath}");

        if (!Directory.Exists(searchRoot))
            return new ValueTask<object?>("");

        var matcher = new Matcher();
        matcher.AddInclude(pattern);

        var directoryInfo = new DirectoryInfoWrapper(new DirectoryInfo(searchRoot));
        var matchResult = matcher.Execute(directoryInfo);

        // Convert matches to workspace-relative paths for consistency.
        var relativePaths = matchResult.Files
            .Select(m => Path.GetRelativePath(
                _workspace.RootPath,
                Path.Combine(searchRoot, m.Path)))
            .OrderBy(p => p, StringComparer.Ordinal)
            .ToList();

        if (relativePaths.Count == 0)
            return new ValueTask<object?>("");

        var truncated = relativePaths.Count > MaxResults;
        var output = string.Join('\n', truncated ? relativePaths.Take(MaxResults) : relativePaths);

        if (truncated)
            output += $"\n[truncated: showing {MaxResults} of {relativePaths.Count} matches]";

        return new ValueTask<object?>(output);
    }

}
