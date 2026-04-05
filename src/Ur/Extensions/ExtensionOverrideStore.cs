using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

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

    public ExtensionOverrideStore(string rootDirectory, Workspace workspace)
    {
        ArgumentNullException.ThrowIfNull(rootDirectory);
        ArgumentNullException.ThrowIfNull(workspace);

        _rootDirectory = rootDirectory;
        _workspace = workspace;
    }

    private string GlobalOverridesPath =>
        Path.Combine(_rootDirectory, "extensions-state.json");

    private string WorkspaceOverridesPath =>
        Path.Combine(_rootDirectory, "workspaces", _workspace.StateHash, "extensions-state.json");

    public async Task<ExtensionOverrideSnapshot> LoadAsync(CancellationToken ct = default)
    {
        var global = await LoadOverridesAsync(
                GlobalOverridesPath,
                allowedTier: tier => tier is ExtensionTier.System or ExtensionTier.User,
                scopeName: "global",
                ct)
            .ConfigureAwait(false);
        var workspace = await LoadOverridesAsync(
                WorkspaceOverridesPath,
                allowedTier: tier => tier is ExtensionTier.Workspace,
                scopeName: "workspace",
                ct)
            .ConfigureAwait(false);

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

    private static async Task<Dictionary<ExtensionId, bool>> LoadOverridesAsync(
        string path,
        Func<ExtensionTier, bool> allowedTier,
        string scopeName,
        CancellationToken ct)
    {
        if (!File.Exists(path))
            return [];

        try
        {
            await using var stream = File.OpenRead(path);
            var file = await JsonSerializer.DeserializeAsync(stream, JsonContext.OverrideFile, ct)
                .ConfigureAwait(false);

            if (file is null)
                return [];

            if (file.Version != CurrentVersion)
            {
                await Console.Error.WriteLineAsync(
                    $"Extension overrides at '{path}' ignored: unsupported version {file.Version}.");
                return [];
            }

            var overrides = new Dictionary<ExtensionId, bool>();
            foreach (var (serializedId, enabled) in file.Extensions)
            {
                if (!ExtensionId.TryParse(serializedId, out var extensionId))
                {
                    await Console.Error.WriteLineAsync(
                        $"Extension overrides at '{path}' ignored entry '{serializedId}': invalid extension ID.");
                    continue;
                }

                if (!allowedTier(extensionId.Tier))
                {
                    await Console.Error.WriteLineAsync(
                        $"Extension overrides at '{path}' ignored entry '{serializedId}': invalid tier for {scopeName} overrides.");
                    continue;
                }

                overrides[extensionId] = enabled;
            }

            return overrides;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or JsonException)
        {
            await Console.Error.WriteLineAsync(
                $"Extension overrides at '{path}' ignored: {ex.Message}");
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
