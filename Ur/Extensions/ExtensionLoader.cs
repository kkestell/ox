using System.Text.Json;
using Lua;
using Lua.IO;
using Lua.Platforms;
using Lua.Standard;
using Ur.AgentLoop;

namespace Ur.Extensions;

/// <summary>
/// Discovers extension directories, evaluates manifests, initializes Lua runtimes,
/// and wires up the <c>ur.tool.register</c> API.
/// </summary>
internal static class ExtensionLoader
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
        ToolRegistry toolRegistry,
        CancellationToken ct = default)
    {
        extension.ResetRuntimeState(toolRegistry);

        var mainPath = Path.Combine(extension.Directory, "main.lua");
        if (!File.Exists(mainPath))
        {
            extension.MarkActivated(toolRegistry);
            return;
        }

        try
        {
            var state = CreateSandboxedState();
            extension.LuaState = state;
            InjectToolApi(state, extension);

            var script = await File.ReadAllTextAsync(mainPath, ct).ConfigureAwait(false);
            await state.DoStringAsync(script, mainPath, ct).ConfigureAwait(false);

            extension.MarkActivated(toolRegistry);
        }
        catch (Exception ex) when (
            ex is LuaRuntimeException or LuaParseException or LuaCompileException or InvalidOperationException)
        {
            extension.MarkActivationFailed(toolRegistry, ex.Message);
            Console.Error.WriteLine(
                $"Extension '{extension.Name}': main.lua failed: {ex.Message}");
        }
    }

    public static void Deactivate(Extension extension, ToolRegistry toolRegistry) =>
        extension.MarkDeactivated(toolRegistry);

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
                    Console.Error.WriteLine(
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
                Console.Error.WriteLine(
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
        using var state = CreateSandboxedState();
        var script = await File.ReadAllTextAsync(manifestPath, ct).ConfigureAwait(false);
        var results = await state.DoStringAsync(script, manifestPath, ct).ConfigureAwait(false);

        if (results.Length == 0 || !results[0].TryRead<LuaTable>(out var manifest))
            throw new InvalidOperationException("manifest.lua must return a table.");

        var name = ReadRequiredString(manifest, "name");
        var version = ReadRequiredString(manifest, "version");
        var description = ReadOptionalString(manifest, "description") ?? "";

        var settingsSchemas = new Dictionary<string, JsonElement>(StringComparer.Ordinal);
        if (manifest["settings"].TryRead<LuaTable>(out var settingsTable))
        {
            foreach (var (key, value) in settingsTable)
            {
                if (key.TryRead<string>(out var settingKey) &&
                    value.TryRead<LuaTable>(out var schemaTable))
                {
                    settingsSchemas[settingKey] = LuaTableToJsonElement(schemaTable);
                }
            }
        }

        return new Extension(
            new ExtensionDescriptor(
                new ExtensionId(tier, name),
                description,
                version,
                extDir,
                settingsSchemas));
    }

    private static LuaState CreateSandboxedState()
    {
        var platform = new LuaPlatform(
            FileSystem: new NoOpFileSystem(),
            OsEnvironment: new NoOpOsEnvironment(),
            StandardIO: new NoOpStandardIO(),
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
        var urTable = new LuaTable();
        var toolTable = new LuaTable();

        toolTable["register"] = new LuaFunction("ur.tool.register", (context, _) =>
        {
            var def = context.GetArgument<LuaTable>(0);

            var name = ReadRequiredString(def, "name");
            var description = ReadOptionalString(def, "description") ?? "";
            var handler = def["handler"].TryRead<LuaFunction>(out var fn)
                ? fn
                : throw new InvalidOperationException(
                    $"ur.tool.register: tool '{name}' missing 'handler' function.");

            JsonElement schema;
            if (def["parameters"].TryRead<LuaTable>(out var paramsTable))
                schema = LuaTableToJsonElement(paramsTable);
            else
                schema = EmptyObjectSchema();

            var adapter = new LuaToolAdapter(name, description, schema, state, handler);
            extension.RegisterTool(adapter);

            return new ValueTask<int>(context.Return());
        });

        urTable["tool"] = toolTable;
        state.Environment["ur"] = urTable;
    }

    // --- Lua table → JSON Schema conversion ---

    /// <summary>
    /// Converts a Lua table representing a JSON Schema into a <see cref="JsonElement"/>.
    /// Handles: type, properties, required, description, items, enum, default.
    /// </summary>
    internal static JsonElement LuaTableToJsonElement(LuaTable table)
    {
        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream))
        {
            WriteLuaTableAsJson(writer, table);
        }

        var doc = JsonDocument.Parse(stream.ToArray());
        return doc.RootElement.Clone();
    }

    private static void WriteLuaTableAsJson(Utf8JsonWriter writer, LuaTable table)
    {
        // Detect array: has array portion and no hash entries.
        if (table.ArrayLength > 0 && table.HashMapCount == 0)
        {
            writer.WriteStartArray();
            for (var i = 1; i <= table.ArrayLength; i++)
                WriteLuaValueAsJson(writer, table[i]);
            writer.WriteEndArray();
        }
        else
        {
            writer.WriteStartObject();
            foreach (var (key, value) in table)
            {
                if (!key.TryRead<string>(out var k))
                    continue;
                writer.WritePropertyName(k);
                WriteLuaValueAsJson(writer, value);
            }
            writer.WriteEndObject();
        }
    }

    private static void WriteLuaValueAsJson(Utf8JsonWriter writer, LuaValue value)
    {
        if (value.TryRead<string>(out var s))
            writer.WriteStringValue(s);
        else if (value.TryRead<double>(out var d))
            writer.WriteNumberValue(d);
        else if (value.TryRead<bool>(out var b))
            writer.WriteBooleanValue(b);
        else if (value.TryRead<LuaTable>(out var t))
            WriteLuaTableAsJson(writer, t);
        else
            writer.WriteNullValue();
    }

    private static JsonElement EmptyObjectSchema()
    {
        var doc = JsonDocument.Parse("""{"type":"object","properties":{}}""");
        return doc.RootElement.Clone();
    }

    // --- String helpers ---

    private static string ReadRequiredString(LuaTable table, string key)
    {
        if (table[key].TryRead<string>(out var s))
            return s;
        throw new InvalidOperationException($"Missing required field '{key}'.");
    }

    private static string? ReadOptionalString(LuaTable table, string key)
    {
        return table[key].TryRead<string>(out var s) ? s : null;
    }

    private static int TierSortOrder(ExtensionTier tier) =>
        tier switch
        {
            ExtensionTier.System => 0,
            ExtensionTier.User => 1,
            ExtensionTier.Workspace => 2,
            _ => throw new ArgumentOutOfRangeException(nameof(tier), tier, null),
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

    private sealed class NoOpStandardIO : ILuaStandardIO
    {
        public ILuaStream Input { get; } = ILuaStream.CreateFromString("");
        public ILuaStream Output { get; } = ILuaStream.CreateFromString("");
        public ILuaStream Error { get; } = ILuaStream.CreateFromString("");
    }
}
