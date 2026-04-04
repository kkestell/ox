using System.Text.Json;
using Microsoft.Extensions.AI;

namespace Ur.Tools;

/// <summary>
/// Built-in tool that creates or overwrites a file in the workspace.
/// Automatically creates parent directories if they don't exist.
/// </summary>
internal sealed class WriteFileTool : AIFunction
{
    private static readonly JsonElement Schema = JsonDocument.Parse("""
        {
            "type": "object",
            "properties": {
                "file_path": {
                    "type": "string",
                    "description": "Absolute or workspace-relative path to the file to write."
                },
                "content": {
                    "type": "string",
                    "description": "The content to write to the file."
                }
            },
            "required": ["file_path", "content"],
            "additionalProperties": false
        }
        """).RootElement.Clone();

    private readonly Workspace _workspace;

    public WriteFileTool(Workspace workspace)
    {
        _workspace = workspace;
    }

    public override string Name => "write_file";
    public override string Description => "Create or overwrite a file in the workspace.";
    public override JsonElement JsonSchema => Schema;

    protected override ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var filePath = ToolArgHelpers.GetRequiredString(arguments, "file_path");
        var content = ToolArgHelpers.GetRequiredString(arguments, "content");
        var fullPath = ResolvePath(filePath);

        if (!_workspace.Contains(fullPath))
            throw new InvalidOperationException($"Path is outside the workspace: {filePath}");

        // Ensure the parent directory exists so the LLM can write to new subdirectories.
        var directory = Path.GetDirectoryName(fullPath);
        if (directory is not null)
            Directory.CreateDirectory(directory);

        File.WriteAllText(fullPath, content);

        var bytes = System.Text.Encoding.UTF8.GetByteCount(content);
        return new ValueTask<object?>($"Wrote {bytes} bytes to {filePath}");
    }

    private string ResolvePath(string filePath)
    {
        if (!Path.IsPathRooted(filePath))
            return Path.GetFullPath(Path.Combine(_workspace.RootPath, filePath));
        return Path.GetFullPath(filePath);
    }

}
