using System.Text.Json;
using Microsoft.Extensions.AI;

namespace Ur.Tools;

/// <summary>
/// Built-in tool that performs a single find-and-replace within a workspace file.
/// The old string must appear exactly once — zero or multiple matches are errors,
/// preventing ambiguous edits.
/// </summary>
internal sealed class UpdateFileTool(Workspace workspace) : AIFunction
{
    private static readonly JsonElement Schema = JsonDocument.Parse("""
        {
            "type": "object",
            "properties": {
                "file_path": {
                    "type": "string",
                    "description": "Absolute or workspace-relative path to the file to update."
                },
                "old_string": {
                    "type": "string",
                    "description": "The exact string to find. Must appear exactly once in the file."
                },
                "new_string": {
                    "type": "string",
                    "description": "The replacement string."
                }
            },
            "required": ["file_path", "old_string", "new_string"],
            "additionalProperties": false
        }
        """).RootElement.Clone();

    public override string Name => "update_file";
    public override string Description => "Find and replace a unique string in a workspace file.";
    public override JsonElement JsonSchema => Schema;

    protected override ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var filePath = ToolArgHelpers.GetRequiredString(arguments, "file_path");
        var oldString = ToolArgHelpers.GetRequiredString(arguments, "old_string");
        var newString = ToolArgHelpers.GetRequiredString(arguments, "new_string");
        var fullPath = ToolArgHelpers.ResolvePath(workspace.RootPath, filePath);

        if (!workspace.Contains(fullPath))
            throw new InvalidOperationException($"Path is outside the workspace: {filePath}");

        string content;
        try
        {
            content = File.ReadAllText(fullPath);
        }
        catch (FileNotFoundException)
        {
            throw new InvalidOperationException($"File not found: {filePath}");
        }

        // Count occurrences to enforce the uniqueness constraint.
        var count = CountOccurrences(content, oldString);

        switch (count)
        {
            case 0:
                throw new InvalidOperationException("old_string not found in file");
            case > 1:
                throw new InvalidOperationException($"old_string appears {count} times; must be unique");
        }

        var updated = content.Replace(oldString, newString, StringComparison.Ordinal);
        File.WriteAllText(fullPath, updated);

        return new ValueTask<object?>($"Updated {filePath}");
    }

    /// <summary>
    /// Counts non-overlapping occurrences of a substring. Uses ordinal comparison
    /// so the tool behaves predictably with whitespace and special characters.
    /// </summary>
    private static int CountOccurrences(string text, string search)
    {
        var count = 0;
        var index = 0;
        while ((index = text.IndexOf(search, index, StringComparison.Ordinal)) >= 0)
        {
            count++;
            index += search.Length;
        }
        return count;
    }

}
