using System.Text.Json;
using System.Text.RegularExpressions;
using Lua;
using Lua.IO;
using Lua.Platforms;
using Lua.Standard;

namespace Ur.Extensions;

/// <summary>
/// Discovers extension directories, evaluates manifests, initializes Lua runtimes,
/// and wires up the <c>ur.tool.register</c> API.
/// </summary>
internal static partial class ExtensionLoader
{
    public static async Task<List<Extension>> DiscoverAllAsync(
        string? systemDir,
        string? userDir,
        string? workspaceDir,
        CancellationToken ct = default)
    {
        var seen = new Dictionary<string, ExtensionTier>(StringComparer.Ordinal);
        var extensions = new List<Extension>();

        // Process tiers in trust order: system > user > workspace.
        await DiscoverTierAsync(systemDir, ExtensionTier.System, seen, extensions, ct)
            .ConfigureAwait(false);
        await DiscoverTierAsync(userDir, ExtensionTier.User, seen, extensions, ct)
            .ConfigureAwait(false);
        await DiscoverTierAsync(workspaceDir, ExtensionTier.Workspace, seen, extensions, ct)
            .ConfigureAwait(false);

        return extensions
            .OrderBy(extension => TierSortOrder(extension.Tier))
            .ThenBy(extension => extension.Name, StringComparer.Ordinal)
            .ToList();
    }

    public static async Task ActivateAsync(
        Extension extension,
        CancellationToken ct = default)
    {
        // Reset to a clean slate before activation — clears any previous Lua
        // runtime and tools from a prior activation attempt.
        extension.ApplyState(ExtensionState.Inactive);

        var mainPath = Path.Combine(extension.Directory, "main.lua");
        if (!File.Exists(mainPath))
        {
            // Manifest-only extension — no Lua runtime, but still considered active.
            extension.ApplyState(ExtensionState.Active);
            return;
        }

        try
        {
            var state = CreateSandboxedState();
            InjectToolApi(state, extension);

            var script = await File.ReadAllTextAsync(mainPath, ct).ConfigureAwait(false);
            await state.DoStringAsync(script, mainPath, ct).ConfigureAwait(false);

            extension.ApplyState(ExtensionState.Active, lua: state);
        }
        catch (Exception ex) when (
            ex is LuaRuntimeException or LuaParseException or LuaCompileException or InvalidOperationException)
        {
            extension.ApplyState(ExtensionState.Failed, error: ex.Message);
            await Console.Error.WriteLineAsync(
                $"Extension '{extension.Name}': main.lua failed: {ex.Message}");
        }
    }

    public static void Deactivate(Extension extension) =>
        extension.ApplyState(ExtensionState.Inactive);

    private static async Task DiscoverTierAsync(
        string? directory,
        ExtensionTier tier,
        Dictionary<string, ExtensionTier> seen,
        List<Extension> extensions,
        CancellationToken ct)
    {
        if (directory is null || !Directory.Exists(directory))
            return;

        foreach (var extDir in Directory.EnumerateDirectories(directory))
        {
            var manifestPath = Path.Combine(extDir, "manifest.lua");
            if (!File.Exists(manifestPath))
                continue;

            try
            {
                var ext = await EvaluateManifestAsync(manifestPath, extDir, tier, ct)
                    .ConfigureAwait(false);

                // Higher-trust tier wins on name collision.
                if (seen.TryGetValue(ext.Name, out var existingTier))
                {
                    await Console.Error.WriteLineAsync(
                        $"Extension '{ext.Name}' from {tier} tier skipped: " +
                        $"already loaded from {existingTier} tier.");
                    continue;
                }

                seen[ext.Name] = tier;
                extensions.Add(ext);
            }
            catch (Exception ex) when (
                ex is LuaRuntimeException or LuaParseException or
                LuaCompileException or InvalidOperationException)
            {
                await Console.Error.WriteLineAsync(
                    $"Extension at '{extDir}' skipped: {ex.Message}");
            }
        }
    }

    private static async Task<Extension> EvaluateManifestAsync(
        string manifestPath,
        string extDir,
        ExtensionTier tier,
        CancellationToken ct)
    {
        // Lua I/O: evaluate the manifest script to get the raw table.
        using var state = CreateSandboxedState();
        var script = await File.ReadAllTextAsync(manifestPath, ct).ConfigureAwait(false);
        var results = await state.DoStringAsync(script, manifestPath, ct).ConfigureAwait(false);

        if (results.Length == 0 || !results[0].TryRead<LuaTable>(out var manifest))
            throw new InvalidOperationException("manifest.lua must return a table.");

        // Domain mapping: translate the Lua table to a typed Extension.
        return ManifestParser.FromLuaTable(manifest, extDir, tier);
    }

    private static LuaState CreateSandboxedState()
    {
        var platform = new LuaPlatform(
            FileSystem: new NoOpFileSystem(),
            OsEnvironment: new NoOpOsEnvironment(),
            StandardIO: new NoOpStandardIo(),
            TimeProvider: TimeProvider.System);

        var state = LuaState.Create(platform);

        // Only open safe libraries — no IO, OS, module, or debug.
        state.OpenBasicLibrary();
        state.OpenStringLibrary();
        state.OpenTableLibrary();
        state.OpenMathLibrary();

        return state;
    }

    private static void InjectToolApi(LuaState state, Extension extension)
    {
        var toolTable = new LuaTable
        {
            ["register"] = new LuaFunction("ur.tool.register", (context, _) =>
        {
            var def = context.GetArgument<LuaTable>(0);

            var name = SanitizeToolName(ReadRequiredString(def, "name"));
            var description = ReadOptionalString(def, "description") ?? "";
            var handler = def["handler"].TryRead<LuaFunction>(out var fn)
                ? fn
                : throw new InvalidOperationException(
                    $"ur.tool.register: tool '{name}' missing 'handler' function.");

            var schema = def["parameters"].TryRead<LuaTable>(out var paramsTable)
                ? LuaJsonHelpers.ToJsonElement(paramsTable)
                : EmptyObjectSchema();

            var adapter = new LuaToolAdapter(name, description, schema, state, handler);
            extension.RegisterTool(adapter);

            return new ValueTask<int>(context.Return());
        })
        };
        var urTable = new LuaTable
        {
            ["tool"] = toolTable
        };
        state.Environment["ur"] = urTable;
    }

    private static JsonElement EmptyObjectSchema()
    {
        var doc = JsonDocument.Parse("""{"type":"object","properties":{}}""");
        return doc.RootElement.Clone();
    }

    // --- String helpers ---

    private static string ReadRequiredString(LuaTable table, string key) =>
        table[key].TryRead<string>(out var s)
            ? s
            : throw new InvalidOperationException($"Missing required field '{key}'.");

    private static string? ReadOptionalString(LuaTable table, string key)
    {
        return table[key].TryRead<string>(out var s) ? s : null;
    }

    [GeneratedRegex("[^a-zA-Z0-9_-]")]
    private static partial Regex InvalidToolNameCharsRegex();

    private static string SanitizeToolName(string name) =>
        InvalidToolNameCharsRegex().Replace(name, "_");

    private static int TierSortOrder(ExtensionTier tier) =>
        tier switch
        {
            ExtensionTier.System => 0,
            ExtensionTier.User => 1,
            ExtensionTier.Workspace => 2,
            _ => throw new ArgumentOutOfRangeException(nameof(tier), tier, null)
        };

    // --- Sandboxed platform implementations ---

    private sealed class NoOpFileSystem : ILuaFileSystem
    {
        private static InvalidOperationException CreateSandboxViolation(string capability) =>
            new($"{capability} is not permitted in extensions.");

        public string DirectorySeparator => "/";

        public bool IsReadable(string path) => false;

        public ValueTask<ILuaStream> Open(
            string path, LuaFileOpenMode mode, CancellationToken cancellationToken) =>
            throw CreateSandboxViolation("filesystem access");

        public ValueTask Rename(
            string oldName, string newName, CancellationToken cancellationToken) =>
            throw CreateSandboxViolation("filesystem access");

        public ValueTask Remove(string path, CancellationToken cancellationToken) =>
            throw CreateSandboxViolation("filesystem access");

        public string GetTempFileName() =>
            throw CreateSandboxViolation("filesystem access");

        public ValueTask<ILuaStream> OpenTempFileStream(CancellationToken cancellationToken) =>
            throw CreateSandboxViolation("filesystem access");
    }

    private sealed class NoOpOsEnvironment : ILuaOsEnvironment
    {
        public string GetEnvironmentVariable(string name) => "";

        public ValueTask Exit(int exitCode, CancellationToken cancellationToken) =>
            throw new InvalidOperationException("os.exit is not permitted in extensions.");

        public double GetTotalProcessorTime() => 0;
    }

    private sealed class NoOpStandardIo : ILuaStandardIO
    {
        public ILuaStream Input { get; } = ILuaStream.CreateFromString("");
        public ILuaStream Output { get; } = ILuaStream.CreateFromString("");
        public ILuaStream Error { get; } = ILuaStream.CreateFromString("");
    }
}
