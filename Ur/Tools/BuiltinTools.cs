using System.Text.Json;
using Ur.AgentLoop;
using Ur.Permissions;

namespace Ur.Tools;

/// <summary>
/// Registers the built-in file tools into the tool registry.
/// These tools are always available — no extension enablement required.
/// Called during host startup before extensions load so that extension
/// tools cannot shadow built-in names.
/// </summary>
internal static class BuiltinTools
{
    public static void RegisterAll(ToolRegistry registry, Workspace workspace)
    {
        // Skip tools that are already registered — this lets tests inject fakes
        // before host startup without having them overwritten.

        // read_file is auto-allowed (ReadInWorkspace doesn't prompt).
        if (registry.Get("read_file") is null)
        {
            registry.Register(
                new ReadFileTool(workspace),
                OperationType.ReadInWorkspace,
                targetExtractor: args => ExtractFilePath(args));
        }

        // write_file and update_file both require write permission.
        if (registry.Get("write_file") is null)
        {
            registry.Register(
                new WriteFileTool(workspace),
                OperationType.WriteInWorkspace,
                targetExtractor: args => ExtractFilePath(args));
        }

        if (registry.Get("update_file") is null)
        {
            registry.Register(
                new UpdateFileTool(workspace),
                OperationType.WriteInWorkspace,
                targetExtractor: args => ExtractFilePath(args));
        }
    }

    /// <summary>
    /// Pulls the file_path argument out of the tool arguments so the permission
    /// prompt can show the user which file is being accessed. Handles both
    /// native strings (from tests) and JsonElement (from real LLM responses).
    /// </summary>
    private static string ExtractFilePath(IDictionary<string, object?> args)
    {
        if (!args.TryGetValue("file_path", out var v) || v is null)
            return "(unknown)";

        return v switch
        {
            string s => s,
            JsonElement { ValueKind: JsonValueKind.String } je => je.GetString() ?? "(unknown)",
            _ => v.ToString() ?? "(unknown)"
        };
    }
}
