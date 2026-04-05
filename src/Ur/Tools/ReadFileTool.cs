using System.Text.Json;
using Microsoft.Extensions.AI;

namespace Ur.Tools;

/// <summary>
/// Built-in tool that reads a file from the workspace and returns its contents.
/// Supports optional offset/limit parameters for reading specific line ranges,
/// and appends a truncation message when the output doesn't cover the entire file.
/// </summary>
internal sealed class ReadFileTool(Workspace workspace) : AIFunction
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

    public override string Name => "read_file";
    public override string Description => "Read the contents of a file in the workspace.";
    public override JsonElement JsonSchema => Schema;

    protected override ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var filePath = ToolArgHelpers.GetRequiredString(arguments, "file_path");
        var fullPath = ToolArgHelpers.ResolvePath(workspace.RootPath, filePath);

        if (!workspace.Contains(fullPath))
            throw new InvalidOperationException($"Path is outside the workspace: {filePath}");

        if (!File.Exists(fullPath))
            throw new InvalidOperationException($"File not found: {filePath}");

        var start = ToolArgHelpers.GetOptionalInt(arguments, "offset") ?? 0;
        var limit = ToolArgHelpers.GetOptionalInt(arguments, "limit") ?? DefaultLimit;

        // Stream lines lazily so only the selected range is held in memory.
        // The full file is still read from disk to compute totalLines for the
        // truncation notice, but the GC pressure is much lower for large files.
        var selected = new List<string>();
        var totalLines = 0;

        foreach (var line in File.ReadLines(fullPath))
        {
            if (totalLines >= start && selected.Count < limit)
                selected.Add(line);
            totalLines++;
        }

        var result = string.Join('\n', selected);

        // Append a truncation notice when the returned window doesn't cover the whole file.
        if (start > 0 || start + selected.Count < totalLines)
            result += $"\n[truncated: showing lines {start + 1}-{start + selected.Count} of {totalLines} lines]";

        return new ValueTask<object?>(result);
    }

}
