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

        // glob and grep are read operations — auto-allowed like read_file.
        if (registry.Get("glob") is null)
        {
            registry.Register(
                new GlobTool(workspace),
                OperationType.ReadInWorkspace,
                targetExtractor: args => ExtractStringArg(args, "pattern"));
        }

        if (registry.Get("grep") is null)
        {
            registry.Register(
                new GrepTool(workspace),
                OperationType.ReadInWorkspace,
                targetExtractor: args => ExtractStringArg(args, "pattern"));
        }

        // bash requires per-invocation approval — ExecuteCommand is Once-only.
        if (registry.Get("bash") is null)
        {
            registry.Register(
                new BashTool(workspace),
                OperationType.ExecuteCommand,
                targetExtractor: args => ExtractStringArg(args, "command"));
        }
    }

    /// <summary>
    /// Pulls the file_path argument out of the tool arguments so the permission
    /// prompt can show the user which file is being accessed. Handles both
    /// native strings (from tests) and JsonElement (from real LLM responses).
    /// </summary>
    private static string ExtractFilePath(IDictionary<string, object?> args) =>
        ExtractStringArg(args, "file_path");

    /// <summary>
    /// Generic extraction of a named string argument for permission target display.
    /// Handles both native strings (from tests) and JsonElement (from real LLM responses).
    /// </summary>
    private static string ExtractStringArg(IDictionary<string, object?> args, string key)
    {
        if (!args.TryGetValue(key, out var v) || v is null)
            return "(unknown)";

        return v switch
        {
            string s => s,
            JsonElement { ValueKind: JsonValueKind.String } je => je.GetString() ?? "(unknown)",
            _ => v.ToString() ?? "(unknown)"
        };
    }
}
