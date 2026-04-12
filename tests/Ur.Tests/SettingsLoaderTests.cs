using System.Text.Json;
using Microsoft.Extensions.Configuration;
using Ur.Configuration;
using Ur.Settings;

namespace Ur.Tests;

/// <summary>
/// Tests for the configuration pipeline that replaced <c>SettingsLoader</c>:
/// <see cref="UrSettingsConfigurationProvider"/> (load + migration) and
/// <see cref="SettingsWriter"/> (write + validate + reload).
///
/// The merge contract is foundational to configuration: workspace values
/// must override user values for the same key, and validation must reject
/// values whose types don't match their registered schemas.
/// </summary>
public sealed class SettingsConfigurationTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "ur-settings-config-tests",
        Guid.NewGuid().ToString("N"));

    public SettingsConfigurationTests()
    {
        Directory.CreateDirectory(_root);
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
            Directory.Delete(_root, recursive: true);
    }

    // ─── Provider: Merge semantics ───────────────────────────────────

    [Fact]
    public void Provider_WorkspaceOverridesUser()
    {
        var userPath = WriteTempJson("""{"ur": {"color": "blue"}}""");
        var workspacePath = WriteTempJson("""{"ur": {"color": "red"}}""");

        var config = BuildConfig(userPath, workspacePath);

        Assert.Equal("red", config["ur:color"]);
    }

    [Fact]
    public void Provider_DisjointKeys_BothPresent()
    {
        var userPath = WriteTempJson("""{"user": {"key": "u"}}""");
        var workspacePath = WriteTempJson("""{"workspace": {"key": "w"}}""");

        var config = BuildConfig(userPath, workspacePath);

        Assert.Equal("u", config["user:key"]);
        Assert.Equal("w", config["workspace:key"]);
    }

    [Fact]
    public void Provider_EmptyWorkspace_ReturnsUserValues()
    {
        var userPath = WriteTempJson("""{"ns": {"name": "alice"}}""");
        var workspacePath = Path.Combine(_root, "nonexistent.json");

        var config = BuildConfig(userPath, workspacePath);

        Assert.Equal("alice", config["ns:name"]);
    }

    [Fact]
    public void Provider_MissingFiles_ReturnsEmpty()
    {
        var config = BuildConfig(
            Path.Combine(_root, "missing-user.json"),
            Path.Combine(_root, "missing-workspace.json"));

        Assert.Null(config["anything"]);
    }

    // ─── Writer: Validate ────────────────────────────────────────────

    [Fact]
    public void Writer_Set_ValidationFailure_ThrowsAndDoesNotPersist()
    {
        var userPath = WriteTempJson("{}");

        var registry = BuildRegistryWithBoolSchema("test.flag");
        var writerWithSchema = BuildWriterWithRegistry(registry, userPath, null);

        // Write a string where boolean is expected — should throw.
        var stringValue = JsonSerializer.SerializeToElement("not-a-bool", SettingsJsonContext.Default.String);
        Assert.Throws<SettingsValidationException>(
            () => writerWithSchema.Set("test.flag", stringValue, ConfigurationScope.User));

        // The failed write should not have modified the file. Verify by checking
        // the value is not present rather than comparing raw strings (avoids
        // sensitivity to JSON formatting).
        Assert.Null(writerWithSchema.Get("test.flag"));
    }

    [Fact]
    public void Writer_Set_ValidValue_Persists()
    {
        var userPath = WriteTempJson("{}");
        var (writer, config) = BuildWriter(userPath, null);

        var stringValue = JsonSerializer.SerializeToElement("hello", SettingsJsonContext.Default.String);
        writer.Set("test.key", stringValue, ConfigurationScope.User);

        // Should be readable from IConfiguration after reload.
        Assert.Equal("hello", config["test:key"]);
    }

    [Fact]
    public void Writer_Clear_RemovesKeyAndReloads()
    {
        var userPath = WriteTempJson("""{"test": {"key": "value"}}""");
        var (writer, config) = BuildWriter(userPath, null);

        Assert.Equal("value", config["test:key"]);

        writer.Clear("test.key", ConfigurationScope.User);

        Assert.Null(config["test:key"]);
    }

    [Fact]
    public void Writer_ScopeMerge_WorkspaceOverridesUser()
    {
        var userPath = WriteTempJson("""{"test": {"value": "from-user"}}""");
        var workspacePath = WriteTempJson("{}");
        var (writer, config) = BuildWriter(userPath, workspacePath);

        Assert.Equal("from-user", config["test:value"]);

        // Write to workspace scope.
        var wsValue = JsonSerializer.SerializeToElement("from-workspace", SettingsJsonContext.Default.String);
        writer.Set("test.value", wsValue, ConfigurationScope.Workspace);

        // Workspace should override user.
        Assert.Equal("from-workspace", config["test:value"]);

        // Clear workspace — user value surfaces again.
        writer.Clear("test.value", ConfigurationScope.Workspace);
        Assert.Equal("from-user", config["test:value"]);
    }

    [Fact]
    public async Task Writer_Get_ReturnsTypedJsonElement()
    {
        var userPath = WriteTempJson("""{"ns": {"flag": true, "name": "alice"}}""");
        var (writer, _) = BuildWriter(userPath, null);

        var flag = writer.Get("ns.flag");
        Assert.NotNull(flag);
        Assert.Equal(JsonValueKind.True, flag.Value.ValueKind);

        var name = writer.Get("ns.name");
        Assert.NotNull(name);
        Assert.Equal(JsonValueKind.String, name.Value.ValueKind);
        Assert.Equal("alice", name.Value.GetString());
    }

    [Fact]
    public void Writer_Get_MissingKey_ReturnsNull()
    {
        var userPath = WriteTempJson("{}");
        var (writer, _) = BuildWriter(userPath, null);

        Assert.Null(writer.Get("nonexistent.key"));
    }

    [Fact]
    public void Writer_Validate_UnknownKey_Tolerated()
    {
        // Unknown keys should not cause errors — they may come from
        // extensions that are no longer installed.
        var userPath = WriteTempJson("{}");
        var registry = new SettingsSchemaRegistry();
        var writer = BuildWriterWithRegistry(registry, userPath, null);

        var stringValue = JsonSerializer.SerializeToElement("anything", SettingsJsonContext.Default.String);

        // Should not throw.
        writer.Set("unknown.key", stringValue, ConfigurationScope.User);
    }

    // ─── Writer: Round-trip type fidelity ───────────────────────────

    [Fact]
    public void Writer_Set_BoolValue_RoundTripsWithCorrectType()
    {
        // Verifies that a boolean written via Set is read back as a
        // boolean JsonElement, not as a string — the writer must preserve
        // JSON types through the file round-trip.
        var userPath = WriteTempJson("{}");
        var (writer, _) = BuildWriter(userPath, null);

        var boolValue = JsonSerializer.SerializeToElement(true, SettingsJsonContext.Default.Boolean);
        writer.Set("test.flag", boolValue, ConfigurationScope.User);

        var result = writer.Get("test.flag");
        Assert.NotNull(result);
        Assert.Equal(JsonValueKind.True, result.Value.ValueKind);
    }

    // ─── Writer: Namespace cleanup on clear ──────────────────────────

    [Fact]
    public void Writer_Clear_RemovesEmptyNamespaceFromFile()
    {
        // When the last key in a namespace is cleared, the namespace object
        // itself should be removed from the file for cleanliness.
        var userPath = WriteTempJson("""{"test": {"only-key": "value"}}""");
        var (writer, _) = BuildWriter(userPath, null);

        writer.Clear("test.only-key", ConfigurationScope.User);

        // The file should not contain an empty "test" object.
        var json = File.ReadAllText(userPath);
        using var doc = JsonDocument.Parse(json);
        Assert.False(doc.RootElement.TryGetProperty("test", out _),
            "Expected the 'test' namespace to be removed after its last key was cleared");
    }

    // ─── Standard options pipeline tests ────────────────────────────

    [Fact]
    public void Options_ReadsModelFromUrSection()
    {
        var userPath = WriteTempJson("""{"ur": {"model": "openai/gpt-4o"}}""");
        var config = BuildConfig(userPath, null);
        var options = config.GetSection("ur").Get<UrOptions>();

        Assert.NotNull(options);
        Assert.Equal("openai/gpt-4o", options.Model);
    }

    [Fact]
    public void Options_NullWhenModelNotSet()
    {
        var userPath = WriteTempJson("{}");
        var config = BuildConfig(userPath, null);
        var options = config.GetSection("ur").Get<UrOptions>();

        // Get<T> returns null when the section is entirely absent.
        Assert.True(options is null || options.Model is null);
    }

    [Fact]
    public void Options_ReflectsReloadAfterWrite()
    {
        // Verifies that binding UrOptions from the "ur" section sees the new
        // value after SettingsWriter writes and reloads the IConfigurationRoot.
        var userPath = WriteTempJson("{}");
        var config = BuildConfig(userPath, null);
        var writer = new SettingsWriter(new SettingsSchemaRegistry(), config, userPath, null);

        var before = config.GetSection("ur").Get<UrOptions>();
        Assert.True(before is null || before.Model is null);

        var modelValue = JsonSerializer.SerializeToElement("anthropic/claude-3", SettingsJsonContext.Default.String);
        writer.Set("ur.model", modelValue, ConfigurationScope.User);

        var after = config.GetSection("ur").Get<UrOptions>();
        Assert.NotNull(after);
        Assert.Equal("anthropic/claude-3", after.Model);
    }

    // ─── Helpers ──────────────────────────────────────────────────────

    private string WriteTempJson(string json)
    {
        var path = Path.Combine(_root, $"{Guid.NewGuid():N}.json");
        File.WriteAllText(path, json);
        return path;
    }

    private static IConfigurationRoot BuildConfig(string? userPath, string? workspacePath)
    {
        var builder = new ConfigurationBuilder();
        builder.Add(new UrSettingsConfigurationSource(userPath));
        if (workspacePath is not null)
            builder.Add(new UrSettingsConfigurationSource(workspacePath));
        return builder.Build();
    }

    private static (SettingsWriter Writer, IConfigurationRoot Config) BuildWriter(
        string? userPath, string? workspacePath)
    {
        var registry = new SettingsSchemaRegistry();
        var config = BuildConfig(userPath, workspacePath);
        var writer = new SettingsWriter(registry, config, userPath, workspacePath);
        return (writer, config);
    }

    private static SettingsWriter BuildWriterWithRegistry(
        SettingsSchemaRegistry registry,
        string? userPath,
        string? workspacePath)
    {
        var config = BuildConfig(userPath, workspacePath);
        return new SettingsWriter(registry, config, userPath, workspacePath);
    }

    private static SettingsSchemaRegistry BuildRegistryWithBoolSchema(string key)
    {
        var registry = new SettingsSchemaRegistry();
        using var doc = JsonDocument.Parse("""{"type": "boolean"}""");
        registry.Register(key, doc.RootElement.Clone());
        return registry;
    }
}
