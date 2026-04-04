using System.Text.Json;
using Microsoft.Extensions.AI;

namespace Ur.Tools;

/// <summary>
/// Built-in tool that reads a file from the workspace and returns its contents.
/// Supports optional offset/limit parameters for reading specific line ranges,
/// and appends a truncation message when the output doesn't cover the entire file.
/// </summary>
internal sealed class ReadFileTool : AIFunction
{
    private const int DefaultLimit = 2000;

    private static readonly JsonElement Schema = JsonDocument.Parse("""
        {
            "type": "object",
            "properties": {
                "file_path": {
                    "type": "string",
                    "description": "Absolute or workspace-relative path to the file to read."
                },
                "offset": {
                    "type": "integer",
                    "description": "Zero-based line number to start reading from. Defaults to 0."
                },
                "limit": {
                    "type": "integer",
                    "description": "Maximum number of lines to return. Defaults to 2000."
                }
            },
            "required": ["file_path"],
            "additionalProperties": false
        }
        """).RootElement.Clone();

    private readonly Workspace _workspace;

    public ReadFileTool(Workspace workspace)
    {
        _workspace = workspace;
    }

    public override string Name => "read_file";
    public override string Description => "Read the contents of a file in the workspace.";
    public override JsonElement JsonSchema => Schema;

    protected override ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var filePath = ToolArgHelpers.GetRequiredString(arguments, "file_path");
        var fullPath = ResolvePath(filePath);

        if (!_workspace.Contains(fullPath))
            throw new InvalidOperationException($"Path is outside the workspace: {filePath}");

        if (!File.Exists(fullPath))
            throw new InvalidOperationException($"File not found: {filePath}");

        var offset = ToolArgHelpers.GetOptionalInt(arguments, "offset") ?? 0;
        var limit = ToolArgHelpers.GetOptionalInt(arguments, "limit") ?? DefaultLimit;

        var allLines = File.ReadAllLines(fullPath);
        var totalLines = allLines.Length;

        // Clamp offset to the file length so we don't throw on out-of-range values.
        var start = Math.Min(offset, totalLines);
        var count = Math.Min(limit, totalLines - start);
        var selectedLines = allLines.AsSpan(start, count);

        var result = string.Join('\n', selectedLines.ToArray());

        // Append a truncation notice when the returned window doesn't cover the whole file.
        if (start > 0 || start + count < totalLines)
            result += $"\n[truncated: showing lines {start + 1}-{start + count} of {totalLines} lines]";

        return new ValueTask<object?>(result);
    }

    private string ResolvePath(string filePath)
    {
        // If the path is relative, resolve it against the workspace root.
        if (!Path.IsPathRooted(filePath))
            return Path.GetFullPath(Path.Combine(_workspace.RootPath, filePath));
        return Path.GetFullPath(filePath);
    }

}
