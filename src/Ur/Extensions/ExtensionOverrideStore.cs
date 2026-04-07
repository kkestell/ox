using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Microsoft.Extensions.Logging;

namespace Ur.Extensions;

internal sealed class ExtensionOverrideStore
{
    private const int CurrentVersion = 1;

    private static readonly ExtensionOverrideJsonContext JsonContext = new(new JsonSerializerOptions
    {
        WriteIndented = true
    });

    private readonly string _rootDirectory;
    private readonly Workspace _workspace;
    private readonly ILogger? _logger;

    public ExtensionOverrideStore(string rootDirectory, Workspace workspace, ILogger? logger = null)
    {
        ArgumentNullException.ThrowIfNull(rootDirectory);
        ArgumentNullException.ThrowIfNull(workspace);

        _rootDirectory = rootDirectory;
        _workspace = workspace;
        _logger = logger;
    }

    private string GlobalOverridesPath =>
        Path.Combine(_rootDirectory, "extensions-state.json");

    private string WorkspaceOverridesPath =>
        Path.Combine(_rootDirectory, "workspaces", _workspace.StateHash, "extensions-state.json");

    /// <summary>
    /// Loads persisted override state from both global and workspace files.
    /// Synchronous because this runs during startup where all I/O is local
    /// filesystem reads that complete immediately.
    /// </summary>
    public ExtensionOverrideSnapshot Load()
    {
        var global = LoadOverrides(
            GlobalOverridesPath,
            allowedTier: tier => tier is ExtensionTier.System or ExtensionTier.User,
            scopeName: "global",
            _logger);
        var workspace = LoadOverrides(
            WorkspaceOverridesPath,
            allowedTier: tier => tier is ExtensionTier.Workspace,
            scopeName: "workspace",
            _logger);

        return new ExtensionOverrideSnapshot(global, workspace);
    }

    public Task WriteGlobalAsync(
        IReadOnlyDictionary<ExtensionId, bool> overrides,
        CancellationToken ct = default) =>
        WriteAsync(
            GlobalOverridesPath,
            overrides,
            fileFactory: entries => new OverrideFile(CurrentVersion, null, entries),
            ct);

    public Task WriteWorkspaceAsync(
        IReadOnlyDictionary<ExtensionId, bool> overrides,
        CancellationToken ct = default) =>
        WriteAsync(
            WorkspaceOverridesPath,
            overrides,
            fileFactory: entries => new OverrideFile(CurrentVersion, _workspace.RootPath, entries),
            ct);

    private static Dictionary<ExtensionId, bool> LoadOverrides(
        string path,
        Func<ExtensionTier, bool> allowedTier,
        string scopeName,
        ILogger? logger)
    {
        try
        {
            // Return empty overrides silently if the file or its parent directory doesn't exist yet —
            // that's the normal case before any overrides have been configured.
            if (!File.Exists(path))
                return [];

            var json = File.ReadAllText(path);
            var file = JsonSerializer.Deserialize(json, JsonContext.OverrideFile);

            if (file is null)
                return [];

            if (file.Version != CurrentVersion)
            {
                logger?.LogWarning(
                    "Extension overrides at '{Path}' ignored: unsupported version {Version}",
                    path, file.Version);
                return [];
            }

            var overrides = new Dictionary<ExtensionId, bool>();
            foreach (var (serializedId, enabled) in file.Extensions)
            {
                if (!ExtensionId.TryParse(serializedId, out var extensionId))
                {
                    logger?.LogWarning(
                        "Extension overrides at '{Path}' ignored entry '{SerializedId}': invalid extension ID",
                        path, serializedId);
                    continue;
                }

                if (!allowedTier(extensionId.Tier))
                {
                    logger?.LogWarning(
                        "Extension overrides at '{Path}' ignored entry '{SerializedId}': invalid tier for {Scope} overrides",
                        path, serializedId, scopeName);
                    continue;
                }

                overrides[extensionId] = enabled;
            }

            return overrides;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or JsonException)
        {
            logger?.LogWarning("Extension overrides at '{Path}' ignored: {Error}",
                path, ex.Message);
            return [];
        }
    }

    private static async Task WriteAsync(
        string path,
        IReadOnlyDictionary<ExtensionId, bool> overrides,
        Func<Dictionary<string, bool>, OverrideFile> fileFactory,
        CancellationToken ct)
    {
        if (overrides.Count == 0)
        {
            if (File.Exists(path))
                File.Delete(path);

            return;
        }

        var serializedOverrides = overrides
            .OrderBy(pair => pair.Key.ToString(), StringComparer.Ordinal)
            .ToDictionary(pair => pair.Key.ToString(), pair => pair.Value, StringComparer.Ordinal);

        var directory = Path.GetDirectoryName(path)
            ?? throw new InvalidOperationException($"Override file path '{path}' has no directory.");
        Directory.CreateDirectory(directory);

        await using var stream = File.Create(path);
        await JsonSerializer.SerializeAsync(stream, fileFactory(serializedOverrides), JsonContext.OverrideFile, ct)
            .ConfigureAwait(false);
    }

    internal static string ComputeWorkspaceHash(string workspacePath)
    {
        ArgumentNullException.ThrowIfNull(workspacePath);

        var bytes = SHA256.HashData(Encoding.UTF8.GetBytes(Path.GetFullPath(workspacePath)));
        return Convert.ToHexStringLower(bytes);
    }

    internal sealed record ExtensionOverrideSnapshot(
        IReadOnlyDictionary<ExtensionId, bool> Global,
        IReadOnlyDictionary<ExtensionId, bool> Workspace);

    internal sealed record OverrideFile(
        [property: JsonPropertyName("version")] int Version,
        [property: JsonPropertyName("workspacePath")] string? WorkspacePath,
        [property: JsonPropertyName("extensions")] Dictionary<string, bool> Extensions);
}

/// <summary>
/// Source-generated JSON serialization context for AoT-safe extension override persistence.
/// </summary>
[JsonSerializable(typeof(ExtensionOverrideStore.OverrideFile))]
internal partial class ExtensionOverrideJsonContext : JsonSerializerContext;
